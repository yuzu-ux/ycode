package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/yuzu-ux/ycode/internal/config"
	"github.com/yuzu-ux/ycode/internal/externalcli"
	"github.com/yuzu-ux/ycode/internal/textsafe"
	"github.com/yuzu-ux/ycode/internal/ui"
)

type localEndpoint struct {
	Name    string
	BaseURL string
}

var knownLocalEndpoints = []localEndpoint{
	{Name: "Ollama", BaseURL: "http://127.0.0.1:11434/v1"},
	{Name: "LM Studio", BaseURL: "http://127.0.0.1:1234/v1"},
	{Name: "llama.cpp", BaseURL: "http://127.0.0.1:8080/v1"},
}

func runConnect(args []string, io streams) int {
	if len(args) == 0 {
		printConnectHelp(io.err)
		return 2
	}
	switch args[0] {
	case "local":
		return runConnectLocal(args[1:], io)
	case "cli", "external":
		return runConnectCLI(args[1:], io)
	case "api":
		return runConnectAPI(args[1:], io)
	case "status":
		return runConnectStatus(args[1:], io)
	case "help", "--help", "-h":
		printConnectHelp(io.out)
		return 0
	default:
		return fail(io.err, fmt.Errorf("unknown connect target %q; use cli, local, api, or status", args[0]))
	}
}

func runConnectLocal(args []string, io streams) int {
	return runConnectLocalMode(args, io, ui.InteractiveReader(io.in))
}

func runConnectLocalMode(args []string, io streams, forcePrompt bool) int {
	flags := flag.NewFlagSet("ycode connect local", flag.ContinueOnError)
	flags.SetOutput(io.err)
	runtimeName := flags.String("runtime", "auto", "auto, ollama, lm-studio, or llama.cpp")
	baseURL := flags.String("base-url", "", "custom loopback OpenAI-compatible base URL")
	model := flags.String("model", "", "local model ID (required when the choice is ambiguous)")
	timeout := flags.Duration("timeout", 2*time.Second, "discovery timeout per runtime")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) != 0 {
		return fail(io.err, errors.New("connect local does not accept positional arguments"))
	}
	if *timeout < 100*time.Millisecond || *timeout > 30*time.Second {
		return fail(io.err, errors.New("timeout must be between 100ms and 30s"))
	}

	endpoints, err := localConnectionCandidates(*runtimeName, *baseURL)
	if err != nil {
		return fail(io.err, err)
	}
	endpoint, models, err := discoverLocalEndpoint(endpoints, *timeout)
	if err != nil {
		return fail(io.err, err)
	}
	selected, err := selectLocalModel(models, *model)
	if err != nil && strings.TrimSpace(*model) == "" && forcePrompt {
		selected, err = promptLocalModel(models, io)
	}
	if err != nil {
		return fail(io.err, err)
	}
	path, err := config.WriteGlobalConnection("local", endpoint.BaseURL, selected, "")
	if err != nil {
		return fail(io.err, err)
	}

	_, _ = fmt.Fprintln(io.out, "✓ local runtime     "+textsafe.Terminal(endpoint.Name))
	_, _ = fmt.Fprintln(io.out, "✓ endpoint          "+textsafe.Terminal(endpoint.BaseURL))
	_, _ = fmt.Fprintln(io.out, "✓ model             "+textsafe.Terminal(selected))
	_, _ = fmt.Fprintln(io.out, "✓ saved             "+textsafe.Terminal(path))
	_, _ = fmt.Fprintln(io.out, "✓ API key           disabled for this local connection")
	_, _ = fmt.Fprintln(io.out, "\nRun `ycode` to start chatting with the local model.")
	return 0
}

