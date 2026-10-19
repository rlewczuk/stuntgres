package fts

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type queryNode struct {
	op          byte
	word        string
	left, right int
	distance    uint16
	mask        byte
	prefix      bool
}

// Query preserves operand/operator association independently of canonical text.
type Query struct {
	nodes []queryNode
	root  int
	text  string
}

func (q *Query) String() string { return q.text }
func (q *Query) Empty() bool    { return len(q.nodes) == 0 }
func (q *Query) Valid() bool {
	return q != nil && (q.root == -1 && len(q.nodes) == 0 || len(q.nodes) > 0 && q.root == len(q.nodes)-1)
}

// InputText preserves every binary association for typed logical serialization.
// SQL quoting belongs to sqltype; ordinary output uses only cached String.
func (q *Query) InputText(ctx context.Context) (string, error) { return q.render(ctx, true) }
func (q *Query) Equal(ctx context.Context, other *Query) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !q.Valid() || !other.Valid() {
		return false, nil
	}
	if q == other {
		return true, nil
	}
	if q.root != other.root || len(q.nodes) != len(other.nodes) {
		return false, nil
	}
	for i, n := range q.nodes {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if n != other.nodes[i] {
			return false, nil
		}
	}
	return true, nil
}
func priority(op byte) int {
	switch op {
	case '!':
		return 4
	case '<':
		return 3
	case '&':
		return 2
	case '|':
		return 1
	}
	return 0
}
func queryTooBig(nodes, bytes uint64) bool {
	const max = uint64(1<<30 - 1)
	return bytes > max-8 || nodes > (max-8-bytes)/12
}

type pending struct {
	op       byte
	distance uint16
}

func ParseQuery(ctx context.Context, text string) (*Query, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l := lexer{ctx: ctx, text: text, query: true}
	var opBuffer [32]pending
	var valueBuffer [32]int
	var frameBuffer [16]int
	ops, values, frames := opBuffer[:0], valueBuffer[:0], frameBuffer[:0]
	var nodes []queryNode
	frameStart, operandBytes := 0, 0
	expecting := true
	reduce := func() error {
		p := ops[len(ops)-1]
		ops = ops[:len(ops)-1]
		if len(values) == 0 {
			return l.syntax()
		}
		right := values[len(values)-1]
		values = values[:len(values)-1]
		n := queryNode{op: p.op, right: right, distance: p.distance}
		if p.op != '!' {
			if len(values) == 0 {
				return l.syntax()
			}
			n.left = values[len(values)-1]
			values = values[:len(values)-1]
		}
		nodes = append(nodes, n)
		values = append(values, len(nodes)-1)
		return nil
	}
	push := func(p pending) error {
		for len(ops) > frameStart {
			top := ops[len(ops)-1]
			if priority(top.op) < priority(p.op) || (p.op == '!' && priority(top.op) == priority(p.op)) {
				break
			}
			if err := reduce(); err != nil {
				return err
			}
		}
		if len(ops)-frameStart == 32 {
			return failure("XX000", "tsquery stack too small")
		}
		ops = append(ops, p)
		return nil
	}
	for {
		if err := l.skip(); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if l.pos == len(text) {
			break
		}
		c := text[l.pos]
		if expecting {
			switch c {
			case '!':
				if err := push(pending{op: '!'}); err != nil {
					return nil, err
				}
				l.pos++
				continue
			case '(':
				frames = append(frames, frameStart)
				ops = append(ops, pending{op: '('})
				frameStart = len(ops)
				l.pos++
				continue
			}
			word, err := l.word()
			if err != nil {
				return nil, err
			}
			if len(word) > maxWordBytes {
				return nil, failure("54000", "word is too long in tsquery: \""+text+"\"")
			}
			if operandBytes >= maxStringOffset {
				return nil, failure("54000", "value is too big in tsquery: \""+text+"\"")
			}
			operandBytes += len(word) + 1
			n := queryNode{op: 'v', word: word}
			if l.pos < len(text) && text[l.pos] == ':' {
				l.pos++
				for l.pos < len(text) {
					if err := l.check(); err != nil {
						return nil, err
					}
					c = text[l.pos]
					mask := byte(0)
					switch c {
					case 'a', 'A':
						mask = 8
					case 'b', 'B':
						mask = 4
					case 'c', 'C':
						mask = 2
					case 'd', 'D':
						mask = 1
					case '*':
						n.prefix = true
					default:
						goto modifiersDone
					}
					n.mask |= mask
					l.pos++
				}
			}
		modifiersDone:
			nodes = append(nodes, n)
			values = append(values, len(nodes)-1)
			expecting = false
		} else {
			switch c {
			case ')':
				if len(frames) == 0 {
					return nil, l.syntax()
				}
				for len(ops) > frameStart {
					if err := reduce(); err != nil {
						return nil, err
					}
				}
				ops = ops[:len(ops)-1]
				frameStart = frames[len(frames)-1]
				frames = frames[:len(frames)-1]
				l.pos++
			case '&', '|':
				if err := push(pending{op: c}); err != nil {
					return nil, err
				}
				l.pos++
				expecting = true
			case '<':
				l.pos++
				distance := uint64(1)
				if l.pos < len(text) && text[l.pos] == '-' {
					l.pos++
				} else {
					start := l.pos
					distance = 0
					for l.pos < len(text) && text[l.pos] >= '0' && text[l.pos] <= '9' {
						if err := l.check(); err != nil {
							return nil, err
						}
						distance = distance*10 + uint64(text[l.pos]-'0')
						l.pos++
						if distance > 16384 {
							return nil, failure("22023", "distance in phrase operator must be an integer value between zero and 16384 inclusive")
						}
					}
					if start == l.pos {
						return nil, l.syntax()
					}
				}
				if l.pos == len(text) || text[l.pos] != '>' {
					return nil, l.syntax()
				}
				l.pos++
				if err := push(pending{op: '<', distance: uint16(distance)}); err != nil {
					return nil, err
				}
				expecting = true
			default:
				return nil, l.syntax()
			}
		}
		if queryTooBig(uint64(len(nodes)), uint64(operandBytes)) {
			return nil, failure("54000", "tsquery is too large")
		}
	}
	if expecting {
		if len(nodes) == 0 && len(ops) == 0 {
			return &Query{root: -1}, nil
		}
		return nil, failure("42601", "no operand in tsquery: \""+text+"\"")
	}
	if len(frames) != 0 {
		return nil, l.syntax()
	}
	for len(ops) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := reduce(); err != nil {
			return nil, err
		}
	}
	if len(values) != 1 {
		return nil, l.syntax()
	}
	if queryTooBig(uint64(len(nodes)), uint64(operandBytes)) {
		return nil, failure("54000", "tsquery is too large")
	}
	q := &Query{nodes: nodes, root: values[0]}
	rendered, err := q.render(ctx, false)
	if err != nil {
		return nil, err
	}
	q.text = rendered
	return q, nil
}

