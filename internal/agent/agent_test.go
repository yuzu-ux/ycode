package agent

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/yuzu-ux/ycode/internal/provider"
	"github.com/yuzu-ux/ycode/internal/session"
)

type scriptedProvider struct {
	requests []provider.Request
}

func (s *scriptedProvider) Complete(_ context.Context, request provider.Request) (provider.Turn, error) {
	s.requests = append(s.requests, request)
	if len(s.requests) == 1 {
		return provider.Turn{ToolCalls: []provider.ToolCall{{
			ID:   "call-1",
			Type: "function",
			Function: provider.FunctionCall{
				Name:      "workspace",
				Arguments: `{"action":"read","path":"README.md"}`,
			},
		}}}, nil
	}
	return provider.Turn{Content: "finished"}, nil
}

type fakeTools struct {
	calls int
}

type fakeStatus struct {
	starts []string
	stops  int
}

func (status *fakeStatus) Start(label string) {
	status.starts = append(status.starts, label)
}

func (status *fakeStatus) Stop() {
	status.stops++
}

func (f *fakeTools) Specs() []provider.ToolSpec {
	return []provider.ToolSpec{{Type: "function", Function: provider.FunctionSpec{Name: "workspace"}}}
}

func (f *fakeTools) Execute(_ context.Context, _ provider.ToolCall) (string, error) {
	f.calls++
	return "file contents", nil
}

func TestAgentRunsToolLoop(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/README.md", []byte("# Test"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := &scriptedProvider{}
	toolset := &fakeTools{}
	status := &fakeStatus{}
	var output bytes.Buffer
	runner, err := New(Options{
		Root:             root,
		Model:            "test",
		InputBudget:      2_000,
		OutputTokens:     100,
		RepoMapTokens:    200,
		ToolOutputTokens: 200,
		MaxTurns:         3,
		Provider:         script,
		Tools:            toolset,
		State:            &session.State{ID: "test-session", Root: root},
		Stdout:           &output,
		Status:           status,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), "inspect the readme"); err != nil {
		t.Fatal(err)
	}
	if len(script.requests) != 2 || toolset.calls != 1 {
		t.Fatalf("provider requests=%d tool calls=%d", len(script.requests), toolset.calls)
	}
	second := script.requests[1].Messages
	foundToolResult := false
	for _, message := range second {
		if message.Role == "tool" && message.ToolCallID == "call-1" {
			foundToolResult = true
		}
	}
	if !foundToolResult {
		t.Fatalf("second request omitted tool result: %+v", second)
	}
	if output.String() != "finished\n" {
		t.Fatalf("output = %q", output.String())
	}
	if len(status.starts) != 3 || status.starts[0] != "Mapping workspace" || status.stops != 3 {
		t.Fatalf("status starts=%v stops=%d", status.starts, status.stops)
	}
}