func runConnectCLI(args []string, io streams) int {
	flags := flag.NewFlagSet("ycode connect cli", flag.ContinueOnError)
	flags.SetOutput(io.err)
	list := flags.Bool("list", false, "list supported external coding CLIs")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *list || len(flags.Args()) == 0 {
		printExternalCLIStatuses(io.out)
		if *list {
			return 0
		}
		_, _ = fmt.Fprintln(io.err, "\nChoose one: ycode connect cli codex|claude|opencode")
		return 2
	}
	if len(flags.Args()) != 1 {
		return fail(io.err, errors.New("connect cli accepts exactly one CLI name"))
	}

	status, err := externalcli.Resolve(flags.Args()[0])
	if err != nil {
		return fail(io.err, err)
	}
	path, err := config.WriteGlobalCLI(status.Name)
	if err != nil {
		return fail(io.err, err)
	}
	_, _ = fmt.Fprintln(io.out, "✓ connection        external coding CLI")
	_, _ = fmt.Fprintln(io.out, "✓ CLI               "+textsafe.Terminal(status.DisplayName))
	_, _ = fmt.Fprintln(io.out, "✓ executable        "+textsafe.Terminal(status.Path))
	_, _ = fmt.Fprintln(io.out, "✓ saved             "+textsafe.Terminal(path))
	_, _ = fmt.Fprintln(io.out, "✓ credentials       owned by the external CLI; none stored by YCode")
	_, _ = fmt.Fprintln(io.out, "\nRun `ycode` or `ycode \"your task\"` to delegate through it.")
	return 0
}

func printExternalCLIStatuses(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "External coding CLIs:")
	for _, status := range externalcli.Statuses() {
		detail := "not found on PATH"
		marker := "○"
		if status.Installed {
			detail = status.Path
			marker = "✓"
		}
		_, _ = fmt.Fprintf(
			writer,
			"  %s %-12s %s\n",
			marker,
			textsafe.Terminal(status.DisplayName),
			textsafe.Terminal(detail),
		)
	}
}

func runConnectAPI(args []string, io streams) int {
	defaults := config.Default()
	flags := flag.NewFlagSet("ycode connect api", flag.ContinueOnError)
	flags.SetOutput(io.err)
	baseURL := flags.String("base-url", defaults.Provider.BaseURL, "hosted OpenAI-compatible base URL")
	model := flags.String("model", defaults.Provider.Model, "provider model ID")
	apiKeyEnv := flags.String("api-key-env", defaults.Provider.APIKeyEnv, "environment variable containing the API key")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) != 0 {
		return fail(io.err, errors.New("connect api does not accept positional arguments"))
	}

	cfg := config.Default()
	cfg.Provider.Connection = "api"
	cfg.Provider.BaseURL = strings.TrimRight(strings.TrimSpace(*baseURL), "/")
	cfg.Provider.Model = strings.TrimSpace(*model)
	cfg.Provider.APIKeyEnv = strings.TrimSpace(*apiKeyEnv)
	if err := cfg.Validate(); err != nil {
		return fail(io.err, err)
	}
	if requiresKey(cfg.Provider.BaseURL) && !secureForCredential(cfg.Provider.BaseURL) {
		return fail(io.err, errors.New("hosted API connections must use HTTPS"))
	}
	path, err := config.WriteGlobalConnection(
		cfg.Provider.Connection,
		cfg.Provider.BaseURL,
		cfg.Provider.Model,
		cfg.Provider.APIKeyEnv,
	)
	if err != nil {
		return fail(io.err, err)
	}

	_, _ = fmt.Fprintln(io.out, "✓ connection        hosted API")
	_, _ = fmt.Fprintln(io.out, "✓ endpoint          "+textsafe.Terminal(cfg.Provider.BaseURL))
	_, _ = fmt.Fprintln(io.out, "✓ model             "+textsafe.Terminal(cfg.Provider.Model))
	_, _ = fmt.Fprintln(io.out, "✓ saved             "+textsafe.Terminal(path))
	_, _ = fmt.Fprintln(io.out, "✓ API key value     not stored")
	_, _ = fmt.Fprintln(io.out, "\nSet YCODE_API_KEY or "+textsafe.Terminal(cfg.Provider.APIKeyEnv)+", then run `ycode`.")
	return 0
}

