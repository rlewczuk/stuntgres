package dual

import (
	"fmt"
	"slices"
	"strings"
)

// hasTopLevelOrderBy reports whether any statement of query has ORDER BY
// outside parentheses and brackets. It skips comments (nested /* */),
// standard, escape and dollar-quoted strings and quoted identifiers.
func hasTopLevelOrderBy(query string) bool {
	depth, afterOrder := 0, false
	for i := 0; i < len(query); {
		ch := query[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f' || ch == '\v':
			i++
			continue
		case strings.HasPrefix(query[i:], "--"):
			if end := strings.IndexByte(query[i:], '\n'); end >= 0 {
				i += end + 1
			} else {
				i = len(query)
			}
			continue
		case strings.HasPrefix(query[i:], "/*"):
			i = skipComment(query, i)
			continue
		case (ch == 'E' || ch == 'e') && i+1 < len(query) && query[i+1] == '\'':
			i = skipQuoted(query, i+1, '\'', true)
		case ch == '\'' || ch == '"':
			i = skipQuoted(query, i, ch, false)
		case ch == '$':
			i = skipDollar(query, i)
		case isIdentStart(ch):
			j := i
			for j < len(query) && isIdentChar(query[j]) {
				j++
			}
			word := query[i:j]
			if afterOrder && strings.EqualFold(word, "BY") {
				return true
			}
			afterOrder = depth == 0 && strings.EqualFold(word, "ORDER")
			i = j
			continue
		case ch == '(' || ch == '[':
			depth++
			i++
		case ch == ')' || ch == ']':
			depth = max(depth-1, 0)
			i++
		case ch == ';':
			depth = 0
			i++
		default:
			i++
		}
		afterOrder = false
	}
	return false
}

func isIdentStart(ch byte) bool {
	return ch == '_' || ch >= 0x80 || (ch|0x20 >= 'a' && ch|0x20 <= 'z')
}

func isIdentChar(ch byte) bool { return isIdentStart(ch) || ch == '$' || (ch >= '0' && ch <= '9') }

// skipComment skips a nested block comment starting at i.
func skipComment(query string, i int) int {
	depth := 0
	for i < len(query) {
		switch {
		case strings.HasPrefix(query[i:], "/*"):
			depth++
			i += 2
		case strings.HasPrefix(query[i:], "*/"):
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return i
}

// skipQuoted skips a quoted token whose opening quote is at i; a doubled
// quote is an escaped quote, and so is a backslash escape when escapes is set.
func skipQuoted(query string, i int, quote byte, escapes bool) int {
	for i++; i < len(query); i++ {
		switch query[i] {
		case '\\':
			if escapes {
				i++
			}
		case quote:
			if i+1 < len(query) && query[i+1] == quote {
				i++
				continue
			}
			return i + 1
		}
	}
	return i
}

// skipDollar skips a dollar-quoted string at i, or just the '$' of a
// parameter such as $1.
func skipDollar(query string, i int) int {
	j := i + 1
	if j < len(query) && query[j] >= '0' && query[j] <= '9' {
		return j
	}
	for j < len(query) && query[j] != '$' && isIdentChar(query[j]) {
		j++
	}
	if j >= len(query) || query[j] != '$' {
		return i + 1
	}
	tag := query[i : j+1]
	if end := strings.Index(query[j+1:], tag); end >= 0 {
		return j + 1 + end + len(tag)
	}
	return len(query)
}

// canonSet is one result set's canonical rows for classification.
type canonSet struct {
	columns []string
	rows    [][]*string
	partial bool
}

// classifySet compares a native set n with the complete reference set r of
// the same width. It returns whether n matched only as a multiset; ordered
// decides whether that adds a line.
func classifySet(d *diffLines, s int, n, r canonSet, ordered bool) bool {
	nk, rk := rowKeys(n.rows), rowKeys(r.rows)
	k := len(nk)
	if !n.partial {
		if slices.Equal(nk, rk) {
			return false
		}
		if len(nk) == len(rk) && len(subtract(nk, rk)) == 0 {
			orderLine(d, s, nk, rk, ordered)
			return true
		}
		if len(nk) != len(rk) {
			d.add(fmt.Sprintf("#%d rows: stuntgres %d, reference %d", s, len(nk), len(rk)))
		}
		pairRows(d, s, n, r, subtract(nk, rk), subtract(rk, nk), true)
		return false
	}
	prefix := rk[:min(k, len(rk))]
	if k <= len(rk) && slices.Equal(nk, prefix) {
		return false
	}
	if len(subtract(nk, rk)) == 0 {
		orderLine(d, s, nk, rk, ordered)
		return true
	}
	if k > len(rk) {
		d.add(fmt.Sprintf("#%d rows: stuntgres at least %d (partial), reference %d", s, k, len(rk)))
	}
	// Unmatched reference rows beyond the consumed prefix may still follow
	// in native's unread rows, so they are not reported.
	pairRows(d, s, n, r, subtract(nk, rk), subtract(prefix, nk), false)
	return false
}

func orderLine(d *diffLines, s int, nk, rk []string, ordered bool) {
	if !ordered {
		return
	}
	first := 0
	for first < len(nk) && first < len(rk) && nk[first] == rk[first] {
		first++
	}
	d.add(fmt.Sprintf("#%d rows: same multiset, different order (first at row %d)", s, first))
}

// subtract returns the indices of x's rows left after removing y's rows as a
// multiset; equal rows match first occurrence first.
func subtract(x, y []string) []int {
	count := make(map[string]int, len(y))
	for _, key := range y {
		count[key]++
	}
	var left []int
	for i, key := range x {
		if count[key] > 0 {
			count[key]--
			continue
		}
		left = append(left, i)
	}
	return left
}

func rowKeys(rows [][]*string) []string {
	keys := make([]string, len(rows))
	for i, row := range rows {
		keys[i] = compactJSON(row)
	}
	return keys
}

// pairRows pairs unmatched native rows a with unmatched reference rows b in
// order, adding cell lines for differing values, then one-sided lines.
func pairRows(d *diffLines, s int, n, r canonSet, a, b []int, refOnly bool) {
	m := min(len(a), len(b))
	for p := range m {
		i, j := a[p], b[p]
		at := fmt.Sprintf("#%d row %d", s, i)
		if i != j {
			at += fmt.Sprintf(" (reference %d)", j)
		}
		nr, rr := n.rows[i], r.rows[j]
		for c := range min(len(nr), len(rr)) {
			if !sameCell(nr[c], rr[c]) {
				d.add(fmt.Sprintf("%s %s: stuntgres %s, reference %s", at, columnLabel(n.columns, c), jsonValue(nr[c]), jsonValue(rr[c])))
			}
		}
	}
	for _, i := range a[m:] {
		d.add(fmt.Sprintf("#%d stuntgres only row %d %s", s, i, shown(compactJSON(n.rows[i]))))
	}
	if refOnly {
		for _, j := range b[m:] {
			d.add(fmt.Sprintf("#%d reference only row %d %s", s, j, shown(compactJSON(r.rows[j]))))
		}
	}
}

func sameCell(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// columnLabel is the column's name when it is non-empty and unique, else [K].
func columnLabel(columns []string, k int) string {
	if k < len(columns) && columns[k] != "" {
		count := 0
		for _, c := range columns {
			if c == columns[k] {
				count++
			}
		}
		if count == 1 {
			return columns[k]
		}
	}
	return fmt.Sprintf("[%d]", k)
}
