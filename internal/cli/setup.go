package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/yuzu-ux/ycode/internal/config"
	"github.com/yuzu-ux/ycode/internal/externalcli"
	"github.com/yuzu-ux/ycode/internal/textsafe"
	"github.com/yuzu-ux/ycode/internal/ui"
)

type setupChoice struct {
	Name  string
	Label string
	Kind  string
}

func runSetup(args []string, io streams) int {
	flags := flag.NewFlagSet("ycode setup", flag.ContinueOnError)
	flags.SetOutput(io.err)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if len(flags.Args()) != 0 {
		return fail(io.err, errors.New("setup does not accept positional arguments"))
	}

	ui.Banner(io.err, "first-run setup", "No model is started during discovery")
	choices := setupChoices()
	_, _ = fmt.Fprintln(io.err, "Choose how YCode should run:")
	for index, choice := range choices {
		_, _ = fmt.Fprintf(io.err, "  %d. %-28s %s\n", index+1, choice.Label, setupChoiceDetail(choice))
	}
	for _, status := range externalcli.Statuses() {
		if !status.Installed {
			_, _ = fmt.Fprintf(io.err, "  · %-28s not found on PATH\n", status.DisplayName)
		}
	}
	_, _ = fmt.Fprint(io.err, "\nConnection: ")

	reader := bufio.NewReader(io.in)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return fail(io.err, errors.New("setup cancelled"))
	}
	choice, err := parseSetupChoice(choices, strings.TrimSpace(line))
	if err != nil {
		return fail(io.err, err)
	}

	nested := io
	nested.in = reader
	switch choice.Kind {
	case "cli":
		return runConnectCLI([]string{choice.Name}, nested)
	case "local":
		return runConnectLocalMode(nil, nested, true)
	case "api":
		return runConnectAPI(nil, nested)
	default:
		return fail(io.err, errors.New("invalid setup choice"))
	}
}

func setupChoices() []setupChoice {
	var choices []setupChoice
	for _, status := range externalcli.Statuses() {
		if status.Installed {
			choices = append(choices, setupChoice{
				Name:  status.Name,
				Label: status.DisplayName,
				Kind:  "cli",
			})
		}
	}
	choices = append(choices,
		setupChoice{Name: "local", Label: "Local LLM", Kind: "local"},
		setupChoice{Name: "api", Label: "Hosted API", Kind: "api"},
	)
	return choices
}

func setupChoiceDetail(choice setupChoice) string {
	switch choice.Kind {
	case "cli":
		return "use its existing login"
	case "local":
		return "runs on this Mac only after a prompt"
	case "api":
		return "uses an environment API key"
	default:
		return ""
	}
}

func parseSetupChoice(choices []setupChoice, answer string) (setupChoice, error) {
	normalized := externalcli.NormalizeName(answer)
	for index, choice := range choices {
		if answer == fmt.Sprint(index+1) || normalized == choice.Name {
			return choice, nil
		}
	}
	return setupChoice{}, fmt.Errorf("invalid connection choice %q", textsafe.Terminal(answer))
}

func needsCredentialSetup(cfg config.Config) bool {
	return cfg.Provider.Connection == "api" &&
		requiresKey(cfg.Provider.BaseURL) &&
		cfg.APIKey() == ""
}
