package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecodePreservesNestedDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"provider":{"model":"test-model"},"agent":{"max_turns":3}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	if err := decodeIfPresent(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Model != "test-model" {
		t.Fatalf("model = %q", cfg.Provider.Model)
	}
	if cfg.Provider.BaseURL == "" {
		t.Fatal("unspecified nested default was lost")
	}
	if cfg.Agent.MaxTurns != 3 || cfg.Agent.InputBudgetTokens == 0 {
		t.Fatalf("unexpected agent config: %+v", cfg.Agent)
	}
}

func TestEnvironmentOverrides(t *testing.T) {
	t.Setenv("YCODE_CONNECTION", "local")
	t.Setenv("YCODE_BASE_URL", "http://127.0.0.1:11434/v1")
	t.Setenv("YCODE_MODEL", "env-model")
	t.Setenv("YCODE_INPUT_BUDGET", "9000")
	t.Setenv("YCODE_STREAM", "false")
	cfg := Default()
	applyEnvironment(&cfg)

	if cfg.Provider.Connection != "local" || cfg.Provider.Model != "env-model" || cfg.Agent.InputBudgetTokens != 9000 || cfg.Provider.Stream {
		t.Fatalf("environment overrides not applied: %+v", cfg)
	}
}

func TestWriteProjectTemplateDoesNotContainSecret(t *testing.T) {
	t.Setenv("YCODE_API_KEY", "top-secret-value")
	path, err := WriteProjectTemplate(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || contains(string(data), "top-secret-value") {
		t.Fatal("template was empty or leaked an API key")
	}
}

func contains(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}

func TestValidateRejectsInvalidPolicy(t *testing.T) {
	cfg := Default()
	cfg.Agent.ShellPolicy = "anything"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid policy to fail")
	}
}

func TestLocalConnectionNeverResolvesAPIKey(t *testing.T) {
	t.Setenv("YCODE_API_KEY", "must-not-be-used")
	t.Setenv("OPENAI_API_KEY", "also-must-not-be-used")
	cfg := Default()
	cfg.Provider.Connection = "local"
	cfg.Provider.BaseURL = "http://127.0.0.1:11434/v1"
	if key := cfg.APIKey(); key != "" {
		t.Fatalf("local API key = %q", key)
	}
}

func TestLocalConnectionRejectsNonLoopbackURL(t *testing.T) {
	cfg := Default()
	cfg.Provider.Connection = "local"
	cfg.Provider.BaseURL = "https://example.com/v1"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-loopback local URL to fail")
	}
}

func TestCLIConnectionNeverResolvesOrStoresAPIKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YCODE_CONFIG_DIR", dir)
	t.Setenv("YCODE_API_KEY", "must-not-be-used-or-written")
	t.Setenv("OPENAI_API_KEY", "also-must-not-be-used")

	path, err := WriteGlobalCLI("claude-code")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Connection != "cli" || cfg.Provider.CLI != "claude" {
		t.Fatalf("unexpected CLI config: %+v", cfg.Provider)
	}
	if key := cfg.APIKey(); key != "" {
		t.Fatalf("CLI API key = %q", key)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(data), "must-not-be-used-or-written") {
		t.Fatal("CLI config leaked an API key")
	}
}

func TestCLIConnectionRequiresSupportedName(t *testing.T) {
	cfg := Default()
	cfg.Provider.Connection = "cli"
	cfg.Provider.CLI = "anything"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported CLI to fail")
	}
}

func TestWriteGlobalConnectionPreservesAgentSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YCODE_CONFIG_DIR", dir)
	t.Setenv("YCODE_API_KEY", "must-not-be-written")
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"agent":{"max_turns":7}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	written, err := WriteGlobalConnection("local", "http://127.0.0.1:11434/v1", "coder", "")
	if err != nil {
		t.Fatal(err)
	}
	if written != path {
		t.Fatalf("path = %q", written)
	}
	cfg, _, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Connection != "local" || cfg.Provider.Model != "coder" || cfg.Agent.MaxTurns != 7 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if contains(string(data), "must-not-be-written") {
		t.Fatal("global connection config leaked API key")
	}
}

func TestLegacyBaseURLInfersConnectionMode(t *testing.T) {
	t.Run("loopback", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		if err := os.WriteFile(path, []byte(`{"provider":{"base_url":"http://127.0.0.1:11434/v1"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := Default()
		if err := decodeIfPresent(path, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg.Provider.Connection != "local" {
			t.Fatalf("connection = %q", cfg.Provider.Connection)
		}
	})

	t.Run("project remote override", func(t *testing.T) {
		t.Setenv("YCODE_CONFIG_DIR", t.TempDir())
		if _, err := WriteGlobalConnection("local", "http://127.0.0.1:11434/v1", "local-model", ""); err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		projectDir := filepath.Join(root, ".ycode")
		if err := os.MkdirAll(projectDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(projectDir, "config.json"),
			[]byte(`{"provider":{"base_url":"https://example.com/v1","model":"remote-model"}}`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		cfg, _, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Provider.Connection != "api" || cfg.Provider.Model != "remote-model" {
			t.Fatalf("unexpected provider: %+v", cfg.Provider)
		}
	})
}
