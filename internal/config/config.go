// Package config loads YCode settings without ever persisting an API key.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const projectConfigPath = ".ycode/config.json"

type Provider struct {
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	APIKeyEnv      string `json:"api_key_env"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Stream         bool   `json:"stream"`
}

type Agent struct {
	InputBudgetTokens int    `json:"input_budget_tokens"`
	OutputTokens      int    `json:"output_tokens"`
	RepoMapTokens     int    `json:"repo_map_tokens"`
	ToolOutputTokens  int    `json:"tool_output_tokens"`
	MaxTurns          int    `json:"max_turns"`
	ShellPolicy       string `json:"shell_policy"`
}

type Config struct {
	Provider Provider `json:"provider"`
	Agent    Agent    `json:"agent"`
}

type Sources struct {
	Global  string
	Project string
}

func Default() Config {
	return Config{
		Provider: Provider{
			BaseURL:        "https://api.openai.com/v1",
			Model:          "gpt-4.1-mini",
			APIKeyEnv:      "OPENAI_API_KEY",
			TimeoutSeconds: 180,
			Stream:         true,
		},
		Agent: Agent{
			InputBudgetTokens: 16_000,
			OutputTokens:      4_096,
			RepoMapTokens:     1_200,
			ToolOutputTokens:  1_800,
			MaxTurns:          12,
			ShellPolicy:       "ask",
		},
	}
}

// Load applies defaults, global config, project config, then environment
// overrides. Missing config files are normal.
func Load(root string) (Config, Sources, error) {
	cfg := Default()
	var sources Sources

	if global, err := globalPath(); err == nil {
		sources.Global = global
		if err := decodeIfPresent(global, &cfg); err != nil {
			return Config{}, sources, fmt.Errorf("global config: %w", err)
		}
	}

	if root != "" {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return Config{}, sources, fmt.Errorf("resolve workspace: %w", err)
		}
		sources.Project = filepath.Join(absolute, filepath.FromSlash(projectConfigPath))
		if err := decodeIfPresent(sources.Project, &cfg); err != nil {
			return Config{}, sources, fmt.Errorf("project config: %w", err)
		}
	}

	applyEnvironment(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, sources, err
	}
	return cfg, sources, nil
}

func globalPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("YCODE_CONFIG_DIR")); override != "" {
		absolute, err := filepath.Abs(override)
		if err != nil {
			return "", err
		}
		return filepath.Join(absolute, "config.json"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ycode", "config.json"), nil
}

func decodeIfPresent(path string, target *Config) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func applyEnvironment(cfg *Config) {
	stringOverride("YCODE_BASE_URL", &cfg.Provider.BaseURL)
	stringOverride("YCODE_MODEL", &cfg.Provider.Model)
	stringOverride("YCODE_API_KEY_ENV", &cfg.Provider.APIKeyEnv)
	stringOverride("YCODE_SHELL_POLICY", &cfg.Agent.ShellPolicy)
	intOverride("YCODE_INPUT_BUDGET", &cfg.Agent.InputBudgetTokens)
	intOverride("YCODE_OUTPUT_TOKENS", &cfg.Agent.OutputTokens)
	intOverride("YCODE_REPO_MAP_TOKENS", &cfg.Agent.RepoMapTokens)
	intOverride("YCODE_TOOL_OUTPUT_TOKENS", &cfg.Agent.ToolOutputTokens)
	intOverride("YCODE_MAX_TURNS", &cfg.Agent.MaxTurns)
	intOverride("YCODE_TIMEOUT_SECONDS", &cfg.Provider.TimeoutSeconds)
	if value, ok := os.LookupEnv("YCODE_STREAM"); ok {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.Provider.Stream = parsed
		}
	}
}

func stringOverride(name string, target *string) {
	if value, ok := os.LookupEnv(name); ok && strings.TrimSpace(value) != "" {
		*target = strings.TrimSpace(value)
	}
}

func intOverride(name string, target *int) {
	if value, ok := os.LookupEnv(name); ok {
		if parsed, err := strconv.Atoi(value); err == nil {
			*target = parsed
		}
	}
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (cfg Config) Validate() error {
	parsed, err := url.Parse(cfg.Provider.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("provider.base_url must be an http(s) URL")
	}
	if strings.TrimSpace(cfg.Provider.Model) == "" {
		return errors.New("provider.model cannot be empty")
	}
	if !environmentName.MatchString(cfg.Provider.APIKeyEnv) {
		return errors.New("provider.api_key_env is not a valid environment variable name")
	}
	if cfg.Provider.TimeoutSeconds < 1 || cfg.Provider.TimeoutSeconds > 3_600 {
		return errors.New("provider.timeout_seconds must be between 1 and 3600")
	}
	if cfg.Agent.InputBudgetTokens < 1_000 {
		return errors.New("agent.input_budget_tokens must be at least 1000")
	}
	if cfg.Agent.OutputTokens < 1 {
		return errors.New("agent.output_tokens must be positive")
	}
	if cfg.Agent.RepoMapTokens < 100 {
		return errors.New("agent.repo_map_tokens must be at least 100")
	}
	if cfg.Agent.ToolOutputTokens < 100 {
		return errors.New("agent.tool_output_tokens must be at least 100")
	}
	if cfg.Agent.MaxTurns < 1 || cfg.Agent.MaxTurns > 100 {
		return errors.New("agent.max_turns must be between 1 and 100")
	}
	switch cfg.Agent.ShellPolicy {
	case "safe", "ask", "allow":
	default:
		return errors.New("agent.shell_policy must be safe, ask, or allow")
	}
	return nil
}

// APIKey resolves the configured key at call time. YCODE_API_KEY is a
// convenient universal override, while APIKeyEnv supports provider-native names.
func (cfg Config) APIKey() string {
	if key := strings.TrimSpace(os.Getenv("YCODE_API_KEY")); key != "" {
		return key
	}
	return strings.TrimSpace(os.Getenv(cfg.Provider.APIKeyEnv))
}

// WriteProjectTemplate creates a non-secret project configuration.
func WriteProjectTemplate(root string, force bool) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(absolute, filepath.FromSlash(projectConfigPath))
	if !force {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("%s already exists (use --force to replace it)", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(Default(), "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
