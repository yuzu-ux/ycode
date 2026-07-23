package textsafe

import "testing"

func TestTerminalRemovesControls(t *testing.T) {
	input := "safe\x1b]52;c;clipboard\a\nnext\tcolumn\rhidden"
	got := Terminal(input)
	want := "safe]52;c;clipboard\nnext\tcolumnhidden"
	if got != want {
		t.Fatalf("Terminal() = %q, want %q", got, want)
	}
}
