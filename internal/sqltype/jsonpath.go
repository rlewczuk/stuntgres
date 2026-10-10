package sqltype

// Grammar and canonical rendering follow PostgreSQL REL_18_0 jsonpath_gram.y
// and jsonpath.c. Copyright (c) 2019-2025, PostgreSQL Global Development Group.
// PostgreSQL is distributed under the PostgreSQL License.
import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
)

// A payload and everything reachable from it are frozen before publication.
// No node or mutable slice is exposed by the datum API. Output uses the cached
// text, while future consumers can walk the actual syntax rather than reparse.
type jsonpathPayload struct {
	strict bool
	root   *jsonpathNode
}
type jsonpathNode struct {
	kind              string
	text              string
	number            Value
	left, right, next *jsonpathNode
	args              []jsonpathIndex
	first, last       int32
	flags             uint8
}
type jsonpathIndex struct{ from, to *jsonpathNode }

func jsonpathDetail(code, detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: "invalid input syntax for type jsonpath", Detail: detail}
}
func jsonpathUnicodeZero() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P05", Message: "unsupported Unicode escape sequence", Detail: `\u0000 cannot be converted to text.`}
}
func (d *Descriptor) jsonpathInput(ctx context.Context, text string) (Value, error) {
	p, err := parseJSONPath(ctx, text)
	if err != nil {
		return Value{}, err
	}
	output, err := printJSONPath(ctx, p)
	if err != nil {
		return Value{}, err
	}
	return Value{typ: d, text: output, jsonpath: p}, nil
}
func jsonpathPriority(n *jsonpathNode) int {
	switch n.kind {
	case "||":
		return 0
	case "&&":
		return 1
	case "==", "!=", "<", ">", "<=", ">=", "starts with":
		return 2
	case "+", "-":
		return 3
	case "*", "/", "%":
		return 4
	case "unary+", "unary-":
		return 5
	default:
		return 6
	}
}
func jsonpathPredicate(n *jsonpathNode) bool {
	// Accessors turn a parenthesized predicate into an expression.
	if n.next != nil {
		return false
	}
	switch n.kind {
	case "||", "&&", "==", "!=", "<", ">", "<=", ">=", "starts with", "like_regex", "!", "is unknown", "exists":
		return true
	}
	return false
}
func validateJSONPathScope(ctx context.Context, root *jsonpathNode) error {
	type entry struct {
		n         *jsonpathNode
		filters   int
		subscript bool
	}
	stack := []entry{{root, 0, false}}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := e.n
		if n == nil {
			continue
		}
		if n.kind == "@" && e.filters == 0 {
			return failure("42601", "@ is not allowed in root expressions")
		}
		if n.kind == "last" && !e.subscript {
			return failure("42601", "LAST is allowed only in array subscripts")
		}
		if n.next != nil {
			stack = append(stack, entry{n.next, e.filters, e.subscript})
		}
		filters := e.filters
		if n.kind == "filter" {
			filters++
		}
		if n.right != nil {
			stack = append(stack, entry{n.right, filters, e.subscript})
		}
		if n.left != nil {
			stack = append(stack, entry{n.left, filters, e.subscript})
		}
		for i := len(n.args) - 1; i >= 0; i-- {
			if n.args[i].to != nil {
				stack = append(stack, entry{n.args[i].to, filters, true})
			}
			stack = append(stack, entry{n.args[i].from, filters, true})
		}
	}
	return nil
}