func (q *Query) render(ctx context.Context, lossless bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !q.Valid() {
		return "", fmt.Errorf("invalid tsquery payload")
	}
	if len(q.nodes) == 0 {
		return "", nil
	}
	type visit struct {
		node, parent int
		rightPhrase  bool
		stage        byte
		wrap         bool
	}
	var buffer [32]visit
	stack := append(buffer[:0], visit{node: q.root})
	var out strings.Builder
	var number [20]byte
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		v := &stack[len(stack)-1]
		n := q.nodes[v.node]
		if n.op == 'v' {
			quote(&out, n.word)
			if n.mask != 0 || n.prefix {
				out.WriteByte(':')
				if n.prefix {
					out.WriteByte('*')
				}
				for i := range 4 {
					if n.mask&(8>>i) != 0 {
						out.WriteByte("ABCD"[i])
					}
				}
			}
			stack = stack[:len(stack)-1]
			continue
		}
		if v.stage == 0 {
			v.wrap = lossless && n.op != '!' || priority(n.op) < v.parent || (n.op == '<' && v.rightPhrase)
			if v.wrap {
				out.WriteString("( ")
			}
			v.stage = 1
			if n.op == '!' {
				out.WriteByte('!')
				stack = append(stack, visit{node: n.right, parent: priority(n.op)})
			} else {
				stack = append(stack, visit{node: n.left, parent: priority(n.op)})
			}
			continue
		}
		if v.stage == 1 && n.op != '!' {
			v.stage = 2
			switch n.op {
			case '&':
				out.WriteString(" & ")
			case '|':
				out.WriteString(" | ")
			case '<':
				if n.distance == 1 {
					out.WriteString(" <-> ")
				} else {
					out.WriteString(" <")
					out.Write(strconv.AppendUint(number[:0], uint64(n.distance), 10))
					out.WriteString("> ")
				}
			}
			stack = append(stack, visit{node: n.right, parent: priority(n.op), rightPhrase: n.op == '<'})
			continue
		}
		if v.wrap {
			out.WriteString(" )")
		}
		stack = stack[:len(stack)-1]
	}
	return out.String(), nil
}
