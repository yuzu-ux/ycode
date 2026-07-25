package externalcli

import (
	"slices"
	"strings"
	"testing"
)

func TestArgumentsUseSafeNonInteractiveModes(t *testing.T) {
	root := "/workspace"
	prompt := "fix the parser"

	codex, err := Arguments("codex", root, prompt, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(codex, []string{
		"exec",
		"--ephemeral",
		"--sandbox", "workspace-write",
		"--cd", root,
		prompt,
	}) {
		t.Fatalf("codex args = %#v", codex)
	}

	claude, err := Arguments("claude-code", root, prompt, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(claude, "plan") || !slices.Contains(claude, "--no-session-persistence") {
		t.Fatalf("claude args = %#v", claude)
	}

	opencode, err := Arguments("open-code", root, prompt, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(opencode, []string{"run", "--auto", prompt}) {
		t.Fatalf("opencode args = %#v", opencode)
	}
}

func TestSanitizedEnvironmentRemovesCredentials(t *testing.T) {
	input := []string{
		"PATH=/bin",
		"OPENAI_API_KEY=secret",
		"YCODE_API_KEY=secret",
		"CLAUDE_CODE_OAUTH_TOKEN=secret",
		"TERM=xterm-256color",
	}
	output := strings.Join(sanitizedEnvironment(input), "\n")
	if strings.Contains(output, "secret") {
		t.Fatalf("credential-bearing environment survived: %s", output)
	}
	if !strings.Contains(output, "PATH=/bin") || !strings.Contains(output, "TERM=xterm-256color") {
		t.Fatalf("safe environment was removed: %s", output)
	}
}

func TestOpenCodeReadOnlyEnvironmentDeniesMutation(t *testing.T) {
	output := strings.Join(commandEnvironment("opencode", true, []string{
		"PATH=/bin",
		`OPENCODE_PERMISSION={"edit":"allow"}`,
	}), "\n")
	for _, expected := range []string{`"edit":"deny"`, `"bash":"deny"`, `"external_directory":"deny"`} {
		if !strings.Contains(output, expected) {
			t.Fatalf("read-only environment missing %s: %s", expected, output)
		}
	}
	if strings.Contains(output, `"edit":"allow"`) {
		t.Fatalf("read-only environment retained writable override: %s", output)
	}
	if writable := strings.Join(commandEnvironment("opencode", false, []string{"PATH=/bin"}), "\n"); strings.Contains(writable, "OPENCODE_PERMISSION") {
		t.Fatalf("normal environment unexpectedly overrides permissions: %s", writable)
	}
}

func TestReadOnlyMappings(t *testing.T) {
	codex, err := Arguments("codex", ".", "inspect", true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(codex, "read-only") {
		t.Fatalf("codex args = %#v", codex)
	}

	claude, err := Arguments("claude", ".", "inspect", false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(claude, "acceptEdits") {
		t.Fatalf("claude args = %#v", claude)
	}

	opencode, err := Arguments("opencode", ".", "inspect", true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(opencode, []string{"run", "--agent", "plan", "inspect"}) {
		t.Fatalf("opencode args = %#v", opencode)
	}
}
