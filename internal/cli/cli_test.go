package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/yuzu-ux/ycode/internal/config"
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

func TestConnectLocalPersistsAndRunsWithoutAPIKey(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YCODE_CONFIG_DIR", t.TempDir())
	t.Setenv("YCODE_CACHE_DIR", t.TempDir())
	t.Setenv("YCODE_API_KEY", "must-not-be-sent")
	t.Setenv("OPENAI_API_KEY", "also-must-not-be-sent")

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if authorization := request.Header.Get("Authorization"); authorization != "" {
			t.Errorf("local request sent authorization header %q", authorization)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/models":
			_, _ = fmt.Fprint(writer, `{"data":[{"id":"text-embedding"},{"id":"general-model"},{"id":"fast-coder"}]}`)
		case "/v1/chat/completions":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["model"] != "fast-coder" {
				t.Errorf("model = %v", body["model"])
			}
			_, _ = fmt.Fprint(writer, `{"choices":[{"message":{"content":"local response"}}]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	var connectOut, connectErr bytes.Buffer
	exit := Run([]string{
		"connect", "local",
		"--base-url", server.URL,
		"--timeout", "1s",
	}, bytes.NewReader(nil), &connectOut, &connectErr, "test")
	if exit != 0 {
		t.Fatalf("connect exit=%d stderr=%s", exit, connectErr.String())
	}
	if !bytes.Contains(connectOut.Bytes(), []byte("fast-coder")) ||
		!bytes.Contains(connectOut.Bytes(), []byte("API key           disabled")) {
		t.Fatalf("connect output = %s", connectOut.String())
	}

	cfg, _, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Connection != "local" ||
		cfg.Provider.BaseURL != server.URL+"/v1" ||
		cfg.Provider.Model != "fast-coder" ||
		cfg.APIKey() != "" {
		t.Fatalf("unexpected local config: %+v", cfg.Provider)
	}

	var stdout, stderr bytes.Buffer
	exit = Run([]string{
		"run",
		"--root", root,
		"--stream=false",
		"answer locally",
	}, bytes.NewReader(nil), &stdout, &stderr, "test")
	if exit != 0 {
		t.Fatalf("run exit=%d stderr=%s", exit, stderr.String())
	}
	if stdout.String() != "local response\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestConnectLocalRejectsRemoteEndpoint(t *testing.T) {
	t.Setenv("YCODE_CONFIG_DIR", t.TempDir())
	var output bytes.Buffer
	exit := Run([]string{
		"connect", "local",
		"--base-url", "https://example.com/v1",
	}, bytes.NewReader(nil), &output, &output, "test")
	if exit == 0 {
		t.Fatal("expected remote endpoint to be rejected")
	}
	if !bytes.Contains(output.Bytes(), []byte("must use loopback")) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestConnectAPIPersistsOnlyKeyName(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("YCODE_CONFIG_DIR", configDir)
	t.Setenv("YCODE_API_KEY", "must-not-be-written")

	var output bytes.Buffer
	exit := Run([]string{
		"connect", "api",
		"--base-url", "https://provider.example/v1",
		"--model", "hosted-model",
		"--api-key-env", "PROVIDER_API_KEY",
	}, bytes.NewReader(nil), &output, &output, "test")
	if exit != 0 {
		t.Fatalf("exit=%d output=%s", exit, output.String())
	}
	cfg, _, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Connection != "api" ||
		cfg.Provider.BaseURL != "https://provider.example/v1" ||
		cfg.Provider.Model != "hosted-model" ||
		cfg.Provider.APIKeyEnv != "PROVIDER_API_KEY" {
		t.Fatalf("unexpected API config: %+v", cfg.Provider)
	}
	data, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("must-not-be-written")) {
		t.Fatal("saved API connection leaked key value")
	}
}

func TestSelectLocalModelRequiresChoiceWhenAmbiguous(t *testing.T) {
	_, err := selectLocalModel([]string{"general-a", "general-b"}, "")
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("--model")) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInteractiveLocalConnectionPromptsInsteadOfGuessing(t *testing.T) {
	t.Setenv("YCODE_CONFIG_DIR", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"data":[{"id":"general-a"},{"id":"general-b"}]}`)
	}))
	defer server.Close()

	var output bytes.Buffer
	exit := runConnectLocalMode(
		[]string{"--base-url", server.URL, "--timeout", "1s"},
		streams{in: bytes.NewBufferString("2\n"), out: &output, err: &output},
		true,
	)
	if exit != 0 {
		t.Fatalf("exit=%d output=%s", exit, output.String())
	}
	cfg, _, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Model != "general-b" {
		t.Fatalf("selected model = %q", cfg.Provider.Model)
	}
	if !bytes.Contains(output.Bytes(), []byte("nothing will be loaded")) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestMissingAPIKeyPointsToGuidedSetup(t *testing.T) {
	t.Setenv("YCODE_CONFIG_DIR", t.TempDir())
	t.Setenv("YCODE_CACHE_DIR", t.TempDir())
	t.Setenv("YCODE_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	var output bytes.Buffer
	exit := Run(
		[]string{"run", "--root", t.TempDir(), "inspect"},
		bytes.NewReader(nil),
		&output,
		&output,
		"test",
	)
	if exit == 0 {
		t.Fatal("expected missing connection to fail")
	}
	if !bytes.Contains(output.Bytes(), []byte("ycode setup")) ||
		!bytes.Contains(output.Bytes(), []byte("Codex")) ||
		!bytes.Contains(output.Bytes(), []byte("local model")) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestSetupAcceptsNamedHostedAPIChoice(t *testing.T) {
	t.Setenv("YCODE_CONFIG_DIR", t.TempDir())
	t.Setenv("YCODE_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	var output bytes.Buffer
	exit := Run([]string{"setup"}, bytes.NewBufferString("api\n"), &output, &output, "test")
	if exit != 0 {
		t.Fatalf("exit=%d output=%s", exit, output.String())
	}
	cfg, _, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Connection != "api" {
		t.Fatalf("connection = %q", cfg.Provider.Connection)
	}
	if !bytes.Contains(output.Bytes(), []byte("No model is started during discovery")) {
		t.Fatalf("output = %s", output.String())
	}
}

func TestExternalCLIConnectionAndDelegation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	binDir := t.TempDir()
	fakeCodex := filepath.Join(binDir, "codex")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\"\n"
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	configDir := t.TempDir()
	t.Setenv("YCODE_CONFIG_DIR", configDir)
	t.Setenv("YCODE_CACHE_DIR", t.TempDir())
	t.Setenv("YCODE_API_KEY", "must-not-be-written")

	var connectOutput bytes.Buffer
	exit := Run(
		[]string{"connect", "cli", "codex"},
		bytes.NewReader(nil),
		&connectOutput,
		&connectOutput,
		"test",
	)
	if exit != 0 {
		t.Fatalf("connect exit=%d output=%s", exit, connectOutput.String())
	}
	if !bytes.Contains(connectOutput.Bytes(), []byte("existing")) &&
		!bytes.Contains(connectOutput.Bytes(), []byte("none stored")) {
		t.Fatalf("connect output = %s", connectOutput.String())
	}

	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	exit = Run(
		[]string{"run", "--root", root, "--read-only", "inspect; do not execute this as shell"},
		bytes.NewReader(nil),
		&stdout,
		&stderr,
		"test",
	)
	if exit != 0 {
		t.Fatalf("run exit=%d stderr=%s", exit, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("read-only")) ||
		!bytes.Contains(stdout.Bytes(), []byte("inspect; do not execute this as shell")) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	data, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("must-not-be-written")) {
		t.Fatal("external CLI connection leaked a key")
	}
}