func runConnectStatus(args []string, io streams) int {
	root := scanStringFlag(args, "root", ".")
	flags := flag.NewFlagSet("ycode connect status", flag.ContinueOnError)
	flags.SetOutput(io.err)
	flags.StringVar(&root, "root", root, "workspace root")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) != 0 {
		return fail(io.err, errors.New("connect status does not accept positional arguments"))
	}
	cfg, _, err := config.Load(root)
	if err != nil {
		return fail(io.err, err)
	}
	_, _ = fmt.Fprintln(io.out, "connection  "+textsafe.Terminal(cfg.Provider.Connection))
	if cfg.Provider.Connection == "cli" {
		_, _ = fmt.Fprintln(io.out, "CLI         "+textsafe.Terminal(externalcli.DisplayName(cfg.Provider.CLI)))
		if status, resolveErr := externalcli.Resolve(cfg.Provider.CLI); resolveErr != nil {
			_, _ = fmt.Fprintln(io.out, "executable  not found on PATH")
		} else {
			_, _ = fmt.Fprintln(io.out, "executable  "+textsafe.Terminal(status.Path))
		}
		_, _ = fmt.Fprintln(io.out, "API key     not used by YCode")
		return 0
	}
	_, _ = fmt.Fprintln(io.out, "endpoint    "+textsafe.Terminal(cfg.Provider.BaseURL))
	_, _ = fmt.Fprintln(io.out, "model       "+textsafe.Terminal(cfg.Provider.Model))
	if cfg.Provider.Connection == "local" {
		_, _ = fmt.Fprintln(io.out, "API key     disabled")
	} else if cfg.APIKey() == "" {
		_, _ = fmt.Fprintln(io.out, "API key     not found")
	} else {
		_, _ = fmt.Fprintln(io.out, "API key     found (value hidden)")
	}
	return 0
}

func promptLocalModel(models []string, io streams) (string, error) {
	var choices []string
	for _, model := range models {
		if localModelScore(model) < 100 {
			choices = append(choices, model)
		}
	}
	if len(choices) == 0 {
		return "", errors.New("no local chat models are available")
	}
	_, _ = fmt.Fprintln(io.err, "\nChoose a local model (nothing will be loaded until you send a prompt):")
	for index, model := range choices {
		_, _ = fmt.Fprintf(io.err, "  %d. %s\n", index+1, textsafe.Terminal(model))
	}
	_, _ = fmt.Fprint(io.err, "Model: ")
	line, err := bufio.NewReader(io.in).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", errors.New("model selection cancelled")
	}
	answer := strings.TrimSpace(line)
	for index, model := range choices {
		if answer == fmt.Sprint(index+1) || answer == model {
			return model, nil
		}
	}
	return "", fmt.Errorf("invalid model choice %q", answer)
}

func localConnectionCandidates(runtimeName, customBaseURL string) ([]localEndpoint, error) {
	runtimeName = strings.ToLower(strings.TrimSpace(runtimeName))
	runtimeName = strings.ReplaceAll(runtimeName, "_", "-")

	if strings.TrimSpace(customBaseURL) != "" {
		normalized, err := normalizeLocalBaseURL(customBaseURL)
		if err != nil {
			return nil, err
		}
		name := "custom"
		if runtimeName != "" && runtimeName != "auto" {
			name = runtimeDisplayName(runtimeName)
			if name == "" {
				return nil, fmt.Errorf("unknown local runtime %q", runtimeName)
			}
		}
		return []localEndpoint{{Name: name, BaseURL: normalized}}, nil
	}

	switch runtimeName {
	case "", "auto":
		return append([]localEndpoint(nil), knownLocalEndpoints...), nil
	case "ollama":
		return []localEndpoint{knownLocalEndpoints[0]}, nil
	case "lmstudio", "lm-studio":
		return []localEndpoint{knownLocalEndpoints[1]}, nil
	case "llamacpp", "llama-cpp", "llama.cpp":
		return []localEndpoint{knownLocalEndpoints[2]}, nil
	default:
		return nil, fmt.Errorf("unknown local runtime %q", runtimeName)
	}
}

