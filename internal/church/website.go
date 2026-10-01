// Package church learns a church's details from its website, so `sermon init` can write a
// starting config.toml. The pages are fetched here; Claude only reads their text.
package church

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Page is the readable text of one web page.
type Page struct {
	URL   string
	Title string
	Text  string
}

const (
	maxPages     = 8       // the homepage plus the most promising links
	maxPageBytes = 2 << 20 // larger responses are cut off
	maxPageChars = 12000   // text kept per page; the details are near the top
)

// interesting are words in a link that suggest the page has the church's details.
var interesting = []string{
	"about", "who-we-are", "our-story", "story", "mission", "vision", "believe", "beliefs",
	"staff", "team", "leadership", "pastor", "pastors", "elders", "contact", "visit", "location",
	"ministries", "ministry", "podcast", "sermons", "messages", "watch", "new",
}

// Read fetches the homepage and up to maxPages-1 pages it links to that look like they describe
// the church (About, Staff, Contact, …). Pages on other sites are not followed.
func Read(ctx context.Context, site string) ([]Page, error) {
	home, err := Homepage(site)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 20 * time.Second}
	first, links, err := fetch(ctx, client, home)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", home, err)
	}
	pages := []Page{first}
	for _, link := range pick(home, links, maxPages-1) {
		page, _, err := fetch(ctx, client, link)
		if err != nil {
			continue // a broken link shouldn't stop the rest
		}
		pages = append(pages, page)
	}
	return pages, nil
}

// Homepage turns what a person typed ("feathersoundchurch.com") into the homepage's URL.
func Homepage(site string) (*url.URL, error) {
	site = strings.TrimSpace(site)
	if !strings.Contains(site, "://") {
		site = "https://" + site
	}
	u, err := url.Parse(site)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%q is not a website address", site)
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}

// Domain is the website as people write it: "feathersoundchurch.com".
func Domain(home *url.URL) string {
	return strings.TrimPrefix(home.Hostname(), "www.")
}

// link is an <a> on a page: where it goes and what it says.
type link struct {
	URL  *url.URL
	Text string
}

func fetch(ctx context.Context, client *http.Client, u *url.URL) (Page, []link, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Page{}, nil, err
	}
	// Some site builders turn away requests that don't look like a browser.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; compatible; sermon-pipeline)")
	resp, err := client.Do(req)
	if err != nil {
		return Page{}, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Page{}, nil, fmt.Errorf("%s", resp.Status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "html") {
		return Page{}, nil, fmt.Errorf("not a web page (%s)", ct)
	}
	page, links, err := parse(io.LimitReader(resp.Body, maxPageBytes), resp.Request.URL)
	page.URL = resp.Request.URL.String()
	return page, links, err
}

// parse extracts a page's title, readable text (one line per block) and links. The page's meta
// description comes first, since site builders often put the church's summary there.
func parse(r io.Reader, base *url.URL) (Page, []link, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return Page{}, nil, err
	}
	var (
		page  Page
		links []link
		text  strings.Builder
	)
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "svg", "template", "iframe":
				return
			case "title":
				page.Title = strings.TrimSpace(textOf(n))
				return
			case "meta":
				if name := attr(n, "name") + attr(n, "property"); name == "description" || name == "og:description" {
					if content := strings.TrimSpace(attr(n, "content")); content != "" && !strings.Contains(text.String(), content) {
						text.WriteString(content + "\n")
					}
				}
			case "a":
				if href, err := base.Parse(attr(n, "href")); err == nil {
					links = append(links, link{href, strings.TrimSpace(textOf(n))})
				}
			case "br", "p", "div", "section", "article", "header", "footer", "li", "h1", "h2", "h3", "h4", "h5", "h6", "tr", "nav":
				text.WriteString("\n")
			}
		}
		if n.Type == html.TextNode {
			text.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	page.Text = tidy(text.String())
	if r := []rune(page.Text); len(r) > maxPageChars {
		page.Text = string(r[:maxPageChars])
	}
	return page, links, nil
}

// pick chooses up to n links on the church's own site that most look like they describe it.
func pick(home *url.URL, links []link, n int) []*url.URL {
	type scored struct {
		url   *url.URL
		score int
	}
	seen := map[string]bool{home.Path: true}
	var candidates []scored
	for _, l := range links {
		u := *l.URL
		u.Fragment, u.RawQuery = "", ""
		if Domain(&u) != Domain(home) || (u.Scheme != "http" && u.Scheme != "https") || seen[u.Path] {
			continue
		}
		if ext := path.Ext(u.Path); ext != "" && ext != ".html" && ext != ".htm" && ext != ".php" {
			continue // a PDF, image or download
		}
		seen[u.Path] = true
		words := strings.ToLower(u.Path + " " + strings.ReplaceAll(l.Text, " ", "-"))
		score := 0
		for _, w := range interesting {
			if strings.Contains(words, w) {
				score++
			}
		}
		if score > 0 {
			candidates = append(candidates, scored{&u, score})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	var out []*url.URL
	for _, c := range candidates[:min(n, len(candidates))] {
		out = append(out, c.url)
	}
	return out
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// tidy collapses runs of spaces and drops empty and repeated lines (menus repeat on every page).
func tidy(s string) string {
	var lines []string
	seen := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
