package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceRejectsPathEscape(t *testing.T) {
	workspace, err := NewWorkspace(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Execute(`{"action":"read","path":"../outside"}`); err == nil {
		t.Fatal("expected path escape to fail")
	}
}

func TestWorkspaceRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	workspace, err := NewWorkspace(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Execute(`{"action":"read","path":"link/secret.txt"}`); err == nil {
		t.Fatal("expected escaping symlink to fail")
	}
}

func TestWorkspaceWriteReadReplace(t *testing.T) {
	workspace, err := NewWorkspace(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Execute(`{"action":"write","path":"src/main.txt","content":"hello\nworld\n"}`); err != nil {
		t.Fatal(err)
	}
	read, err := workspace.Execute(`{"action":"read","path":"src/main.txt","start_line":2,"end_line":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read, "world") {
		t.Fatalf("read = %q", read)
	}
	if _, err := workspace.Execute(`{"action":"replace","path":"src/main.txt","old":"world","new":"YCode"}`); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(workspace.Root(), "src", "main.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello\nYCode\n" {
		t.Fatalf("content = %q", data)
	}
}

func TestReadOnlyWorkspaceRejectsEdit(t *testing.T) {
	workspace, err := NewWorkspace(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Execute(`{"action":"write","path":"x","content":"x"}`); err == nil {
		t.Fatal("expected read-only write to fail")
	}
}

func TestWorkspaceHidesSecretLikeFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := NewWorkspace(root, false)
	if err != nil {
		t.Fatal(err)
	}
	list, err := workspace.Execute(`{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(list, ".env") {
		t.Fatalf("secret-like path appeared in list: %q", list)
	}
	if _, err := workspace.Execute(`{"action":"read","path":".env"}`); err == nil {
		t.Fatal("secret-like read should be blocked")
	}
}

func TestShellPolicy(t *testing.T) {
	root := t.TempDir()
	shell := &Shell{Root: root, Policy: "safe"}
	if _, err := shell.Execute(context.Background(), `{"command":"pwd"}`); err != nil {
		t.Fatalf("safe command failed: %v", err)
	}
	if _, err := shell.Execute(context.Background(), `{"command":"echo mutation"}`); err == nil {
		t.Fatal("command outside safe set should fail")
	}
	if _, err := shell.Execute(context.Background(), `{"command":"rm -rf /"}`); err == nil {
		t.Fatal("destructive command should always fail")
	}
	if _, err := shell.Execute(context.Background(), `{"command":"rm -rf $HOME/*"}`); err == nil {
		t.Fatal("recursive home deletion should always fail")
	}

	approved := false
	shell.Policy = "ask"
	shell.Approver = func(command, reason string) bool {
		approved = command == "echo hello" && reason == "test"
		return approved
	}
	output, err := shell.Execute(context.Background(), `{"command":"echo hello","reason":"test"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !approved || strings.TrimSpace(output) != "hello" {
		t.Fatalf("approval=%v output=%q", approved, output)
	}
}

func TestToolArgumentsRejectTrailingJSON(t *testing.T) {
	workspace, err := NewWorkspace(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Execute(`{"action":"list"} {"action":"write","path":"x","content":"x"}`); err == nil {
		t.Fatal("expected trailing JSON to fail")
	}
}

func TestSanitizedEnvironmentRemovesCredentials(t *testing.T) {
	input := []string{
		"PATH=/bin",
		"OPENAI_API_KEY=secret",
		"GITHUB_TOKEN=secret",
		"DATABASE_PASSWORD=secret",
		"PROJECT_NAME=ycode",
	}
	output := strings.Join(sanitizedEnvironment(input), "\n")
	if strings.Contains(output, "secret") {
		t.Fatalf("secret-bearing environment survived: %s", output)
	}
	if !strings.Contains(output, "PATH=/bin") || !strings.Contains(output, "PROJECT_NAME=ycode") {
		t.Fatalf("safe environment was removed: %s", output)
	}
}
