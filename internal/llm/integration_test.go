package llm

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestHeadlessClaude makes one real `claude -p` request on the signed-in subscription. It is skipped
// unless SERMON_CLAUDE_TEST=1, since it needs the network and uses the subscription.
func TestHeadlessClaude(t *testing.T) {
	if os.Getenv("SERMON_CLAUDE_TEST") != "1" {
		t.Skip("set SERMON_CLAUDE_TEST=1 to run")
	}
	client, err := New("claude-opus-5-5", "low")
	if err != nil {
		t.Fatal(err)
	}
	var answer struct {
		Words []struct {
			Word string `json:"word" jsonschema_description:"A word from the input, copied exactly"`
			Kind string `json:"kind" jsonschema:"enum=noun,enum=verb,enum=other"`
		} `json:"words"`
		Confidence string `json:"confidence" jsonschema:"enum=high,enum=medium,enum=low"`
	}
	prompt := Prompt{Name: "integration", Text: "Classify each word of the input."}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := client.Ask(ctx, prompt, "Grace abounds", &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Words) != 2 || answer.Confidence == "" {
		t.Errorf("answer = %+v", answer)
	}
}
