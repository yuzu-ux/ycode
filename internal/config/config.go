// Package config loads YCode settings without ever persisting an API key.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const projectConfigPath = ".ycode/config.json"

type Provider struct {
	Connection     string `json:"connection"`
	CLI            string `json:"cli,omitempty"`
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
			Connection:     "api",
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
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var fields struct {
		Provider *struct {
			Connection *string `json:"connection"`
			BaseURL    *string `json:"base_url"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
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
	if fields.Provider != nil && fields.Provider.Connection == nil && fields.Provider.BaseURL != nil {
		if parsed, err := url.Parse(strings.TrimSpace(*fields.Provider.BaseURL)); err == nil && loopbackHost(parsed.Hostname()) {
			target.Provider.Connection = "local"
		} else {
			target.Provider.Connection = "api"
		}
	}
	return nil
}

func applyEnvironment(cfg *Config) {
	stringOverride("YCODE_CONNECTION", &cfg.Provider.Connection)
	stringOverride("YCODE_CLI", &cfg.Provider.CLI)
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
	switch cfg.Provider.Connection {
	case "api":
		if err := validateProviderURL(cfg.Provider.BaseURL); err != nil {
			return err
		}
	case "local":
		if err := validateProviderURL(cfg.Provider.BaseURL); err != nil {
			return err
		}
		parsed, _ := url.Parse(cfg.Provider.BaseURL)
		if !loopbackHost(parsed.Hostname()) {
			return errors.New("provider.base_url must use loopback for a local connection")
		}
	case "cli":
		switch normalizeCLIName(cfg.Provider.CLI) {
		case "codex", "claude", "opencode":
		default:
			return errors.New("provider.cli must be codex, claude, or opencode for a cli connection")
		}
	default:
		return errors.New("provider.connection must be api, local, or cli")
	}
	if cfg.Provider.Connection != "cli" && strings.TrimSpace(cfg.Provider.Model) == "" {
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
	if cfg.Provider.Connection == "local" || cfg.Provider.Connection == "cli" {
		return ""
	}
	if key := strings.TrimSpace(os.Getenv("YCODE_API_KEY")); key != "" {
		return key
	}
	return strings.TrimSpace(os.Getenv(cfg.Provider.APIKeyEnv))
}

func loopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// WriteGlobalConnection saves non-secret connection metadata while preserving
// existing global agent settings. API key values are never accepted or written.
func WriteGlobalConnection(connection, baseURL, model, apiKeyEnv string) (string, error) {
	path, err := globalPath()
	if err != nil {
		return "", err
	}

	cfg := Default()
	if err := decodeIfPresent(path, &cfg); err != nil {
		return "", fmt.Errorf("global config: %w", err)
	}
	cfg.Provider.Connection = strings.TrimSpace(connection)
	cfg.Provider.CLI = ""
	cfg.Provider.BaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	cfg.Provider.Model = strings.TrimSpace(model)
	if strings.TrimSpace(apiKeyEnv) != "" {
		cfg.Provider.APIKeyEnv = strings.TrimSpace(apiKeyEnv)
	}
	if err := cfg.Validate(); err != nil {
		return "", err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := writePrivateAtomic(path, data); err != nil {
		return "", err
	}
	return path, nil
}

// WriteGlobalCLI selects an installed coding CLI without storing any of that
// CLI's login or credential material.
func WriteGlobalCLI(name string) (string, error) {
	path, err := globalPath()
	if err != nil {
		return "", err
	}

	cfg := Default()
	if err := decodeIfPresent(path, &cfg); err != nil {
		return "", fmt.Errorf("global config: %w", err)
	}
	cfg.Provider.Connection = "cli"
	cfg.Provider.CLI = normalizeCLIName(name)
	if err := cfg.Validate(); err != nil {
		return "", err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := writePrivateAtomic(path, data); err != nil {
		return "", err
	}
	return path, nil
}

func validateProviderURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("provider.base_url must be an http(s) URL")
	}
	return nil
}

func normalizeCLIName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	switch value {
	case "claude-code":
		return "claude"
	case "open-code":
		return "opencode"
	default:
		return value
	}
}

func writePrivateAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".ycode-config-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)

	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil && runtime.GOOS == "windows" {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		return os.Rename(tempPath, path)
	} else {
		return err
	}
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
