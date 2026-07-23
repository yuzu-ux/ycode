package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Shell struct {
	Root     string
	Policy   string
	Approver Approver
}

type shellArgs struct {
	Command   string `json:"command"`
	Reason    string `json:"reason"`
	TimeoutMS int    `json:"timeout_ms"`
}

var hardDeniedShell = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bsudo\b`),
	regexp.MustCompile(`(?i)\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r)\s+(/|~|\$HOME)(\s|$)`),
	regexp.MustCompile(`(?i)\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r)\s+["']?/\*`),
	regexp.MustCompile(`(?i)\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r)\s+["']?(~|\$HOME|\$\{HOME\})(/|\*|\s|$)`),
	regexp.MustCompile(`(?i)\bgit\b[^\n]*\breset\s+--hard\b`),
	regexp.MustCompile(`(?i)\bgit\b[^\n]*\bclean\s+-[a-z]*f`),
	regexp.MustCompile(`(?i)\b(mkfs|shutdown|reboot|halt|diskutil\s+erase|dd\s+if=)\b`),
	regexp.MustCompile(`:\(\)\s*\{\s*:\|:&\s*;\s*\}`),
}

var safeShell = []*regexp.Regexp{
	regexp.MustCompile(`^(pwd|ls|tree|file|stat|wc)(\s|$)`),
	regexp.MustCompile(`^(rg|grep|find|sed|head|tail)(\s|$)`),
	regexp.MustCompile(`^git\s+(status|diff|log|show|rev-parse|ls-files|branch\s+--show-current)(\s|$)`),
	regexp.MustCompile(`^(go\s+(test|build|vet|fmt)|cargo\s+(test|check|build|fmt|clippy))(\s|$)`),
	regexp.MustCompile(`^(npm|pnpm|yarn)\s+(test|run\s+(test|build|lint|check|typecheck))(\s|$)`),
	regexp.MustCompile(`^(pytest|python3?\s+-m\s+pytest|swift\s+test|make\s+(test|check|build))(\s|$)`),
}

func (s *Shell) Execute(ctx context.Context, arguments string) (string, error) {
	var args shellArgs
	if err := decodeStrict(strings.NewReader(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid shell arguments: %w", err)
	}
	args.Command = strings.TrimSpace(args.Command)
	if args.Command == "" {
		return "", errors.New("shell command cannot be empty")
	}
	if deniedReason := hardDenied(args.Command); deniedReason != "" {
		return "", fmt.Errorf("command blocked by YCode safety policy: %s", deniedReason)
	}

	safe := isSafeCommand(args.Command)
	switch s.Policy {
	case "safe":
		if !safe {
			return "", errors.New("command is outside the safe command set; use shell_policy=ask or allow")
		}
	case "ask", "":
		if !safe {
			if s.Approver == nil || !s.Approver(args.Command, args.Reason) {
				return "", errors.New("command was not approved")
			}
		}
	case "allow":
		// Hard-denied commands remain blocked in every mode.
	default:
		return "", fmt.Errorf("unknown shell policy %q", s.Policy)
	}

	timeout := time.Duration(args.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > 120*time.Second {
		timeout = 120 * time.Second
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name, shellArgs := platformShell(args.Command)
	command := exec.CommandContext(commandContext, name, shellArgs...)
	command.Dir = s.Root
	command.Env = sanitizedEnvironment(os.Environ())

	output := &boundedOutput{limit: 1 << 20}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
		return output.String() + "\n[command timed out after " + timeout.String() + "]", nil
	}
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return output.String() + "\n[exit " + strconv.Itoa(exitError.ExitCode()) + "]", nil
		}
		return "", fmt.Errorf("start command: %w", err)
	}
	result := output.String()
	if strings.TrimSpace(result) == "" {
		return "[command completed with no output]", nil
	}
	return result, nil
}

func hardDenied(command string) string {
	for _, pattern := range hardDeniedShell {
		if pattern.MatchString(command) {
			return "destructive or privileged command"
		}
	}
	return ""
}

func isSafeCommand(command string) bool {
	if strings.ContainsAny(command, "\n;&|`<>") || strings.Contains(command, "$(") {
		return false
	}
	lower := strings.ToLower(command)
	if strings.Contains(lower, ".env") || strings.Contains(lower, "credential") ||
		strings.Contains(lower, "secret") || strings.Contains(lower, "id_rsa") ||
		strings.Contains(lower, "id_ed25519") || strings.Contains(lower, ".pem") ||
		strings.Contains(lower, "find ") && (strings.Contains(lower, " -delete") || strings.Contains(lower, " -exec") || strings.Contains(lower, " -ok")) ||
		strings.HasPrefix(lower, "sed ") && strings.Contains(lower, " -i") {
		return false
	}
	for _, pattern := range safeShell {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

func sanitizedEnvironment(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		name, _, found := strings.Cut(value, "=")
		if !found {
			continue
		}
		upper := strings.ToUpper(name)
		if strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") ||
			strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "CREDENTIAL") ||
			strings.Contains(upper, "API_KEY") || strings.HasSuffix(upper, "_KEY") {
			continue
		}
		result = append(result, value)
	}
	return result
}

func platformShell(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd.exe", []string{"/C", command}
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return shell, []string{"-lc", command}
}

type boundedOutput struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	original := len(data)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
			b.truncated = true
		}
		_, _ = b.buffer.Write(data)
	} else {
		b.truncated = true
	}
	return original, nil
}

func (b *boundedOutput) String() string {
	value := strings.TrimRight(b.buffer.String(), "\n")
	if b.truncated {
		value += "\n[process output truncated at 1 MiB]"
	}
	return value
}
