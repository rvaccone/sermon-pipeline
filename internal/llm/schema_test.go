package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchemaIsStrictAndKeepsDescriptions(t *testing.T) {
	type answer struct {
		Time  string   `json:"time" jsonschema_description:"The time printed on the line, e.g. 24:41"`
		Notes []string `json:"notes"`
		Level string   `json:"level" jsonschema:"enum=high,enum=low"`
	}
	schema, err := Schema(&answer{})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Type                 string         `json:"type"`
		Required             []string       `json:"required"`
		AdditionalProperties *bool          `json:"additionalProperties"`
		Properties           map[string]any `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schema), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Type != "object" || len(doc.Required) != 3 || doc.AdditionalProperties == nil || *doc.AdditionalProperties {
		t.Errorf("schema is not strict: %s", schema)
	}
	if strings.Contains(schema, "$schema") || strings.Contains(schema, "$id") {
		t.Errorf("the claude CLI rejects $schema and $id references: %s", schema)
	}
	if !strings.Contains(schema, "The time printed on the line, e.g. 24:41") || !strings.Contains(schema, `"enum":["high","low"]`) {
		t.Errorf("schema lost a description or enum: %s", schema)
	}
}

func TestPromptsLoadWithVersions(t *testing.T) {
	for _, name := range []string{"sermon-boundaries", "transcript-corrections", "descriptions", "clip-candidates", "clip-review"} {
		if p := Load(name); p.Text == "" || len(p.Version) != 8 {
			t.Errorf("prompt %s: %+v", name, p)
		}
	}
}
