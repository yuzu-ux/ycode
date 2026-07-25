package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompleteJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}
		var body wireRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "test-model" || body.Stream {
			t.Errorf("unexpected request: %+v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"choices":[{"message":{"content":"done"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key", time.Second)
	turn, err := client.Complete(context.Background(), Request{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Content != "done" || turn.Usage.TotalTokens != 12 {
		t.Fatalf("unexpected turn: %+v", turn)
	}
}

func TestCompleteStreamingToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"Checking \"}}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"work\",\"arguments\":\"{\\\"act\"}}]}}]}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"space\",\"arguments\":\"ion\\\":\\\"list\\\"}\"}}]}}]}\n\n")
		_, _ = fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	var streamed strings.Builder
	client := NewClient(server.URL, "", time.Second)
	turn, err := client.Complete(context.Background(), Request{
		Model:       "local",
		Messages:    []Message{{Role: "user", Content: "inspect"}},
		Stream:      true,
		OnTextDelta: func(value string) { streamed.WriteString(value) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "Checking " || turn.Content != "Checking " {
		t.Fatalf("streamed=%q content=%q", streamed.String(), turn.Content)
	}
	if len(turn.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", turn.ToolCalls)
	}
	call := turn.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "workspace" || call.Function.Arguments != `{"action":"list"}` {
		t.Fatalf("unexpected tool call: %+v", call)
	}
}

func TestCompleteSurfacesHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "bad model", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", time.Second)
	_, err := client.Complete(context.Background(), Request{Model: "bad"})
	if err == nil || !strings.Contains(err.Error(), "bad model") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCompleteDoesNotFollowRedirect(t *testing.T) {
	var targetHit atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetHit.Store(true)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	client := NewClient(redirect.URL, "must-not-be-forwarded", time.Second)
	_, err := client.Complete(context.Background(), Request{Model: "test"})
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("unexpected error: %v", err)
	}
	if targetHit.Load() {
		t.Fatal("provider followed redirect")
	}
}

func TestDecodeUnknownJSON(t *testing.T) {
	turn, err := decodeUnknown(strings.NewReader("\n {\"choices\":[{\"message\":{\"content\":\"json fallback\"}}]}"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Content != "json fallback" {
		t.Fatalf("content = %q", turn.Content)
	}
}
