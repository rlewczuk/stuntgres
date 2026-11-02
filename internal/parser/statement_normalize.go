package parser

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// NormalizeStatementText uses the existing scanner to recover selected token
// ends and the original top-level batch span. Scanning is not query identity.
// Typed unary operators remain outside their child's replacement location;
// an actual negative Const starts at its sign and replaces the whole token.
// The second result is the same cleaned source span without replacements.
func NormalizeStatementText(source string, anchor, highest int, locations []int, lists [][2]int, utilities []int, options Options) (normalized, raw string) {
	l := lexer{input: source, options: options, backslashEscapes: !options.StandardConformingStrings}
	type span struct {
		start, end int
		list       bool
	}
	replacements := make([]span, 0, len(locations)+len(lists)+len(utilities))
	// Keep caller locations immutable while ordering both kinds of selected
	// offsets for a single forward sweep alongside the scanner.
	var positionStorage [32]int
	positions := positionStorage[:0]
	if count := len(locations) + len(utilities); count > len(positionStorage) {
		positions = make([]int, 0, count)
	}
	positions = append(positions, locations...)
	positions = append(positions, utilities...)
	slices.Sort(positions)
	nextPosition := 0
	// Recover the list's common enclosing delimiter from the two analyzed
	// element locations. Its entire interior is replaced, including comments,
	// grouped operands, unary signs and trailing cast syntax.
	type listState struct {
		depth               int
		started, last, done bool
	}
	var listStorage [8]listState
	states := listStorage[:min(len(lists), len(listStorage))]
	if len(lists) > len(listStorage) {
		states = make([]listState, len(lists))
	}
	var delimiterStorage [32]int
	delimiters := delimiterStorage[:0]
	start, end := 0, len(source)
	parentheses, atomic, cases := 0, 0, 0
	previousBegin := false
	for {
		token := l.nextRaw()
		if token.code == 0 || l.err != nil {
			break
		}
		if token.unicode {
			token = l.filterUnicodeToken(token)
		}
		at := token.lexeme.pos.Offset
		if token.code == ';' && parentheses == 0 && atomic == 0 {
			if at < anchor {
				start = token.end
				previousBegin = false
				continue
			}
			end = at
			break
		}
		if token.code == '(' {
			parentheses++
		} else if token.code == ')' {
			parentheses--
		}
		if previousBegin && token.lexeme.keyword != nil && token.lexeme.text == "atomic" {
			atomic++
		}
		if atomic > 0 {
			if token.code == CASE {
				cases++
			} else if token.code == END {
				if cases > 0 {
					cases--
				} else {
					atomic--
				}
			}
		}
		previousBegin = token.code == BEGIN
		for i, bounds := range lists {
			state := &states[i]
			if at == bounds[0] && !state.started {
				state.started = true
				state.depth = len(delimiters) - 1
			}
			if at == bounds[1] && state.started {
				state.last = true
			}
		}
		if len(states) > 0 && (token.code == ')' || token.code == ']') {
			depth := len(delimiters) - 1
			if depth >= 0 {
				for i := range states {
					state := &states[i]
					if !state.started || state.done || state.depth != depth {
						continue
					}
					if state.last {
						replacements = append(replacements, span{delimiters[depth], at, true})
						state.done = true
					} else {
						state.depth--
					}
				}
				delimiters = delimiters[:depth]
			}
		} else if len(states) > 0 && (token.code == '(' || token.code == '[') {
			delimiters = append(delimiters, token.end)
		}
		// Advancing also discards duplicates and offsets consumed by signed
		// numeric lookahead or Unicode token filtering.
		for nextPosition < len(positions) && positions[nextPosition] < at {
			nextPosition++
		}
		selected := nextPosition < len(positions) && positions[nextPosition] == at
		tokenEnd := token.end
		if selected && (token.code == '-' || token.code == '+') {
			next := l.nextRaw()
			if next.code == NUMBER {
				tokenEnd = next.end
			} else {
				l.lookahead = next
				l.hasLookahead = true
			}
		}
		if selected {
			replacements = append(replacements, span{at, tokenEnd, false})
		}
	}
	slices.SortFunc(replacements, func(a, b span) int {
		if order := cmp.Compare(a.start, b.start); order != 0 {
			return order
		}
		return cmp.Compare(b.end, a.end)
	})
	startText := start
	for startText < end && space(source[startText]) {
		startText++
	}
	for end > startText && space(source[end-1]) {
		end--
	}
	var out strings.Builder
	cursor, number := startText, highest
	for _, replacement := range replacements {
		if replacement.start < cursor || replacement.start >= end || replacement.end > end {
			continue
		}
		out.WriteString(source[cursor:replacement.start])
		number++
		out.WriteByte('$')
		out.WriteString(strconv.Itoa(number))
		if replacement.list {
			out.WriteString(" /*, ... */")
		}
		cursor = replacement.end
	}
	if cursor == startText {
		return source[startText:end], source[startText:end]
	}
	out.WriteString(source[cursor:end])
	return out.String(), source[startText:end]
}
