// Package externalcli delegates a YCode request to an installed coding-agent
// CLI. Commands are assembled as argv and never passed through a shell.
package externalcli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Definition struct {
	Name        string
	DisplayName string
	Executable  string
}

type Status struct {
	Definition
	Path      string
	Installed bool
}

type Options struct {
	Name     string
	Root     string
	Prompt   string
	ReadOnly bool
	Stdout   io.Writer
	Stderr   io.Writer
	OnStart  func()
}

var definitions = []Definition{
	{Name: "codex", DisplayName: "Codex CLI", Executable: "codex"},
	{Name: "claude", DisplayName: "Claude Code", Executable: "claude"},
	{Name: "opencode", DisplayName: "OpenCode", Executable: "opencode"},
}

func Definitions() []Definition {
	return append([]Definition(nil), definitions...)
}

func Statuses() []Status {
	statuses := make([]Status, 0, len(definitions))
	for _, definition := range definitions {
		path, err := exec.LookPath(definition.Executable)
		statuses = append(statuses, Status{
			Definition: definition,
			Path:       path,
			Installed:  err == nil,
		})
	}
	return statuses
}

func Resolve(name string) (Status, error) {
	definition, err := lookup(name)
	if err != nil {
		return Status{}, err
	}
	path, err := exec.LookPath(definition.Executable)
	if err != nil {
		return Status{}, fmt.Errorf(
			"%s is not installed or not on PATH; install it, then run `ycode connect cli %s` again",
			definition.DisplayName,
			definition.Name,
		)
	}
	return Status{Definition: definition, Path: path, Installed: true}, nil
}

func Run(ctx context.Context, options Options) error {
	status, err := Resolve(options.Name)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}
	if strings.TrimSpace(options.Prompt) == "" {
		return errors.New("external CLI prompt cannot be empty")
	}

	args, err := Arguments(status.Name, root, options.Prompt, options.ReadOnly)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, status.Path, args...)
	command.Dir = root
	command.Env = commandEnvironment(status.Name, options.ReadOnly, os.Environ())
	command.Stdout = options.Stdout
	command.Stderr = options.Stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start %s: %w", status.DisplayName, err)
	}
	if options.OnStart != nil {
		options.OnStart()
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("%s failed: %w", status.DisplayName, err)
	}
	return nil
}

func commandEnvironment(name string, readOnly bool, values []string) []string {
	result := sanitizedEnvironment(values)
	if NormalizeName(name) == "opencode" && readOnly {
		filtered := result[:0]
		for _, value := range result {
			variable, _, _ := strings.Cut(value, "=")
			if !strings.EqualFold(variable, "OPENCODE_PERMISSION") {
				filtered = append(filtered, value)
			}
		}
		result = filtered
		result = append(
			result,
			`OPENCODE_PERMISSION={"edit":"deny","bash":"deny","external_directory":"deny"}`,
		)
	}
	return result
}

func sanitizedEnvironment(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		name, _, found := strings.Cut(value, "=")
		if !found {
			continue
		}
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "TOKEN") ||
			strings.Contains(upper, "SECRET") ||
			strings.Contains(upper, "PASSWORD") ||
			strings.Contains(upper, "CREDENTIAL") ||
			strings.Contains(upper, "API_KEY") ||
			strings.HasSuffix(upper, "_KEY") {
			continue
		}
		result = append(result, value)
	}
	return result
}

func Arguments(name, root, prompt string, readOnly bool) ([]string, error) {
	definition, err := lookup(name)
	if err != nil {
		return nil, err
	}
	switch definition.Name {
	case "codex":
		sandbox := "workspace-write"
		if readOnly {
			sandbox = "read-only"
		}
		return []string{
			"exec",
			"--ephemeral",
			"--sandbox", sandbox,
			"--cd", root,
			prompt,
		}, nil
	case "claude":
		permissionMode := "acceptEdits"
		if readOnly {
			permissionMode = "plan"
		}
		return []string{
			"--print",
			"--output-format", "text",
			"--no-session-persistence",
			"--permission-mode", permissionMode,
			prompt,
		}, nil
	case "opencode":
		if readOnly {
			return []string{"run", "--agent", "plan", prompt}, nil
		}
		// Auto mode makes non-interactive edits usable. Explicit deny rules in
		// the user's OpenCode configuration remain enforced.
		return []string{"run", "--auto", prompt}, nil
	default:
		return nil, fmt.Errorf("unsupported external CLI %q", name)
	}
}

func DisplayName(name string) string {
	definition, err := lookup(name)
	if err != nil {
		return strings.TrimSpace(name)
	}
	return definition.DisplayName
}

func NormalizeName(value string) string {
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

func lookup(name string) (Definition, error) {
	name = NormalizeName(name)
	for _, definition := range definitions {
		if definition.Name == name {
			return definition, nil
		}
	}
	return Definition{}, fmt.Errorf("unknown external CLI %q; use codex, claude, or opencode", name)
}
