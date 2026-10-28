package fts

import (
	"context"
	"strings"
)

// NormalizeDocument uses the already captured parser and dictionary chain.
type NormalizeDocument func(string) ([]LexicalPosition, error)

func addNode(nodes *[]queryNode, n queryNode) int {
	*nodes = append(*nodes, n)
	return len(*nodes) - 1
}
func joinNode(nodes *[]queryNode, op byte, left, right, distance int) int {
	return addNode(nodes, queryNode{op: op, left: left, right: right, distance: uint16(distance)})
}
func expandOperand(ctx context.Context, nodes *[]queryNode, source queryNode, normalize NormalizeDocument, phrase bool) (int, error) {
	positions, err := normalize(source.word)
	if err != nil {
		return 0, err
	}
	root, last := -1, 0
	for _, position := range positions {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		// A morph contributes only its surviving span to the outer grammar.
		// Position differences retain internal stops; edge padding must not
		// become phrase width outside this single grammatical operand.
		if len(position.Lexemes) == 0 {
			continue
		}
		alternatives, variant := -1, -1
		var group uint16
		for _, lexeme := range position.Lexemes {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			if len(lexeme.Text) > maxWordBytes {
				return 0, failure("54000", "word is too long in tsquery")
			}
			if variant != -1 && group != lexeme.Variant {
				if alternatives == -1 {
					alternatives = variant
				} else {
					alternatives = joinNode(nodes, '|', alternatives, variant, 0)
				}
				variant = -1
			}
			group = lexeme.Variant
			leaf := addNode(nodes, queryNode{op: 'v', word: lexeme.Text, prefix: source.prefix || lexeme.Prefix, mask: source.mask})
			if variant == -1 {
				variant = leaf
			} else {
				variant = joinNode(nodes, '&', variant, leaf, 0)
			}
		}
		if alternatives == -1 {
			alternatives = variant
		} else if variant != -1 {
			alternatives = joinNode(nodes, '|', alternatives, variant, 0)
		}
		if root == -1 {
			root = alternatives
		} else {
			op, distance := byte('&'), 0
			if phrase {
				op, distance = '<', position.Position-last
			}
			root = joinNode(nodes, op, root, alternatives, distance)
		}
		last = position.Position
	}
	if root == -1 {
		root = addNode(nodes, queryNode{op: 's'})
	}
	return root, nil
}

// ConvertQuery constructs one owned postorder arena. The boolean distinguishes
// an empty grammar from operands that normalized entirely to stopwords.
func ConvertQuery(ctx context.Context, kind ConversionKind, text string, normalize NormalizeDocument) (*Query, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	var q *Query
	var err error
	parsed := false
	switch kind {
	case StrictConversion:
		q, err = parseQuery(ctx, text, func(nodes *[]queryNode, n queryNode) (int, error) {
			parsed = true
			return expandOperand(ctx, nodes, n, normalize, true)
		})
	case PlainConversion, PhraseConversion:
		var positions []LexicalPosition
		positions, err = normalize(text)
		if err == nil {
			parsed = text != ""
			q = &Query{root: -1}
			if parsed {
				q.root, err = expandOperand(ctx, &q.nodes, queryNode{word: text}, func(string) ([]LexicalPosition, error) { return positions, nil }, kind == PhraseConversion)
			}
		}
	case WebConversion:
		q, parsed, err = webQuery(ctx, text, normalize)
	default:
		return nil, false, failure("XX000", "invalid query conversion kind")
	}
	if err != nil {
		return nil, false, err
	}
	q, err = cleanupQuery(ctx, q)
	return q, parsed, err
}