func runtimeDisplayName(runtimeName string) string {
	switch runtimeName {
	case "ollama":
		return "Ollama"
	case "lmstudio", "lm-studio":
		return "LM Studio"
	case "llamacpp", "llama-cpp", "llama.cpp":
		return "llama.cpp"
	default:
		return ""
	}
}

func normalizeLocalBaseURL(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("invalid local base URL: %w", err)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", errors.New("local base URL cannot contain credentials, a query, or a fragment")
	}
	if parsed.Path == "" {
		parsed.Path = "/v1"
	}

	cfg := config.Default()
	cfg.Provider.Connection = "local"
	cfg.Provider.BaseURL = strings.TrimRight(parsed.String(), "/")
	cfg.Provider.Model = "discovery"
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	return cfg.Provider.BaseURL, nil
}

func discoverLocalEndpoint(endpoints []localEndpoint, timeout time.Duration) (localEndpoint, []string, error) {
	var failures []string
	for _, endpoint := range endpoints {
		models, err := fetchLocalModels(endpoint.BaseURL, timeout)
		if err == nil {
			return endpoint, models, nil
		}
		failures = append(failures, endpoint.Name+": "+err.Error())
	}
	return localEndpoint{}, nil, fmt.Errorf(
		"no usable local model server found (%s); start Ollama, LM Studio, or llama.cpp and load a tool-capable model",
		strings.Join(failures, "; "),
	)
}

func fetchLocalModels(baseURL string, timeout time.Duration) ([]string, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/models"
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "ycode/connect")

	response, err := (&http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned %s", endpoint, response.Status)
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode %s: %w", endpoint, err)
	}

	seen := make(map[string]struct{}, len(payload.Data))
	models := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, id)
	}
	if len(models) == 0 {
		return nil, errors.New("server reported no loaded models")
	}
	sort.Slice(models, func(left, right int) bool {
		return strings.ToLower(models[left]) < strings.ToLower(models[right])
	})
	return models, nil
}

func selectLocalModel(models []string, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		for _, model := range models {
			if model == requested {
				return model, nil
			}
		}
		return "", fmt.Errorf("model %q is not available; found: %s", requested, strings.Join(models, ", "))
	}
	if len(models) == 0 {
		return "", errors.New("no local models are available")
	}
	if len(models) == 1 {
		if localModelScore(models[0]) >= 100 {
			return "", fmt.Errorf("model %q does not look like a chat model", models[0])
		}
		return models[0], nil
	}

	var usable []string
	for _, model := range models {
		if localModelScore(model) < 100 {
			usable = append(usable, model)
		}
	}
	if len(usable) == 1 {
		return usable[0], nil
	}
	if len(usable) == 0 {
		return "", errors.New("no local chat models are available")
	}

	var preferred []string
	for _, model := range usable {
		if localModelScore(model) <= 1 {
			preferred = append(preferred, model)
		}
	}
	if len(preferred) == 1 {
		return preferred[0], nil
	}
	return "", fmt.Errorf(
		"multiple local models are available; choose one with --model: %s",
		strings.Join(models, ", "),
	)
}

func localModelScore(model string) int {
	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "coder"), strings.Contains(lower, "devstral"):
		return 0
	case strings.Contains(lower, "code"):
		return 1
	case strings.Contains(lower, "embed"), strings.Contains(lower, "rerank"):
		return 100
	default:
		return 10
	}
}

func printConnectHelp(writer io.Writer) {
	_, _ = fmt.Fprint(writer, `Usage:
  ycode connect cli NAME        use Codex, Claude Code, or OpenCode
  ycode connect cli --list      show installed external coding CLIs
  ycode connect local [flags]   detect and save a local model runtime
  ycode connect api [flags]     save a hosted API connection
  ycode connect status          show the effective connection

Local flags:
  --runtime NAME       auto, ollama, lm-studio, or llama.cpp
  --base-url URL       custom loopback OpenAI-compatible URL
  --model ID           choose an installed model
  --timeout DURATION   discovery timeout per runtime (default 2s)

API flags:
  --base-url URL       hosted OpenAI-compatible URL
  --model ID           provider model
  --api-key-env NAME   environment variable containing the API key
`)
}
