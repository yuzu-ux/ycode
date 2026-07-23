// Package textsafe contains display-only sanitizers. Model and process output
// are untrusted and must not be able to emit terminal control sequences.
package textsafe

import (
	"strings"
	"unicode"
)

// Terminal removes control characters except newline and tab. In particular,
// ESC is removed so ANSI/OSC sequences cannot be activated by model output.
func Terminal(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	for _, current := range value {
		if current == '\n' || current == '\t' || !unicode.IsControl(current) {
			output.WriteRune(current)
		}
	}
	return output.String()
}
