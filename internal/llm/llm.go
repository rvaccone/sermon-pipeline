// Package llm is the one place the pipeline talks to Claude. Every call is a single headless
// Claude Code request (`claude -p`) that runs on the user's Claude subscription, with a strict JSON
// schema derived from a Go struct, so callers get typed results, never free text.
package llm

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/rvaccone/sermon-pipeline/internal/command"
)

// Asker sends one structured request. out must be a pointer to a struct: its fields (and their
// jsonschema tags) define the schema Claude's answer must follow, and the answer is decoded into it.
type Asker interface {
	Ask(ctx context.Context, prompt Prompt, input string, out any) error
}

// Prompt is a versioned system prompt from the prompts directory.
type Prompt struct {
	Name    string
	Text    string
	Version string // first 8 hex digits of the prompt's SHA-256, recorded with every run
}

//go:embed prompts/*.md
var promptFiles embed.FS

// Load returns the named prompt (prompts/<name>.md). Prompts ship inside the binary, so a missing
// one is a programming error.
func Load(name string) Prompt {
	data, err := promptFiles.ReadFile("prompts/" + name + ".md")
	if err != nil {
		panic(fmt.Sprintf("llm: no prompt named %q", name))
	}
	sum := sha256.Sum256(data)
	return Prompt{Name: name, Text: strings.TrimSpace(string(data)), Version: hex.EncodeToString(sum[:4])}
}

// Client asks Claude through the Claude Code CLI in headless mode.
type Client struct {
	model  string
	effort string
}

// New checks that the Claude Code CLI is installed. It authenticates with the signed-in Claude
// subscription; no API key is used.
func New(model, effort string) (*Client, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return nil, fmt.Errorf("the Claude Code CLI (`claude`) is not installed or not on PATH")
	}
	return &Client{model: model, effort: effort}, nil
}

// Ask runs one isolated headless request. Claude gets no tools, no MCP servers, no user or project
// settings, no slash commands and no saved session, and runs in an empty temporary directory: it
// can only read the input and answer in the schema. It cannot reach YouTube, SermonShots or any
// other service or file.
func (c *Client) Ask(ctx context.Context, p Prompt, input string, out any) error {
	schema, err := Schema(out)
	if err != nil {
		return fmt.Errorf("%s: %w", p.Name, err)
	}
	dir, err := os.MkdirTemp("", "sermon-claude-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	stdout, err := command.Pipe(ctx, dir, strings.NewReader(input), "claude",
		"--print", "--output-format", "json",
		"--model", c.model, "--effort", c.effort,
		"--system-prompt", p.Text,
		"--json-schema", schema,
		"--tools", "",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--setting-sources", "",
		"--disable-slash-commands",
		"--no-session-persistence")
	if err != nil {
		return fmt.Errorf("%s: %w", p.Name, err)
	}
	var result struct {
		Subtype    string          `json:"subtype"`
		IsError    bool            `json:"is_error"`
		Result     string          `json:"result"`
		Structured json.RawMessage `json:"structured_output"`
	}
	if err := json.Unmarshal(stdout, &result); err != nil {
		return fmt.Errorf("%s: reading Claude's reply: %w", p.Name, err)
	}
	if result.IsError || len(result.Structured) == 0 || string(result.Structured) == "null" {
		return fmt.Errorf("%s: Claude did not return an answer (%s): %.300s", p.Name, result.Subtype, result.Result)
	}
	if err := json.Unmarshal(result.Structured, out); err != nil {
		return fmt.Errorf("%s: decoding answer: %w", p.Name, err)
	}
	return nil
}

// Schema derives the strict JSON schema Claude must follow from out's struct type: every field
// required, no extra fields. Field descriptions come from `jsonschema_description` tags (the
// `jsonschema:"description=…"` form would stop at the first comma).
func Schema(out any) (string, error) {
	reflector := jsonschema.Reflector{DoNotReference: true, ExpandedStruct: true}
	schema := reflector.Reflect(out)
	// The CLI's validator doesn't resolve the draft 2020-12 meta-schema reference; the schema is
	// valid without it.
	schema.Version, schema.ID = "", ""
	data, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
