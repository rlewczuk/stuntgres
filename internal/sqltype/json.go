package sqltype

import (
	"context"
	"fmt"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

// JSONText exposes the retained source, not a decoded document or SQL equality.
func (v Value) JSONText() (string, error) {
	if v.typ == nil || v.typ.BaseType().id != JSON || v.null {
		return "", fmt.Errorf("invalid JSON value")
	}
	return v.text, nil
}

type jsonState uint8

const (
	jsonValue jsonState = iota
	jsonEnd
	jsonArrayFirst
	jsonArrayValue
	jsonArrayNext
	jsonObjectFirst
	jsonObjectKey
	jsonObjectColon
	jsonObjectValue
	jsonObjectNext
)

type jsonLexer struct {
	ctx                                 context.Context
	text                                string
	offset, start, end, line, lineStart int
	kind                                byte
}

func (d *Descriptor) jsonInput(ctx context.Context, text string) (Value, error) {
	l := jsonLexer{ctx: ctx, text: text, line: 1}
	var local [32]jsonState
	stack := local[:1]
	stack[0] = jsonValue
	for {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if err := l.next(); err != nil {
			return Value{}, err
		}
		state := stack[len(stack)-1]
		if state == jsonEnd && l.kind == 0 {
			return Value{typ: d, text: text}, nil
		}
		if l.kind == 0 {
			return Value{}, l.fail("The input string ended unexpectedly.")
		}
		switch state {
		case jsonEnd:
			return Value{}, l.expected("end of input")
		case jsonArrayFirst:
			if l.kind == ']' {
				stack = stack[:len(stack)-1]
				continue
			}
		case jsonArrayNext:
			if l.kind == ']' {
				stack = stack[:len(stack)-1]
				continue
			}
			if l.kind != ',' {
				return Value{}, l.expected(`"," or "]"`)
			}
			stack[len(stack)-1] = jsonArrayValue
			continue
		case jsonObjectFirst, jsonObjectKey:
			if state == jsonObjectFirst && l.kind == '}' {
				stack = stack[:len(stack)-1]
				continue
			}
			if l.kind != 's' {
				if state == jsonObjectFirst {
					return Value{}, l.expected(`string or "}"`)
				}
				return Value{}, l.expected("string")
			}
			stack[len(stack)-1] = jsonObjectColon
			continue
		case jsonObjectColon:
			if l.kind != ':' {
				return Value{}, l.expected(`":"`)
			}
			stack[len(stack)-1] = jsonObjectValue
			continue
		case jsonObjectNext:
			if l.kind == '}' {
				stack = stack[:len(stack)-1]
				continue
			}
			if l.kind != ',' {
				return Value{}, l.expected(`"," or "}"`)
			}
			stack[len(stack)-1] = jsonObjectKey
			continue
		}
		// Consume a value and record the parent's next expectation before descending.
		switch state {
		case jsonValue:
			stack[len(stack)-1] = jsonEnd
		case jsonArrayFirst, jsonArrayValue:
			stack[len(stack)-1] = jsonArrayNext
		case jsonObjectValue:
			stack[len(stack)-1] = jsonObjectNext
		}
		switch l.kind {
		case '{':
			stack = append(stack, jsonObjectFirst)
		case '[':
			stack = append(stack, jsonArrayFirst)
		case 's', 'n', 't', 'f', 'z':
		default:
			return Value{}, l.expected("JSON value")
		}
	}
}

func (l *jsonLexer) expected(expectation string) error {
	return l.fail(fmt.Sprintf("Expected %s, but found \"%s\".", expectation, l.text[l.start:l.end]))
}
func (l *jsonLexer) invalid() error {
	return l.fail(fmt.Sprintf("Token \"%s\" is invalid.", l.text[l.start:l.end]))
}
func (l *jsonLexer) fail(detail string) error {
	start := l.lineStart
	for l.end-start >= 50 {
		_, width := utf8.DecodeRuneInString(l.text[start:l.end])
		start += width
	}
	if start-l.lineStart <= 3 {
		start = l.lineStart
	}
	prefix, suffix := "", ""
	if start != l.lineStart {
		prefix = "..."
	}
	if l.kind != 0 && l.end < len(l.text) && l.text[l.end] != '\n' && l.text[l.end] != '\r' {
		suffix = "..."
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type json", Detail: detail,
		Context: fmt.Sprintf("JSON data, line %d: %s%s%s", l.line, prefix, l.text[start:l.end], suffix)}
}
func jsonWord(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c >= 128
}
func jsonDigit(c byte) bool { return c >= '0' && c <= '9' }
func jsonHex(c byte) bool   { return jsonDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }
func (l *jsonLexer) canceled() error {
	if l.offset&4095 == 0 {
		return l.ctx.Err()
	}
	return nil
}
func (l *jsonLexer) next() error {
	for l.offset < len(l.text) {
		c := l.text[l.offset]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		l.offset++
		if c == '\n' {
			l.line++
			l.lineStart = l.offset
		}
		if err := l.canceled(); err != nil {
			return err
		}
	}
	l.start, l.end = l.offset, l.offset
	l.kind = 0
	if l.offset == len(l.text) {
		return nil
	}
	c := l.text[l.offset]
	l.kind = c
	l.offset++
	l.end = l.offset
	switch c {
	case '{', '}', '[', ']', ',', ':':
		return nil
	case '"':
		l.kind = 's'
		for l.offset < len(l.text) {
			c = l.text[l.offset]
			if c < 32 {
				l.end = l.offset
				return l.fail(fmt.Sprintf("Character with value 0x%02x must be escaped.", c))
			}
			l.offset++
			l.end = l.offset
			if c == '"' {
				return nil
			}
			if c == '\\' {
				if l.offset == len(l.text) {
					return l.invalid()
				}
				c = l.text[l.offset]
				l.offset++
				l.end = l.offset
				switch c {
				case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				case 'u':
					for range 4 {
						if l.offset == len(l.text) {
							return l.invalid()
						}
						c = l.text[l.offset]
						l.offset++
						l.end = l.offset
						if !jsonHex(c) {
							if c >= utf8.RuneSelf {
								_, width := utf8.DecodeRuneInString(l.text[l.offset-1:])
								l.end = l.offset - 1 + width
							}
							return l.fail(`"\u" must be followed by four hexadecimal digits.`)
						}
					}
				default:
					start := l.offset - 1
					if c >= utf8.RuneSelf {
						_, width := utf8.DecodeRuneInString(l.text[start:])
						l.end = start + width
					}
					return l.fail(fmt.Sprintf(`Escape sequence "\%s" is invalid.`, l.text[start:l.end]))
				}
			}
			if err := l.canceled(); err != nil {
				return err
			}
		}
		return l.invalid()
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		l.kind = 'n'
		// Scan without converting: magnitude and exponent have no storage limit.
		valid := true
		i := l.start
		if l.text[i] == '-' {
			i++
		}
		if i == len(l.text) || !jsonDigit(l.text[i]) {
			valid = false
		} else if l.text[i] == '0' {
			i++
		} else {
			for i < len(l.text) && jsonDigit(l.text[i]) {
				i++
				if i&4095 == 0 {
					if err := l.ctx.Err(); err != nil {
						return err
					}
				}
			}
		}
		if i < len(l.text) && l.text[i] == '.' {
			i++
			first := i
			for i < len(l.text) && jsonDigit(l.text[i]) {
				i++
				if i&4095 == 0 {
					if err := l.ctx.Err(); err != nil {
						return err
					}
				}
			}
			valid = valid && i > first
		}
		if i < len(l.text) && (l.text[i] == 'e' || l.text[i] == 'E') {
			i++
			if i < len(l.text) && (l.text[i] == '+' || l.text[i] == '-') {
				i++
			}
			first := i
			for i < len(l.text) && jsonDigit(l.text[i]) {
				i++
				if i&4095 == 0 {
					if err := l.ctx.Err(); err != nil {
						return err
					}
				}
			}
			valid = valid && i > first
		}
		first := i
		for i < len(l.text) && jsonWord(l.text[i]) {
			i++
			if i&4095 == 0 {
				if err := l.ctx.Err(); err != nil {
					return err
				}
			}
		}
		l.offset, l.end = i, i
		if !valid || i != first {
			return l.invalid()
		}
		return nil
	default:
		if jsonWord(c) {
			for l.offset < len(l.text) && jsonWord(l.text[l.offset]) {
				l.offset++
				if err := l.canceled(); err != nil {
					return err
				}
			}
			l.end = l.offset
			switch l.text[l.start:l.end] {
			case "true":
				l.kind = 't'
				return nil
			case "false":
				l.kind = 'f'
				return nil
			case "null":
				l.kind = 'z'
				return nil
			}
		}
		return l.invalid()
	}
}
