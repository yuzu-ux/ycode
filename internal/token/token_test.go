package token

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestClipPreservesHeadTailAndUTF8(t *testing.T) {
	value := "START-🙂-" + strings.Repeat("middle", 400) + "-🙂-END"
	result := Clip(value, 80)

	if !result.WasClipped {
		t.Fatal("expected result to be clipped")
	}
	if !strings.HasPrefix(result.Text, "START-🙂-") {
		t.Fatalf("head was not preserved: %q", result.Text)
	}
	if !strings.HasSuffix(result.Text, "-🙂-END") {
		t.Fatalf("tail was not preserved: %q", result.Text)
	}
	if !utf8.ValidString(result.Text) {
		t.Fatal("clip split a UTF-8 sequence")
	}
	if result.Saved <= 0 {
		t.Fatalf("expected positive token savings, got %d", result.Saved)
	}
}

func TestLedgerDeduplicatesRepeatedOutput(t *testing.T) {
	ledger := NewLedger()
	value := strings.Repeat("same output\n", 200)
	first := ledger.Compact(value, 100)
	second := ledger.Compact(value, 100)

	if !strings.Contains(first, "clipped by YCode") {
		t.Fatalf("first result should be clipped: %q", first)
	}
	if !strings.Contains(second, "unchanged tool output") {
		t.Fatalf("second result should be a reference: %q", second)
	}
	if ledger.SavedTokens <= 0 {
		t.Fatal("expected savings to be recorded")
	}
}

func TestEstimateTextNeverReturnsZeroForContent(t *testing.T) {
	if got := EstimateText("x"); got != 1 {
		t.Fatalf("EstimateText(x) = %d, want 1", got)
	}
}
