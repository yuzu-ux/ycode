package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestSpinnerAndBannerStayPlainWhenRedirected(t *testing.T) {
	var output bytes.Buffer
	spinner := NewSpinner(&output)
	spinner.Start("Thinking")
	spinner.Stop()
	if output.Len() != 0 {
		t.Fatalf("redirected spinner output = %q", output.String())
	}

	Banner(&output, "Codex CLI", "ready")
	if output.String() != "YCode · Codex CLI · ready\n" {
		t.Fatalf("banner = %q", output.String())
	}
	if strings.Contains(output.String(), "\x1b") {
		t.Fatal("redirected output contains ANSI controls")
	}
}
