// Package cli is the dependency-free YCode command-line interface.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuzu-ux/ycode/internal/agent"
	"github.com/yuzu-ux/ycode/internal/config"
	"github.com/yuzu-ux/ycode/internal/externalcli"
	"github.com/yuzu-ux/ycode/internal/provider"
	"github.com/yuzu-ux/ycode/internal/repo"
	"github.com/yuzu-ux/ycode/internal/session"
	"github.com/yuzu-ux/ycode/internal/textsafe"
	"github.com/yuzu-ux/ycode/internal/tools"
	"github.com/yuzu-ux/ycode/internal/ui"
)

type streams struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, version string) int {
	io := streams{in: stdin, out: stdout, err: stderr}
	if len(args) == 0 {
		return runChat(nil, io)
	}

	command := args[0]
	rest := args[1:]
	switch command {
	case "chat":
		return runChat(rest, io)
	case "run":
		return runOnce(rest, io)
	case "map":
		return runMap(rest, io)
	case "bench", "benchmark":
		return runBenchmark(rest, io)
	case "init":
		return runInit(rest, io)
	case "doctor":
		return runDoctor(rest, io)
	case "connect":
		return runConnect(rest, io)
	case "setup":
		return runSetup(rest, io)
	case "sessions":
		return runSessions(rest, io)
	case "version", "--version", "-v":
		_, _ = fmt.Fprintln(stdout, "ycode "+version)
		return 0
	case "help", "--help", "-h":
		printHelp(stdout)
		return 0
	default:
		// A prompt is the most common operation, so `ycode "fix this"` is a
		// shorthand for `ycode run "fix this"`.
		return runOnce(args, io)
	}
}

type runOptions struct {
	root             string
	connection       string
	externalCLI      string
	model            string
	baseURL          string
	apiKeyEnv        string
	inputBudget      int
	outputTokens     int
	mapTokens        int
	toolOutputTokens int
	maxTurns         int
	timeoutSeconds   int
	shellPolicy      string
	stream           bool
	readOnly         bool
	resume           string
	showStats        bool
}

func prepareRunFlags(name string, args []string, stderr io.Writer) (*flag.FlagSet, *runOptions, config.Config, error) {
	root := scanStringFlag(args, "root", ".")
	cfg, _, err := config.Load(root)
	if err != nil {
		return nil, nil, config.Config{}, err
	}
	options := &runOptions{
		root:             root,
		connection:       cfg.Provider.Connection,
		externalCLI:      cfg.Provider.CLI,
		model:            cfg.Provider.Model,
		baseURL:          cfg.Provider.BaseURL,
		apiKeyEnv:        cfg.Provider.APIKeyEnv,
		inputBudget:      cfg.Agent.InputBudgetTokens,
		outputTokens:     cfg.Agent.OutputTokens,
		mapTokens:        cfg.Agent.RepoMapTokens,
		toolOutputTokens: cfg.Agent.ToolOutputTokens,
		maxTurns:         cfg.Agent.MaxTurns,
		timeoutSeconds:   cfg.Provider.TimeoutSeconds,
		shellPolicy:      cfg.Agent.ShellPolicy,
		stream:           cfg.Provider.Stream,
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.root, "root", options.root, "workspace root")
	flags.StringVar(&options.connection, "connection", options.connection, "api, local, or cli")
	flags.StringVar(&options.externalCLI, "cli", options.externalCLI, "external coding CLI: codex, claude, or opencode")
	flags.StringVar(&options.model, "model", options.model, "provider model")
	flags.StringVar(&options.baseURL, "base-url", options.baseURL, "OpenAI-compatible API base URL")
	flags.StringVar(&options.apiKeyEnv, "api-key-env", options.apiKeyEnv, "environment variable containing the API key")
	flags.IntVar(&options.inputBudget, "budget", options.inputBudget, "maximum estimated input tokens per model call")
	flags.IntVar(&options.outputTokens, "output-tokens", options.outputTokens, "maximum output tokens per model call")
	flags.IntVar(&options.mapTokens, "map-tokens", options.mapTokens, "repository-map token budget")
	flags.IntVar(&options.toolOutputTokens, "tool-output-tokens", options.toolOutputTokens, "per-tool-result token budget")
	flags.IntVar(&options.maxTurns, "max-turns", options.maxTurns, "maximum model turns per user request")
	flags.IntVar(&options.timeoutSeconds, "timeout", options.timeoutSeconds, "provider timeout in seconds")
	flags.StringVar(&options.shellPolicy, "shell-policy", options.shellPolicy, "safe, ask, or allow")
	flags.BoolVar(&options.stream, "stream", options.stream, "stream response text")
	flags.BoolVar(&options.readOnly, "read-only", false, "disable file edits")
	flags.StringVar(&options.resume, "resume", "", "resume a session ID or latest")
	flags.BoolVar(&options.showStats, "stats", false, "print token statistics after the run")
	return flags, options, cfg, nil
}

