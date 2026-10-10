package sqltype

// Productions follow PostgreSQL REL_18_0 jsonpath_gram.y.
// Copyright (c) 2019-2025, PostgreSQL Global Development Group.
// Distributed under the PostgreSQL License.
import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type jsonpathParser struct {
	scanner jsonpathScanner
	tok     jsonpathToken
	err     error
}

func parseJSONPath(ctx context.Context, text string) (*jsonpathPayload, error) {
	p := jsonpathParser{scanner: jsonpathScanner{ctx: ctx, text: text}}
	p.advance()
	if p.err != nil {
		return nil, p.err
	}
	if p.tok.kind == "eof" {
		return nil, failure("22P02", fmt.Sprintf("invalid input syntax for type jsonpath: \"%s\"", text))
	}
	payload := &jsonpathPayload{strict: p.tok.kind == "strict"}
	if p.tok.kind == "strict" || p.tok.kind == "lax" {
		p.advance()
	}
	payload.root = p.expression(0)
	if p.err == nil && p.tok.kind != "eof" {
		p.syntax()
	}
	if p.err != nil {
		return nil, p.err
	}
	if err := validateJSONPathScope(ctx, payload.root); err != nil {
		return nil, err
	}
	return payload, nil
}
func (p *jsonpathParser) advance() {
	if p.err == nil {
		p.tok, p.err = p.scanner.next()
	}
}
func (p *jsonpathParser) syntax() {
	if p.err == nil {
		p.err = jsonpathSyntax("syntax error", p.tok.raw)
	}
}
func (p *jsonpathParser) take(kind string) bool {
	if p.err != nil {
		return false
	}
	if p.tok.kind != kind {
		p.syntax()
		return false
	}
	p.advance()
	return true
}
func jsonpathBinaryPriority(kind string) int {
	switch kind {
	case "||":
		return 1
	case "&&":
		return 2
	case "==", "!=", "<", ">", "<=", ">=", "starts", "like_regex":
		return 3
	case "+", "-":
		return 4
	case "*", "/", "%":
		return 5
	}
	return -1
}
func (p *jsonpathParser) expression(min int) *jsonpathNode {
	if p.err != nil {
		return nil
	}
	var n *jsonpathNode
	switch p.tok.kind {
	case "+", "-":
		sign := p.tok.kind
		p.advance()
		child := p.expression(6)
		if p.err != nil {
			return nil
		}
		if jsonpathPredicate(child) {
			p.syntax()
			return nil
		}
		if child.kind == "number" && child.next == nil {
			if sign == "-" && child.number.text != "" {
				child.number.negative = !child.number.negative
			}
			n = child
		} else {
			n = &jsonpathNode{kind: "unary" + sign, left: child}
		}
	case "!":
		p.advance()
		child := p.delimitedPredicate()
		n = &jsonpathNode{kind: "!", left: child}
	case "exists":
		p.advance()
		if !p.take("(") {
			return nil
		}
		child := p.expression(0)
		if p.err != nil {
			return nil
		}
		if jsonpathPredicate(child) {
			p.syntax()
			return nil
		}
		if !p.take(")") {
			return nil
		}
		n = &jsonpathNode{kind: "exists", left: child}
	case "(":
		p.advance()
		n = p.expression(0)
		if !p.take(")") {
			return nil
		}
		if p.tok.kind == "is" {
			if !jsonpathPredicate(n) {
				p.syntax()
				return nil
			}
			p.advance()
			if !p.take("unknown") {
				return nil
			}
			n = &jsonpathNode{kind: "is unknown", left: n}
		} else {
			n = p.accessors(n)
		}
	case "int", "numeric":
		n = p.number()
		n = p.accessors(n)
	case "quoted", "variable":
		kind := p.tok.kind
		if kind == "quoted" {
			kind = "string"
		}
		n = &jsonpathNode{kind: kind, text: p.tok.text}
		p.advance()
		n = p.accessors(n)
	case "$", "@", "last", "true", "false", "null":
		n = &jsonpathNode{kind: p.tok.kind}
		p.advance()
		n = p.accessors(n)
	default:
		p.syntax()
		return nil
	}
	for p.err == nil {
		kind := p.tok.kind
		priority := jsonpathBinaryPriority(kind)
		if priority < min || priority < 0 {
			break
		}
		predicate := jsonpathPredicate(n)
		if priority <= 2 && !predicate || priority >= 3 && predicate {
			p.syntax()
			return nil
		}
		p.advance()
		switch kind {
		case "starts":
			if !p.take("with") {
				return nil
			}
			if p.tok.kind != "quoted" && p.tok.kind != "variable" {
				p.syntax()
				return nil
			}
			kind := p.tok.kind
			if kind == "quoted" {
				kind = "string"
			}
			right := &jsonpathNode{kind: kind, text: p.tok.text}
			p.advance()
			n = &jsonpathNode{kind: "starts with", left: n, right: right}
		case "like_regex":
			if p.tok.kind != "quoted" {
				p.syntax()
				return nil
			}
			pattern := p.tok.text
			p.advance()
			flags := ""
			if p.tok.kind == "flag" {
				p.advance()
				if p.tok.kind != "quoted" {
					p.syntax()
					return nil
				}
				flags = p.tok.text
				p.advance()
			}
			bits, err := validateJSONPathRegex(p.scanner.ctx, pattern, flags)
			if err != nil {
				p.err = err
				return nil
			}
			n = &jsonpathNode{kind: "like_regex", left: n, text: pattern, flags: bits}
		default:
			right := p.expression(priority + 1)
			if p.err != nil {
				return nil
			}
			if priority <= 2 && !jsonpathPredicate(right) || priority >= 3 && jsonpathPredicate(right) {
				p.syntax()
				return nil
			}
			n = &jsonpathNode{kind: kind, left: n, right: right}
		}
	}
	return n
}
func (p *jsonpathParser) delimitedPredicate() *jsonpathNode {
	if p.tok.kind == "exists" {
		// EXISTS is itself a delimited predicate. Its argument is an expression.
		p.advance()
		if !p.take("(") {
			return nil
		}
		n := p.expression(0)
		if p.err != nil {
			return nil
		}
		if jsonpathPredicate(n) {
			p.syntax()
			return nil
		}
		if !p.take(")") {
			return nil
		}
		return &jsonpathNode{kind: "exists", left: n}
	}
	if !p.take("(") {
		return nil
	}
	n := p.expression(0)
	if p.err != nil {
		return nil
	}
	if !jsonpathPredicate(n) {
		p.syntax()
		return nil
	}
	if !p.take(")") {
		return nil
	}
	return n
}
func (p *jsonpathParser) number() *jsonpathNode {
	token := p.tok
	v, err := parseNumeric(token.text)
	if err == nil {
		err = validateNumericFormat(v)
	}
	if err != nil {
		p.err = err
		return nil
	}
	p.advance()
	return &jsonpathNode{kind: "number", number: v}
}
func (p *jsonpathParser) accessors(n *jsonpathNode) *jsonpathNode {
	if p.err != nil {
		return nil
	}
	tail := n
	for tail.next != nil {
		tail = tail.next
	}
	for p.err == nil {
		var a *jsonpathNode
		switch p.tok.kind {
		case ".":
			p.advance()
			switch p.tok.kind {
			case "*":
				a = &jsonpathNode{kind: "anykey"}
				p.advance()
			case "**":
				p.advance()
				a = &jsonpathNode{kind: "recursive", first: 0, last: -1}
				if p.tok.kind == "{" {
					p.advance()
					a.first = p.level()
					a.last = a.first
					if p.tok.kind == "to" {
						p.advance()
						a.last = p.level()
					}
					p.take("}")
				}
			default:
				tok := p.tok
				if tok.kind != "quoted" && !jsonpathWord(tok.kind) {
					p.syntax()
					return nil
				}
				p.advance()
				if p.tok.kind == "(" && jsonpathMethod(tok.kind) {
					a = p.method(tok.kind)
				} else {
					a = &jsonpathNode{kind: "key", text: tok.text}
				}
			}
		case "[":
			p.advance()
			if p.tok.kind == "*" {
				p.advance()
				p.take("]")
				a = &jsonpathNode{kind: "anyarray"}
			} else {
				a = &jsonpathNode{kind: "index"}
				for p.err == nil {
					from := p.expression(0)
					if p.err != nil {
						return nil
					}
					if jsonpathPredicate(from) {
						p.syntax()
						return nil
					}
					var to *jsonpathNode
					if p.tok.kind == "to" {
						p.advance()
						to = p.expression(0)
						if p.err != nil {
							return nil
						}
						if jsonpathPredicate(to) {
							p.syntax()
							return nil
						}
					}
					a.args = append(a.args, jsonpathIndex{from: from, to: to})
					if p.tok.kind != "," {
						break
					}
					p.advance()
				}
				p.take("]")
			}
		case "?":
			p.advance()
			if !p.take("(") {
				return nil
			}
			child := p.expression(0)
			if p.err != nil {
				return nil
			}
			if !jsonpathPredicate(child) {
				p.syntax()
				return nil
			}
			p.take(")")
			a = &jsonpathNode{kind: "filter", left: child}
		default:
			return n
		}
		if p.err != nil {
			return nil
		}
		tail.next = a
		tail = a
	}
	return n
}
func jsonpathWord(kind string) bool {
	switch kind {
	case "name", "is", "to", "abs", "lax", "date", "flag", "last", "size", "time", "type", "with", "floor", "bigint", "double", "exists", "number", "starts", "strict", "string", "boolean", "ceiling", "decimal", "integer", "time_tz", "unknown", "datetime", "keyvalue", "timestamp", "like_regex", "timestamp_tz", "true", "false", "null":
		return true
	}
	return false
}
func jsonpathMethod(kind string) bool {
	switch kind {
	case "type", "size", "abs", "floor", "double", "ceiling", "keyvalue", "bigint", "boolean", "date", "integer", "number", "string", "decimal", "datetime", "time", "time_tz", "timestamp", "timestamp_tz":
		return true
	}
	return false
}
func (p *jsonpathParser) level() int32 {
	if p.tok.kind == "last" {
		p.advance()
		return -1
	}
	if p.tok.kind != "int" {
		p.syntax()
		return 0
	}
	text := strings.ReplaceAll(p.tok.text, "_", "")
	base := 10
	if len(text) > 2 && text[0] == '0' && strings.ContainsRune("xXoObB", rune(text[1])) {
		base = 0
	}
	v, err := strconv.ParseInt(text, base, 32)
	if err != nil {
		p.err = failure("22003", fmt.Sprintf("value \"%s\" is out of range for type integer", p.tok.text))
		return 0
	}
	p.advance()
	return int32(v)
}
func (p *jsonpathParser) method(name string) *jsonpathNode {
	a := &jsonpathNode{kind: "method", text: name}
	p.take("(")
	switch name {
	case "datetime":
		if p.tok.kind != ")" {
			if p.tok.kind != "quoted" {
				p.syntax()
				return nil
			}
			a.left = &jsonpathNode{kind: "string", text: p.tok.text}
			p.advance()
		}
	case "time", "time_tz", "timestamp", "timestamp_tz":
		if p.tok.kind != ")" {
			if p.tok.kind != "int" {
				p.syntax()
				return nil
			}
			a.left = p.number()
		}
	case "decimal":
		var args []*jsonpathNode
		if p.tok.kind != ")" {
			for p.err == nil {
				sign := ""
				if p.tok.kind == "+" || p.tok.kind == "-" {
					sign = p.tok.kind
					p.advance()
				}
				if p.tok.kind != "int" {
					p.syntax()
					return nil
				}
				n := p.number()
				if p.err != nil {
					return nil
				}
				if sign == "-" && n.number.text != "" {
					n.number.negative = true
				}
				args = append(args, n)
				if p.tok.kind != "," {
					break
				}
				p.advance()
			}
		}
		if p.tok.kind != ")" {
			p.syntax()
			return nil
		}
		if len(args) > 2 {
			p.err = jsonpathDetail("42601", ".decimal() can only have an optional precision[,scale].")
			return nil
		}
		if len(args) > 0 {
			a.left = args[0]
		}
		if len(args) > 1 {
			a.right = args[1]
		}
	}
	p.take(")")
	return a
}
