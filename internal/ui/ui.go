// Package ui provides small, dependency-free terminal affordances. All dynamic
// rendering is disabled when output is redirected or the terminal opts out.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yuzu-ux/ycode/internal/textsafe"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type Spinner struct {
	writer  io.Writer
	enabled bool

	mu     sync.Mutex
	cancel chan struct{}
	done   chan struct{}
}

func NewSpinner(writer io.Writer) *Spinner {
	return &Spinner{
		writer:  writer,
		enabled: AnimationEnabled(writer),
	}
}

func (spinner *Spinner) Start(label string) {
	if spinner == nil || !spinner.enabled {
		return
	}
	spinner.Stop()

	label = textsafe.Terminal(strings.TrimSpace(label))
	cancel := make(chan struct{})
	done := make(chan struct{})

	spinner.mu.Lock()
	spinner.cancel = cancel
	spinner.done = done
	spinner.mu.Unlock()

	_, _ = fmt.Fprintf(spinner.writer, "\r\x1b[2K\x1b[38;5;141m%s\x1b[0m %s", spinnerFrames[0], label)
	go func() {
		defer close(done)
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		index := 1
		for {
			select {
			case <-cancel:
				return
			case <-ticker.C:
				_, _ = fmt.Fprintf(
					spinner.writer,
					"\r\x1b[2K\x1b[38;5;141m%s\x1b[0m %s",
					spinnerFrames[index%len(spinnerFrames)],
					label,
				)
				index++
			}
		}
	}()
}

func (spinner *Spinner) Stop() {
	if spinner == nil || !spinner.enabled {
		return
	}
	spinner.mu.Lock()
	cancel := spinner.cancel
	done := spinner.done
	spinner.cancel = nil
	spinner.done = nil
	spinner.mu.Unlock()
	if cancel == nil {
		return
	}
	close(cancel)
	<-done
	_, _ = fmt.Fprint(spinner.writer, "\r\x1b[2K")
}

func Banner(writer io.Writer, connection, detail string) {
	connection = textsafe.Terminal(strings.TrimSpace(connection))
	detail = textsafe.Terminal(strings.TrimSpace(detail))
	if !TerminalWriter(writer) || noColor() {
		_, _ = fmt.Fprintf(writer, "YCode · %s · %s\n", connection, detail)
		return
	}
	_, _ = fmt.Fprintf(
		writer,
		"\n  \x1b[1;38;5;141m◆ YCode\x1b[0m  \x1b[38;5;117m%s\x1b[0m\n  \x1b[2m%s\x1b[0m\n\n",
		connection,
		detail,
	)
}

func AnimationEnabled(writer io.Writer) bool {
	if !TerminalWriter(writer) {
		return false
	}
	if truthy(os.Getenv("YCODE_NO_ANIMATION")) || truthy(os.Getenv("CI")) || noColor() {
		return false
	}
	return true
}

func noColor() bool {
	_, exists := os.LookupEnv("NO_COLOR")
	return exists
}

func TerminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb")
}

func InteractiveReader(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