func runOnce(args []string, io streams) int {
	flags, options, cfg, err := prepareRunFlags("ycode run", args, io.err)
	if err != nil {
		return fail(io.err, err)
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	cfg = applyRunOptions(cfg, options)
	if err := cfg.Validate(); err != nil {
		return fail(io.err, err)
	}

	prompt := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if prompt == "-" {
		data, err := ioReadAllLimit(io.in, 4<<20)
		if err != nil {
			return fail(io.err, err)
		}
		prompt = strings.TrimSpace(string(data))
	}
	if prompt == "" {
		return fail(io.err, errors.New("missing prompt; try: ycode run \"explain this repository\""))
	}
	if needsCredentialSetup(cfg) && ui.InteractiveReader(io.in) && !hasProviderOverride(args) {
		_, _ = fmt.Fprintln(io.err, "YCode needs a connection before the first run.")
		if exit := runSetup(nil, io); exit != 0 {
			return exit
		}
		cfg, _, err = config.Load(options.root)
		if err != nil {
			return fail(io.err, err)
		}
		loadProviderOptions(options, cfg)
	}
	if cfg.Provider.Connection == "cli" {
		return runExternalPrompt(context.Background(), cfg, options, prompt, io)
	}

	runner, err := buildAgent(cfg, options, nil, io)
	if err != nil {
		return fail(io.err, err)
	}
	if err := runner.Run(context.Background(), prompt); err != nil {
		return fail(io.err, err)
	}
	if options.showStats {
		_, _ = fmt.Fprintln(io.err, "tokens: "+runner.Stats().String())
	}
	return 0
}

func runChat(args []string, io streams) int {
	flags, options, cfg, err := prepareRunFlags("ycode chat", args, io.err)
	if err != nil {
		return fail(io.err, err)
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) != 0 {
		return fail(io.err, errors.New("chat does not accept a prompt argument; use ycode run"))
	}
	cfg = applyRunOptions(cfg, options)
	if err := cfg.Validate(); err != nil {
		return fail(io.err, err)
	}
	if needsCredentialSetup(cfg) && ui.InteractiveReader(io.in) && !hasProviderOverride(args) {
		_, _ = fmt.Fprintln(io.err, "YCode needs a connection before the first chat.")
		if exit := runSetup(nil, io); exit != 0 {
			return exit
		}
		cfg, _, err = config.Load(options.root)
		if err != nil {
			return fail(io.err, err)
		}
		loadProviderOptions(options, cfg)
	}
	if cfg.Provider.Connection == "cli" {
		return runExternalChat(cfg, options, io)
	}

	reader := bufio.NewReader(io.in)
	approver := func(command, reason string) bool {
		_, _ = fmt.Fprintln(io.err, "\nShell approval requested")
		_, _ = fmt.Fprintln(io.err, "  "+command)
		if strings.TrimSpace(reason) != "" {
			_, _ = fmt.Fprintln(io.err, "  reason: "+reason)
		}
		_, _ = fmt.Fprint(io.err, "Approve once? [y/N] ")
		answer, _ := reader.ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		return answer == "y" || answer == "yes"
	}
	runner, err := buildAgent(cfg, options, approver, io)
	if err != nil {
		return fail(io.err, err)
	}

	ui.Banner(io.err, options.model, "session "+runner.SessionID())
	_, _ = fmt.Fprintln(io.err, "Type /help for commands. Ctrl-D or /exit to leave.")
	for {
		_, _ = fmt.Fprint(io.err, "ycode › ")
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if readErr != nil && line == "" {
			_, _ = fmt.Fprintln(io.err)
			return 0
		}
		switch {
		case line == "":
			continue
		case line == "/exit" || line == "/quit":
			return 0
		case line == "/help":
			printChatHelp(io.err)
			continue
		case line == "/stats":
			_, _ = fmt.Fprintln(io.err, "tokens: "+runner.Stats().String())
			continue
		case line == "/session":
			_, _ = fmt.Fprintln(io.err, runner.SessionID())
			continue
		case line == "/clear":
			store, storeErr := session.NewStore(options.root)
			if storeErr != nil {
				_, _ = fmt.Fprintln(io.err, "error: "+storeErr.Error())
				continue
			}
			state, stateErr := store.New()
			if stateErr != nil {
				_, _ = fmt.Fprintln(io.err, "error: "+stateErr.Error())
				continue
			}
			runner.Reset(state)
			_, _ = fmt.Fprintln(io.err, "started session "+runner.SessionID())
			continue
		case strings.HasPrefix(line, "/map"):
			query := strings.TrimSpace(strings.TrimPrefix(line, "/map"))
			snapshot, mapErr := repo.Build(options.root, query, options.mapTokens)
			if mapErr != nil {
				_, _ = fmt.Fprintln(io.err, "error: "+mapErr.Error())
			} else {
				_, _ = fmt.Fprintln(io.out, textsafe.Terminal(snapshot.Text))
			}
			continue
		case strings.HasPrefix(line, "/"):
			_, _ = fmt.Fprintln(io.err, "unknown command; try /help")
			continue
		}
		if err := runner.Run(context.Background(), line); err != nil {
			_, _ = fmt.Fprintln(io.err, "error: "+err.Error())
		}
	}
}

func buildAgent(cfg config.Config, options *runOptions, approver tools.Approver, io streams) (*agent.Agent, error) {
	absolute, err := filepath.Abs(options.root)
	if err != nil {
		return nil, err
	}
	options.root = absolute
	key := cfg.APIKey()
	if options.connection == "api" && key == "" && requiresKey(options.baseURL) {
		return nil, fmt.Errorf(
			"no API key found; run `ycode setup` to use Codex, Claude Code, OpenCode, a local model, or a hosted API",
		)
	}
	if key != "" && !secureForCredential(options.baseURL) {
		return nil, errors.New("refusing to send an API key over non-loopback HTTP; use HTTPS or a loopback URL")
	}
	registry, err := tools.New(tools.Options{
		Root:        absolute,
		ReadOnly:    options.readOnly,
		ShellPolicy: options.shellPolicy,
		Approver:    approver,
	})
	if err != nil {
		return nil, err
	}
	store, err := session.NewStore(absolute)
	if err != nil {
		return nil, err
	}
	var state *session.State
	if options.resume != "" {
		state, err = store.Load(options.resume)
		if err != nil {
			return nil, fmt.Errorf("resume session: %w", err)
		}
	}
	client := provider.NewClient(
		options.baseURL,
		key,
		time.Duration(options.timeoutSeconds)*time.Second,
	)
	return agent.New(agent.Options{
		Root:             absolute,
		Model:            options.model,
		InputBudget:      options.inputBudget,
		OutputTokens:     options.outputTokens,
		RepoMapTokens:    options.mapTokens,
		ToolOutputTokens: options.toolOutputTokens,
		MaxTurns:         options.maxTurns,
		Stream:           options.stream,
		Provider:         client,
		Tools:            registry,
		Store:            store,
		State:            state,
		Stdout:           io.out,
		Progress:         io.err,
		Status:           ui.NewSpinner(io.err),
	})
}

func applyRunOptions(cfg config.Config, options *runOptions) config.Config {
	cfg.Provider.Connection = options.connection
	cfg.Provider.CLI = externalcli.NormalizeName(options.externalCLI)
	cfg.Provider.BaseURL = options.baseURL
	cfg.Provider.Model = options.model
	cfg.Provider.APIKeyEnv = options.apiKeyEnv
	cfg.Provider.TimeoutSeconds = options.timeoutSeconds
	cfg.Provider.Stream = options.stream
	cfg.Agent.InputBudgetTokens = options.inputBudget
	cfg.Agent.OutputTokens = options.outputTokens
	cfg.Agent.RepoMapTokens = options.mapTokens
	cfg.Agent.ToolOutputTokens = options.toolOutputTokens
	cfg.Agent.MaxTurns = options.maxTurns
	cfg.Agent.ShellPolicy = options.shellPolicy
	return cfg
}

func loadProviderOptions(options *runOptions, cfg config.Config) {
	options.connection = cfg.Provider.Connection
	options.externalCLI = cfg.Provider.CLI
	options.baseURL = cfg.Provider.BaseURL
	options.model = cfg.Provider.Model
	options.apiKeyEnv = cfg.Provider.APIKeyEnv
	options.timeoutSeconds = cfg.Provider.TimeoutSeconds
	options.stream = cfg.Provider.Stream
}

func hasProviderOverride(args []string) bool {
	for _, name := range []string{"connection", "cli", "model", "base-url", "api-key-env"} {
		long := "--" + name
		for _, value := range args {
			if value == long || strings.HasPrefix(value, long+"=") {
				return true
			}
		}
	}
	return false
}

func runExternalPrompt(ctx context.Context, cfg config.Config, options *runOptions, prompt string, io streams) int {
	absolute, err := filepath.Abs(options.root)
	if err != nil {
		return fail(io.err, err)
	}
	spinner := ui.NewSpinner(io.err)
	spinner.Start("Starting " + externalcli.DisplayName(cfg.Provider.CLI))
	err = externalcli.Run(ctx, externalcli.Options{
		Name:     cfg.Provider.CLI,
		Root:     absolute,
		Prompt:   prompt,
		ReadOnly: options.readOnly,
		Stdout:   io.out,
		Stderr:   io.err,
		OnStart:  spinner.Stop,
	})
	spinner.Stop()
	if err != nil {
		return fail(io.err, err)
	}
	return 0
}

func runExternalChat(cfg config.Config, options *runOptions, io streams) int {
	reader := bufio.NewReader(io.in)
	displayName := externalcli.DisplayName(cfg.Provider.CLI)
	ui.Banner(io.err, displayName, "lean external handoff · existing CLI login")
	_, _ = fmt.Fprintln(io.err, "Each prompt is delegated directly. Type /help or /exit.")
	for {
		_, _ = fmt.Fprint(io.err, "ycode › ")
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if readErr != nil && line == "" {
			_, _ = fmt.Fprintln(io.err)
			return 0
		}
		switch {
		case line == "":
			continue
		case line == "/exit" || line == "/quit":
			return 0
		case line == "/help":
			_, _ = fmt.Fprintln(io.err, "/map [query]  show YCode's bounded repository map")
			_, _ = fmt.Fprintln(io.err, "/stats        explain external token accounting")
			_, _ = fmt.Fprintln(io.err, "/exit         leave YCode")
			continue
		case line == "/stats":
			_, _ = fmt.Fprintln(io.err, "Token accounting is owned by "+textsafe.Terminal(displayName)+"; YCode adds no repo map or tool schemas.")
			continue
		case strings.HasPrefix(line, "/map"):
			query := strings.TrimSpace(strings.TrimPrefix(line, "/map"))
			snapshot, mapErr := repo.Build(options.root, query, options.mapTokens)
			if mapErr != nil {
				_, _ = fmt.Fprintln(io.err, "error: "+mapErr.Error())
			} else {
				_, _ = fmt.Fprintln(io.out, textsafe.Terminal(snapshot.Text))
			}
			continue
		case strings.HasPrefix(line, "/"):
			_, _ = fmt.Fprintln(io.err, "unknown command; try /help")
			continue
		}
		if exit := runExternalPrompt(context.Background(), cfg, options, line, io); exit != 0 {
			_, _ = fmt.Fprintln(io.err, "The external CLI stopped; you can fix its login/config and try another prompt.")
		}
	}
}

func runMap(args []string, io streams) int {
	root := scanStringFlag(args, "root", ".")
	cfg, _, err := config.Load(root)
	if err != nil {
		return fail(io.err, err)
	}
	flags := flag.NewFlagSet("ycode map", flag.ContinueOnError)
	flags.SetOutput(io.err)
	flags.StringVar(&root, "root", root, "workspace root")
	budget := flags.Int("tokens", cfg.Agent.RepoMapTokens, "map token budget")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	snapshot, err := repo.Build(root, strings.Join(flags.Args(), " "), *budget)
	if err != nil {
		return fail(io.err, err)
	}
	_, _ = fmt.Fprint(io.out, textsafe.Terminal(snapshot.Text))
	_, _ = fmt.Fprintf(io.err, "map: %d/%d files, ≈%d tokens\n", snapshot.FilesIncluded, snapshot.FilesScanned, snapshot.EstimatedTokens)
	return 0
}

func runBenchmark(args []string, io streams) int {
	root := scanStringFlag(args, "root", ".")
	cfg, _, err := config.Load(root)
	if err != nil {
		return fail(io.err, err)
	}
	flags := flag.NewFlagSet("ycode benchmark", flag.ContinueOnError)
	flags.SetOutput(io.err)
	flags.StringVar(&root, "root", root, "workspace root")
	budget := flags.Int("map-tokens", cfg.Agent.RepoMapTokens, "repository-map token budget")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	measurement, err := repo.Measure(root, strings.Join(flags.Args(), " "), *budget)
	if err != nil {
		return fail(io.err, err)
	}
	_, _ = fmt.Fprintf(io.out, "text files measured       %d\n", measurement.Files)
	_, _ = fmt.Fprintf(io.out, "naive full context        ≈%d tokens\n", measurement.NaiveContextTokens)
	_, _ = fmt.Fprintf(io.out, "YCode repository map      ≈%d tokens\n", measurement.MapTokens)
	_, _ = fmt.Fprintf(io.out, "input avoided             ≈%d tokens (%.1f%%)\n", measurement.AvoidedTokens, measurement.ReductionPercentage)
	_, _ = fmt.Fprintf(io.out, "map build time            %s\n", measurement.MapBuildDuration.Round(time.Microsecond))
	return 0
}

func runInit(args []string, io streams) int {
	flags := flag.NewFlagSet("ycode init", flag.ContinueOnError)
	flags.SetOutput(io.err)
	root := flags.String("root", ".", "workspace root")
	force := flags.Bool("force", false, "replace existing YCode files")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) != 0 {
		return fail(io.err, errors.New("init does not accept positional arguments"))
	}
	path, err := config.WriteProjectTemplate(*root, *force)
	if err != nil {
		return fail(io.err, err)
	}
	instructions, instructionErr := writeInstructions(*root, *force)
	if instructionErr != nil {
		return fail(io.err, instructionErr)
	}
	_, _ = fmt.Fprintln(io.out, "created "+textsafe.Terminal(path))
	_, _ = fmt.Fprintln(io.out, "created "+textsafe.Terminal(instructions))
	_, _ = fmt.Fprintln(io.out, "API keys stay in environment variables and are never written to these files.")
	return 0
}

