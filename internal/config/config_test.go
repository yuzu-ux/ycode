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
	t.Setenv("YCODE_MODEL", "env-model")
	t.Setenv("YCODE_INPUT_BUDGET", "9000")
	t.Setenv("YCODE_STREAM", "false")
	cfg := Default()
	applyEnvironment(&cfg)

	if cfg.Provider.Model != "env-model" || cfg.Agent.InputBudgetTokens != 9000 || cfg.Provider.Stream {
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
