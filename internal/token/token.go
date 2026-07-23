// Package token contains YCode's dependency-free token accounting primitives.
//
// Counts are estimates, deliberately biased slightly high. Exact tokenization is
// provider and model specific; a predictable upper bound is more useful to the
// context budgeter than pretending one tokenizer fits every model.
package token

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const bytesPerEstimatedToken = 4

// EstimateText returns a conservative, model-independent token estimate.
func EstimateText(value string) int {
	if value == "" {
		return 0
	}
	n := (len(value) + bytesPerEstimatedToken - 1) / bytesPerEstimatedToken
	if n == 0 {
		return 1
	}
	return n
}

// EstimateJSON estimates the serialized token cost of a value.
func EstimateJSON(value any) int {
	data, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return EstimateText(string(data))
}

// Ref returns a short content-addressed identifier.
func Ref(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

// ClipResult describes a bounded string and the estimated tokens it avoided.
type ClipResult struct {
	Text       string
	Original   int
	Kept       int
	Saved      int
	WasClipped bool
	ContentRef string
}

// Clip keeps the most useful beginning and ending of a large result.
func Clip(value string, maxTokens int) ClipResult {
	original := EstimateText(value)
	result := ClipResult{
		Text:       value,
		Original:   original,
		Kept:       original,
		ContentRef: Ref(value),
	}
	if maxTokens <= 0 || original <= maxTokens {
		return result
	}

	maxBytes := maxTokens * bytesPerEstimatedToken
	marker := fmt.Sprintf("\n… clipped by YCode; ref=%s; original≈%d tokens …\n", result.ContentRef, original)
	available := maxBytes - len(marker)
	if available < 32 {
		available = 32
	}
	headBytes := available * 3 / 5
	tailBytes := available - headBytes

	head := validPrefix(value, headBytes)
	tail := validSuffix(value, tailBytes)
	result.Text = strings.TrimRight(head, "\n") + marker + strings.TrimLeft(tail, "\n")
	result.Kept = EstimateText(result.Text)
	result.Saved = original - result.Kept
	if result.Saved < 0 {
		result.Saved = 0
	}
	result.WasClipped = true
	return result
}

func validPrefix(value string, n int) string {
	if n >= len(value) {
		return value
	}
	if n <= 0 {
		return ""
	}
	for n > 0 && !utf8.ValidString(value[:n]) {
		n--
	}
	return value[:n]
}

func validSuffix(value string, n int) string {
	if n >= len(value) {
		return value
	}
	if n <= 0 {
		return ""
	}
	start := len(value) - n
	for start < len(value) && !utf8.ValidString(value[start:]) {
		start++
	}
	return value[start:]
}

// Ledger removes repeated tool output and clips first-seen output to a hard
// budget. It is intentionally per session, so references never leak between
// projects.
type Ledger struct {
	seen        map[string]struct{}
	SavedTokens int
}

func NewLedger() *Ledger {
	return &Ledger{seen: make(map[string]struct{})}
}

func (l *Ledger) Compact(value string, maxTokens int) string {
	if l == nil {
		return Clip(value, maxTokens).Text
	}
	ref := Ref(value)
	if _, exists := l.seen[ref]; exists {
		replacement := fmt.Sprintf("[unchanged tool output; ref=%s]", ref)
		saved := EstimateText(value) - EstimateText(replacement)
		if saved > 0 {
			l.SavedTokens += saved
		}
		return replacement
	}
	l.seen[ref] = struct{}{}
	clipped := Clip(value, maxTokens)
	l.SavedTokens += clipped.Saved
	return clipped.Text
}