// Rendering is an explicit work stack: deep trees do not consume the Go call
// stack and cancellation is checked throughout. Parentheses deliberately match
// PostgreSQL's printer, including equal-precedence left children.
func printJSONPath(ctx context.Context, p *jsonpathPayload) (string, error) {
	type item struct {
		n        *jsonpathNode
		text     string
		brackets bool
	}
	var out strings.Builder
	if p.strict {
		out.WriteString("strict ")
	}
	stack := []item{{n: p.root, brackets: true}}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if e.n == nil {
			out.WriteString(e.text)
			continue
		}
		n := e.n
		if n.next != nil {
			stack = append(stack, item{n: n.next, brackets: true})
		}
		pushText := func(s string) { stack = append(stack, item{text: s}) }
		pushNode := func(child *jsonpathNode, brackets bool) {
			if child != nil {
				stack = append(stack, item{n: child, brackets: brackets})
			}
		}
		switch n.kind {
		case "number":
			if n.next != nil {
				out.WriteByte('(')
			}
			out.WriteString(numericOutput(n.number))
			if n.next != nil {
				out.WriteByte(')')
			}
		case "string":
			if err := jsonpathQuote(ctx, &out, n.text); err != nil {
				return "", err
			}
		case "key":
			out.WriteByte('.')
			if err := jsonpathQuote(ctx, &out, n.text); err != nil {
				return "", err
			}
		case "variable":
			out.WriteByte('$')
			if err := jsonpathQuote(ctx, &out, n.text); err != nil {
				return "", err
			}
		case "$", "@", "last", "true", "false", "null":
			out.WriteString(n.kind)
		case "anykey":
			out.WriteString(".*")
		case "anyarray":
			out.WriteString("[*]")
		case "recursive":
			out.WriteString(".**")
			level := func(v int32) string {
				if v == -1 {
					return "last"
				}
				return strconv.FormatInt(int64(v), 10)
			}
			if n.first != 0 || n.last != -1 {
				out.WriteByte('{')
				out.WriteString(level(n.first))
				if n.first != n.last {
					out.WriteString(" to ")
					out.WriteString(level(n.last))
				}
				out.WriteByte('}')
			}
		case "index":
			out.WriteByte('[')
			pushText("]")
			for i := len(n.args) - 1; i >= 0; i-- {
				a := n.args[i]
				pushNode(a.to, false)
				if a.to != nil {
					pushText(" to ")
				}
				pushNode(a.from, false)
				if i > 0 {
					pushText(",")
				}
			}
		case "method":
			out.WriteByte('.')
			out.WriteString(n.text)
			out.WriteByte('(')
			pushText(")")
			pushNode(n.right, false)
			if n.right != nil {
				pushText(",")
			}
			pushNode(n.left, false)
		case "filter", "exists", "!", "is unknown":
			switch n.kind {
			case "filter":
				out.WriteString("?(")
			case "exists":
				out.WriteString("exists (")
			case "!":
				out.WriteString("!(")
			case "is unknown":
				out.WriteByte('(')
			}
			if n.kind == "is unknown" {
				pushText(") is unknown")
			} else {
				pushText(")")
			}
			pushNode(n.left, false)
		case "unary+", "unary-":
			if e.brackets {
				out.WriteByte('(')
				pushText(")")
			}
			out.WriteByte(n.kind[5])
			pushNode(n.left, jsonpathPriority(n.left) <= jsonpathPriority(n))
		case "like_regex":
			if e.brackets {
				out.WriteByte('(')
				pushText(")")
			}
			var suffix strings.Builder
			suffix.WriteString(" like_regex ")
			if err := jsonpathQuote(ctx, &suffix, n.text); err != nil {
				return "", err
			}
			if n.flags != 0 {
				suffix.WriteString(" flag \"")
				for i, c := range "ismxq" {
					if n.flags&(1<<i) != 0 {
						suffix.WriteRune(c)
					}
				}
				suffix.WriteByte('"')
			}
			pushText(suffix.String())
			pushNode(n.left, jsonpathPriority(n.left) <= jsonpathPriority(n))
		case "+", "-", "*", "/", "%", "==", "!=", "<", ">", "<=", ">=", "&&", "||", "starts with":
			if e.brackets {
				out.WriteByte('(')
				pushText(")")
			}
			pushNode(n.right, jsonpathPriority(n.right) <= jsonpathPriority(n))
			pushText(" " + n.kind + " ")
			pushNode(n.left, jsonpathPriority(n.left) <= jsonpathPriority(n))
		default:
			return "", fmt.Errorf("unknown JSONPath node %q", n.kind)
		}
	}
	return out.String(), nil
}
func jsonpathQuote(ctx context.Context, b *strings.Builder, s string) error {
	return writeJSONBString(ctx, b, s)
}
