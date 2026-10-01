package church

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rvaccone/sermon-pipeline/internal/config"
	"github.com/rvaccone/sermon-pipeline/internal/llm"
)

const homepage = `<!doctype html><html><head><title>Home | Grace Church</title>
<meta name="description" content="Grace Church is a family in Tampa, FL.">
<script>var tracking = "ignore me";</script><style>p { color: red }</style></head>
<body><nav><a href="/about-us">About</a> <a href="/our-team">Leadership</a>
<a href="https://www.grace.example/contact#map">Contact</a> <a href="/give">Give</a>
<a href="https://facebook.com/grace">Facebook</a> <a href="/bulletin.pdf">Bulletin</a>
<a href="mailto:hi@grace.example">Email</a></nav>
<h1>Welcome   home</h1><p>Sundays at 10am.</p><p>Sundays at 10am.</p></body></html>`

func TestParseAndPick(t *testing.T) {
	base, _ := url.Parse("https://grace.example/")
	page, links, err := parse(strings.NewReader(homepage), base)
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "Home | Grace Church" {
		t.Errorf("title = %q", page.Title)
	}
	for _, want := range []string{"Grace Church is a family in Tampa, FL.", "Welcome home", "Sundays at 10am."} {
		if !strings.Contains(page.Text, want) {
			t.Errorf("text is missing %q:\n%s", want, page.Text)
		}
	}
	if strings.Contains(page.Text, "tracking") || strings.Contains(page.Text, "color") || strings.Count(page.Text, "Sundays") != 1 {
		t.Errorf("text kept scripts, styles or repeated lines:\n%s", page.Text)
	}

	var got []string
	for _, u := range pick(base, links, 5) {
		got = append(got, u.String())
	}
	want := []string{"https://grace.example/about-us", "https://grace.example/our-team", "https://www.grace.example/contact"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("picked %q, want %q (own site, about/team/contact; no giving page, PDF, mail or other sites)", got, want)
	}
}

func TestHomepage(t *testing.T) {
	for in, want := range map[string]string{
		"feathersoundchurch.com":          "https://feathersoundchurch.com/",
		"https://www.grace.example/about": "https://www.grace.example/about",
	} {
		u, err := Homepage(in)
		if err != nil || u.String() != want {
			t.Errorf("Homepage(%q) = %v, %v; want %s", in, u, err, want)
		}
	}
	if u, _ := Homepage("https://www.grace.example"); Domain(u) != "grace.example" {
		t.Errorf("Domain = %q", Domain(u))
	}
	if _, err := Homepage("http://"); err == nil {
		t.Error("an address without a host was accepted")
	}
}

type fakeAsker struct{ reply string }

func (f fakeAsker) Ask(_ context.Context, _ llm.Prompt, _ string, out any) error {
	return json.Unmarshal([]byte(f.reply), out)
}

func TestLearnWritesAConfigThatLoads(t *testing.T) {
	reply := `{"name":"Grace Church","location":"Tampa, FL","mission":"Love God,\n love \"people\".",
		"podcast":"","preachers":["Pastor Jo Smith","Pastor Jo Smith"],"glossary":["Grace Kids","Tampa"],"notes":[]}`
	pages := []Page{{URL: "https://grace.example/"}, {URL: "https://grace.example/about-us"}}
	d, err := Learn(context.Background(), fakeAsker{reply}, "https://grace.example/", pages)
	if err != nil {
		t.Fatal(err)
	}
	text := Config(d, "grace.example", "2026-10-01", pages)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("the written config doesn't load: %v\n%s", err, text)
	}
	c := cfg.Church
	if c.Name != "Grace Church" || c.Website != "grace.example" || c.Location != "Tampa, FL" ||
		c.Mission != `Love God, love "people".` || c.Podcast != "Grace Church" ||
		!slices.Equal(c.Preachers, []string{"Pastor Jo Smith"}) ||
		!slices.Equal(cfg.Transcription.Glossary, []string{"Grace Kids", "Tampa"}) {
		t.Errorf("loaded %+v / %q from:\n%s", c, cfg.Transcription.Glossary, text)
	}
	if !strings.Contains(text, "https://grace.example/about-us") {
		t.Errorf("the config doesn't say which pages it came from:\n%s", text)
	}
}

func TestConfigMarksWhatIsMissing(t *testing.T) {
	text := Config(Details{Name: "Grace Church"}, "grace.example", "2026-10-01", nil)
	for _, want := range []string{`location = "" # TODO`, `preachers = [] # TODO`, `glossary = [] # TODO`} {
		if !strings.Contains(text, want) {
			t.Errorf("config is missing %q:\n%s", want, text)
		}
	}
}
