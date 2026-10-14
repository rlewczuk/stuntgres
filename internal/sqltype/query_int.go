package sqltype

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// QueryInt is an extension-allocated base identity, not a builtin OID or TEXT.
func QueryInt(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid query_int descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "QUERY_INT", queryInt: true}, nil
}

func (d *Descriptor) IsQueryInt() bool { return d != nil && d.queryInt }

type queryIntNode struct {
	op          byte
	value       int32
	left, right int
}
type queryIntPayload struct {
	nodes []queryIntNode
	root  int
}

func queryIntSyntax() error { return failure("42601", "syntax error") }

// REL_18_0 tokenizes at most fifteen sign/digit characters and uses strtol
// base zero without an end-pointer check. Partial octal and a lone minus are
// therefore legal; this is intentionally not Go's stricter ParseInt grammar.
func queryIntNumber(text string) (int32, error) {
	negative := text[0] == '-'
	if negative {
		text = text[1:]
	}
	base := int64(10)
	if len(text) > 0 && text[0] == '0' {
		base = 8
	}
	limit := int64(2147483647)
	if negative {
		limit++
	}
	var value int64
	for i := range len(text) {
		digit := int64(text[i] - '0')
		if digit >= base {
			break
		}
		if value > (limit-digit)/base {
			return 0, queryIntSyntax()
		}
		value = value*base + digit
	}
	if negative {
		value = -value
	}
	return int32(value), nil
}

func (d *Descriptor) queryIntInput(ctx context.Context, text string) (Value, error) {
	var opBuffer [32]byte
	var valueBuffer [32]int
	var frameBuffer [16]int
	operators, values, frames := opBuffer[:0], valueBuffer[:0], frameBuffer[:0]
	nodes := make([]queryIntNode, 0, min(len(text), 16))
	frameStart := 0
	expecting := true
	precedence := func(op byte) int {
		switch op {
		case '!':
			return 3
		case '&':
			return 2
		case '|':
			return 1
		}
		return 0
	}
	reduce := func() error {
		op := operators[len(operators)-1]
		operators = operators[:len(operators)-1]
		if len(values) == 0 {
			return queryIntSyntax()
		}
		right := values[len(values)-1]
		values = values[:len(values)-1]
		node := queryIntNode{op: op, right: right}
		if op != '!' {
			if len(values) == 0 {
				return queryIntSyntax()
			}
			node.left = values[len(values)-1]
			values = values[:len(values)-1]
		}
		nodes = append(nodes, node)
		values = append(values, len(nodes)-1)
		return nil
	}
	for pos, steps := 0, 0; pos < len(text); steps++ {
		if steps&255 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		ch := text[pos]
		if ch == ' ' {
			pos++
			continue
		}
		if expecting {
			switch {
			case ch == '!':
				if len(operators)-frameStart == 16 {
					return Value{}, failure("54001", "statement too complex")
				}
				operators = append(operators, ch)
				pos++
				continue
			case ch == '(':
				frames = append(frames, frameStart)
				operators = append(operators, ch)
				frameStart = len(operators)
				pos++
				continue
			case ch == '-' || ch >= '0' && ch <= '9':
				start := pos
				pos++
				for pos < len(text) && text[pos] >= '0' && text[pos] <= '9' {
					pos++
					if pos-start > 15 {
						return Value{}, queryIntSyntax()
					}
				}
				number, err := queryIntNumber(text[start:pos])
				if err != nil {
					return Value{}, err
				}
				nodes = append(nodes, queryIntNode{op: 'v', value: number})
				values = append(values, len(nodes)-1)
				expecting = false
			default:
				return Value{}, queryIntSyntax()
			}
		} else if ch == ')' {
			if len(frames) == 0 {
				return Value{}, queryIntSyntax()
			}
			for len(operators) > frameStart {
				if err := reduce(); err != nil {
					return Value{}, err
				}
			}
			operators = operators[:len(operators)-1]
			frameStart = frames[len(frames)-1]
			frames = frames[:len(frames)-1]
			pos++
		} else if ch == '&' || ch == '|' {
			for len(operators) > frameStart && precedence(operators[len(operators)-1]) >= precedence(ch) {
				if err := reduce(); err != nil {
					return Value{}, err
				}
			}
			operators = append(operators, ch)
			pos++
			expecting = true
			continue
		} else {
			return Value{}, queryIntSyntax()
		}
		// makepol eagerly consumes NOT and AND after an operand/subexpression;
		// OR remains until the next OR or the end of its parenthesis frame.
		for len(operators) > frameStart && operators[len(operators)-1] != '|' {
			if err := reduce(); err != nil {
				return Value{}, err
			}
		}
	}
	if expecting || len(frames) != 0 {
		return Value{}, queryIntSyntax()
	}
	for len(operators) > 0 {
		if err := reduce(); err != nil {
			return Value{}, err
		}
	}
	if len(values) != 1 {
		return Value{}, queryIntSyntax()
	}
	return Value{typ: d, queryInt: &queryIntPayload{nodes: nodes, root: values[0]}}, nil
}

