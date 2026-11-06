package dual

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxDiffLines = 50  // as cmd/stuntgres query_diff.go
	maxDiffShown = 120 // bytes of one shown value or row
)

// diffLines collects a record's response_diff lines: every line counts, the
// first maxDiffLines are kept and the rest are summarized.
type diffLines struct {
	lines []string
	total int
}

func (d *diffLines) add(line string) {
	d.total++
	if len(d.lines) < maxDiffLines {
		d.lines = append(d.lines, line)
	}
}

func (d *diffLines) finish() []string {
	if d.total > len(d.lines) {
		d.lines = append(d.lines, fmt.Sprintf("… %d more differences", d.total-len(d.lines)))
	}
	return d.lines
}

// shown truncates s to maxDiffShown bytes on a rune boundary, marking the cut.
func shown(s string) string {
	if len(s) <= maxDiffShown {
		return s
	}
	cut := maxDiffShown
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// jsonValue shows a canonical value as a JSON string, or null.
func jsonValue(v *string) string {
	if v == nil {
		return "null"
	}
	return quoteJSON(shown(*v))
}

func quoteJSON(s string) string { return compactJSON(s) }

// compactJSON encodes v without HTML escaping.
func compactJSON(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