// cleanupQuery retains two-sided phrase widths until their enclosing operator
// consumes them. Its input is postorder, so traversal needs no recursion.
func cleanupQuery(ctx context.Context, source *Query) (*Query, error) {
	type reduced struct{ root, left, right int }
	states := make([]reduced, len(source.nodes))
	out := &Query{root: -1}
	for i, n := range source.nodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state := reduced{root: -1}
		switch n.op {
		case 'v':
			state.root = addNode(&out.nodes, n)
		case 's':
		case '!':
			state = states[n.right]
			if state.root != -1 {
				n.right = state.root
				state.root = addNode(&out.nodes, n)
			}
		default:
			l, r := states[n.left], states[n.right]
			if l.root == -1 && r.root == -1 {
				width := l.left + l.right
				other := r.left + r.right
				if n.op == '<' {
					width += int(n.distance) + other
				} else if other > width {
					width = other
				}
				state.right = width
			} else if l.root == -1 {
				state = r
				if n.op == '<' {
					state.left += l.left + l.right + int(n.distance)
				} else {
					state.left, state.right = 0, 0
				}
			} else if r.root == -1 {
				state = l
				if n.op == '<' {
					state.right += r.left + r.right + int(n.distance)
				} else {
					state.left, state.right = 0, 0
				}
			} else {
				n.left, n.right = l.root, r.root
				if n.op == '<' {
					distance := int(n.distance) + l.right + r.left
					n.distance = uint16(distance)
					state.left, state.right = l.left, r.right
				}
				state.root = addNode(&out.nodes, n)
			}
		}
		states[i] = state
	}
	if source.root >= 0 {
		out.root = states[source.root].root
	}
	if out.root == -1 {
		out.nodes = nil
	}
	var operandBytes uint64
	for i := len(out.nodes) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := out.nodes[i]
		if n.op != 'v' {
			continue
		}
		if operandBytes > maxStringOffset {
			return nil, failure("54000", "tsquery is too large")
		}
		operandBytes += uint64(len(n.word)) + 1
	}
	if queryTooBig(uint64(len(out.nodes)), operandBytes) {
		return nil, failure("54000", "tsquery is too large")
	}
	rendered, err := out.render(ctx, false)
	if err != nil {
		return nil, err
	}
	out.text = rendered
	return out, nil
}

func webSpace(text string, at int) bool {
	return space(text[at])
}

// Match the released web operator at the input cursor. After OR, a word
// continuation rejects the operator. Otherwise skip that first delimiter and
// require some later nonspace byte, even if it is not a valid operand itself.
func webOR(ctx context.Context, text string, at int) (bool, error) {
	if len(text)-at < 3 || (text[at] != 'o' && text[at] != 'O') || (text[at+1] != 'r' && text[at+1] != 'R') {
		return false, nil
	}
	c := text[at+2]
	// UTF8+C uses the same alpha/alnum classification as document parsing.
	if c == '-' || c == '_' || c >= 128 || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
		return false, nil
	}
	for next := at + 3; next < len(text); next++ {
		if next&255 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if !webSpace(text, next) {
			return true, nil
		}
	}
	return false, nil
}

// Web input has operand/operator states, but no strict tsquery punctuation.
// Quoted and unquoted spans both normalize through the document parser.
func webQuery(ctx context.Context, text string, normalize NormalizeDocument) (*Query, bool, error) {
	q := &Query{root: -1}
	conjunction, disjunction := -1, -1
	negate, pendingOR := 0, false
	parsed := false
	for at := 0; at < len(text); {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if webSpace(text, at) {
			at++
			continue
		}
		if text[at] == '-' {
			negate++
			at++
			continue
		}
		if strings.ContainsRune("()|&!", rune(text[at])) {
			at++
			continue
		}
		if conjunction != -1 && !pendingOR && negate == 0 {
			operator, err := webOR(ctx, text, at)
			if err != nil {
				return nil, false, err
			}
			if operator {
				at += 2
				pendingOR = true
				continue
			}
		}
		start, quoted := at, text[at] == '"'
		var word string
		if quoted {
			at++
			start = at
			for at < len(text) && text[at] != '"' {
				if at&255 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, false, err
					}
				}
				at++
			}
			word = text[start:at]
			if at < len(text) {
				at++
			}
		} else {
			for at < len(text) && !webSpace(text, at) && text[at] != '"' && !strings.ContainsRune("()|&!", rune(text[at])) {
				if at&255 == 0 {
					if err := ctx.Err(); err != nil {
						return nil, false, err
					}
				}
				at++
			}
			word = text[start:at]
		}
		if word == "" && !quoted {
			continue
		}
		parsed = true
		positions, err := normalize(word)
		if err != nil {
			return nil, false, err
		}
		leaf, err := expandOperand(ctx, &q.nodes, queryNode{word: word}, func(string) ([]LexicalPosition, error) { return positions, nil }, true)
		if err != nil {
			return nil, false, err
		}
		for ; negate > 0; negate-- {
			leaf = addNode(&q.nodes, queryNode{op: '!', right: leaf})
		}
		if pendingOR && conjunction != -1 {
			if disjunction == -1 {
				disjunction = conjunction
			} else {
				disjunction = joinNode(&q.nodes, '|', disjunction, conjunction, 0)
			}
			conjunction = -1
			pendingOR = false
		}
		if conjunction == -1 {
			conjunction = leaf
		} else {
			conjunction = joinNode(&q.nodes, '&', conjunction, leaf, 0)
		}
	}
	q.root = conjunction
	if disjunction != -1 && conjunction != -1 {
		q.root = joinNode(&q.nodes, '|', disjunction, conjunction, 0)
	}
	return q, parsed, nil
}