func (v Value) validQueryInt() bool {
	return v.queryInt != nil && len(v.queryInt.nodes) > 0 && v.queryInt.root == len(v.queryInt.nodes)-1 && v.queryInt.nodes[v.queryInt.root].valid(v.queryInt.root)
}

func (n queryIntNode) valid(index int) bool {
	switch n.op {
	case 'v':
		return true
	case '!':
		return n.right >= 0 && n.right < index
	case '&', '|':
		return n.left >= 0 && n.left < index && n.right >= 0 && n.right < index
	}
	return false
}

func (d *Descriptor) queryIntOutput(ctx context.Context, v Value) (string, error) {
	if v.typ != d || v.null || !v.validQueryInt() {
		return "", fmt.Errorf("invalid query_int payload")
	}
	type visit struct {
		node  int
		stage byte
		wrap  bool
	}
	var buffer [32]visit
	stack := append(buffer[:0], visit{node: v.queryInt.root})
	var out strings.Builder
	var integer [12]byte
	for steps := 0; len(stack) > 0; steps++ {
		if steps&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		at := len(stack) - 1
		current := &stack[at]
		node := v.queryInt.nodes[current.node]
		if !node.valid(current.node) {
			return "", fmt.Errorf("invalid query_int node")
		}
		if current.stage == 0 {
			if current.wrap {
				out.WriteString("( ")
			}
			if node.op == 'v' {
				out.Write(strconv.AppendInt(integer[:0], int64(node.value), 10))
				current.stage = 3
			} else if node.op == '!' {
				out.WriteByte('!')
				current.stage = 3
				stack = append(stack, visit{node: node.right, wrap: v.queryInt.nodes[node.right].op != 'v'})
			} else {
				current.stage = 1
				stack = append(stack, visit{node: node.left, wrap: v.queryInt.nodes[node.left].op == '|'})
			}
		} else if current.stage == 1 {
			out.WriteByte(' ')
			out.WriteByte(node.op)
			out.WriteByte(' ')
			current.stage = 3
			stack = append(stack, visit{node: node.right, wrap: v.queryInt.nodes[node.right].op == '|'})
		} else {
			if current.wrap {
				out.WriteString(" )")
			}
			stack = stack[:at]
		}
	}
	return out.String(), nil
}

// RemapQueryInt requires an explicit catalog-import identity mapping. Ordinary
// Retag cannot authorize changing the installed type's OID.
func (v Value) RemapQueryInt(target *Descriptor) (Value, error) {
	if !v.typ.IsQueryInt() || !target.IsQueryInt() || !v.null && !v.validQueryInt() {
		return Value{}, fmt.Errorf("incompatible imported query_int")
	}
	v.typ = target
	return v, nil
}