func writeInstructions(root string, force bool) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(absolute, "YCODE.md")
	if !force {
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("%s already exists (use --force to replace it)", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	content := `# YCode project instructions

Describe the project-specific commands, constraints, and conventions the agent
should follow. Keep this file short: YCode places it near the top of the
query-ranked repository map.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func runSessions(args []string, io streams) int {
	flags := flag.NewFlagSet("ycode sessions", flag.ContinueOnError)
	flags.SetOutput(io.err)
	root := flags.String("root", ".", "workspace root")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	store, err := session.NewStore(*root)
	if err != nil {
		return fail(io.err, err)
	}
	entries, err := store.List()
	if err != nil {
		return fail(io.err, err)
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(io.out, "No saved sessions for this workspace.")
		return 0
	}
	for _, entry := range entries {
		_, _ = fmt.Fprintf(io.out, "%s  %s  %d messages\n", entry.ID, entry.UpdatedAt.Local().Format("2006-01-02 15:04"), entry.Messages)
	}
	return 0
}

func scanStringFlag(args []string, name, fallback string) string {
	long := "--" + name
	for index, value := range args {
		if value == long && index+1 < len(args) {
			return args[index+1]
		}
		if strings.HasPrefix(value, long+"=") {
			return strings.TrimPrefix(value, long+"=")
		}
	}
	return fallback
}

func requiresKey(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return true
	}
	host := parsed.Hostname()
	if host == "localhost" || host == "::1" {
		return false
	}
	return net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()
}

func secureForCredential(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	host := parsed.Hostname()
	if host == "localhost" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func ioReadAllLimit(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("stdin exceeds %d bytes", limit)
	}
	return data, nil
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, "error: "+textsafe.Terminal(err.Error()))
	return 1
}

func printChatHelp(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "/map [query]  show the bounded repository map")
	_, _ = fmt.Fprintln(writer, "/stats        show estimated and provider token usage")
	_, _ = fmt.Fprintln(writer, "/session      show the current session ID")
	_, _ = fmt.Fprintln(writer, "/clear        start a new session without deleting the old one")
	_, _ = fmt.Fprintln(writer, "/exit         leave YCode")
}

func printHelp(writer io.Writer) {
	_, _ = fmt.Fprint(writer, `YCode — a fast, token-budgeted coding-agent harness

Usage:
  ycode                              interactive chat
  ycode "fix the failing test"       one-shot shorthand
  ycode run [flags] <prompt>         one-shot agent
  ycode chat [flags]                 interactive agent
  ycode map [flags] [query]          preview query-ranked context
  ycode benchmark [flags] [query]    measure repository-context reduction
  ycode setup                        guided first-run connection setup
  ycode connect cli NAME             use Codex, Claude Code, or OpenCode
  ycode connect local [flags]        detect and save a local model runtime
  ycode connect api [flags]          save a hosted API connection
  ycode connect status               show the effective model connection
  ycode init [flags]                 create project config and YCODE.md
  ycode doctor [flags]               verify local setup
  ycode sessions [--root PATH]       list resumable sessions
  ycode version

Common agent flags:
  --root PATH              workspace root
  --connection MODE        api, local, or cli
  --cli NAME               codex, claude, or opencode
  --model ID               provider model
  --base-url URL           OpenAI-compatible API base
  --budget TOKENS          per-call input budget
  --shell-policy MODE      safe, ask, or allow
  --read-only              disable edits
  --resume ID              resume an ID or "latest"
  --stats                  show token accounting

Configuration precedence:
  defaults < global config < .ycode/config.json < YCODE_* environment < flags
`)
}
