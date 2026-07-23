package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestRunEndToEndAgainstCompatibleServer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YCODE_CONFIG_DIR", t.TempDir())
	t.Setenv("YCODE_CACHE_DIR", t.TempDir())

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			_, _ = fmt.Fprint(writer, `{"choices":[{"message":{"tool_calls":[{"id":"read-1","type":"function","function":{"name":"workspace","arguments":"{\"action\":\"read\",\"path\":\"README.md\"}"}}]}}]}`)
			return
		}
		_, _ = fmt.Fprint(writer, `{"choices":[{"message":{"content":"README inspected"}}],"usage":{"prompt_tokens":50,"completion_tokens":3,"total_tokens":53}}`)
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	exit := Run([]string{
		"run",
		"--root", root,
		"--base-url", server.URL,
		"--model", "test-model",
		"--stream=false",
		"--shell-policy", "safe",
		"inspect the readme",
	}, bytes.NewReader(nil), &stdout, &stderr, "test")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if requests.Load() != 2 {
		t.Fatalf("provider requests = %d", requests.Load())
	}
	if stdout.String() != "README inspected\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !bytes.Contains(stderr.Bytes(), []byte("workspace.read README.md")) {
		t.Fatalf("tool progress missing: %s", stderr.String())
	}
}

func TestHelpDoesNotRequireConfiguration(t *testing.T) {
	var output bytes.Buffer
	if exit := Run([]string{"help"}, bytes.NewReader(nil), &output, &output, "test"); exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	if !bytes.Contains(output.Bytes(), []byte("token-budgeted")) {
		t.Fatalf("unexpected help: %s", output.String())
	}
}

func TestCredentialTransportSafety(t *testing.T) {
	if secureForCredential("http://example.com/v1") {
		t.Fatal("remote plain HTTP must not be credential-safe")
	}
	if !secureForCredential("https://example.com/v1") {
		t.Fatal("HTTPS should be credential-safe")
	}
	if !secureForCredential("http://127.0.0.1:11434/v1") {
		t.Fatal("loopback HTTP should be credential-safe")
	}
}
