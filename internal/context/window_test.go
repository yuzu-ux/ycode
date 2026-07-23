package context

import (
	"strings"
	"testing"

	"github.com/yuzu-ux/ycode/internal/provider"
)

func TestFitDropsWholeOldTurns(t *testing.T) {
	large := strings.Repeat("old context ", 400)
	history := []provider.Message{
		{Role: "user", Content: "first request " + large},
		{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "one", Type: "function", Function: provider.FunctionCall{Name: "workspace", Arguments: `{"action":"read"}`}}}},
		{Role: "tool", ToolCallID: "one", Name: "workspace", Content: large},
		{Role: "assistant", Content: "first result"},
		{Role: "user", Content: "latest request"},
		{Role: "assistant", Content: "latest result"},
	}
	capsule := NewCapsule()
	messages, stats := Fit("system", "map", history, nil, 500, capsule)

	if stats.DroppedTurns != 1 {
		t.Fatalf("dropped turns = %d, want 1", stats.DroppedTurns)
	}
	encoded := messageText(messages)
	if strings.Contains(encoded, large) {
		t.Fatal("large old turn remained in provider context")
	}
	if !strings.Contains(encoded, "latest request") || !strings.Contains(encoded, "Deterministic memory capsule") {
		t.Fatalf("latest turn or capsule missing: %s", encoded)
	}
	for _, message := range messages {
		if message.ToolCallID == "one" {
			t.Fatal("orphaned tool result remained after dropping its turn")
		}
	}
}

func TestCapsuleDeduplicatesTurn(t *testing.T) {
	capsule := NewCapsule()
	turn := []provider.Message{{Role: "user", Content: "same request"}}
	capsule.ObserveTurn(turn)
	capsule.ObserveTurn(turn)
	if len(capsule.Requests) != 1 {
		t.Fatalf("requests = %#v", capsule.Requests)
	}
}

func messageText(messages []provider.Message) string {
	var output strings.Builder
	for _, message := range messages {
		output.WriteString(message.Content)
		output.WriteByte('\n')
	}
	return output.String()
}
