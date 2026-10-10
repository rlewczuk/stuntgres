package sqltype

import (
	"context"
	"fmt"
	"strings"
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
	ctx                                                context.Context
	text                                               string
	offset, start, end, line, lineStart, checkedOffset int
	kind                                               byte
	jsonb                                              bool
	decoded                                            string
}

func (d *Descriptor) jsonInput(ctx context.Context, text string) (Value, error) {
	l := jsonLexer{ctx: ctx, text: text, line: 1, jsonb: d.id == JSONB}
	var local [32]jsonState
	stack := local[:1]
	stack[0] = jsonValue
	var frames [32]jsonbFrame
	builder := jsonbBuilder{}
	if l.jsonb {
		builder.frames = frames[:0]
	}
	var pendingNumber string
	for {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		if err := l.next(); err != nil {
			return Value{}, err
		}
		// PostgreSQL lexes one lookahead token before invoking numeric input.
		// Malformed lookahead (1e131072.) therefore wins over numeric overflow.
		if pendingNumber != "" {
			v, err := d.numericInput(pendingNumber, -1)
			if err != nil {
				return Value{}, err
			}
			builder.add(jsonbNode{kind: jsonbNumber, number: jsonbDecimal{digits: v.text, exponent: v.exponent, scale: v.scale, negative: v.negative}})
			pendingNumber = ""
		}
		state := stack[len(stack)-1]
		if state == jsonEnd && l.kind == 0 {
			if l.jsonb {
				payload := &jsonbPayload{root: builder.root}
				output, err := jsonbOutput(ctx, &payload.root)
				if err != nil {
					return Value{}, err
				}
				return Value{typ: d, text: output, jsonb: payload}, nil
			}
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
				if l.jsonb {
					if err := builder.close(ctx); err != nil {
						return Value{}, err
					}
				}
				stack = stack[:len(stack)-1]
				continue
			}
		case jsonArrayNext:
			if l.kind == ']' {
				if l.jsonb {
					if err := builder.close(ctx); err != nil {
						return Value{}, err
					}
				}
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
				if l.jsonb {
					if err := builder.close(ctx); err != nil {
						return Value{}, err
					}
				}
				stack = stack[:len(stack)-1]
				continue
			}
			if l.kind != 's' {
				if state == jsonObjectFirst {
					return Value{}, l.expected(`string or "}"`)
				}
				return Value{}, l.expected("string")
			}
			if l.jsonb {
				builder.frames[len(builder.frames)-1].key = l.decoded
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
				if l.jsonb {
					if err := builder.close(ctx); err != nil {
						return Value{}, err
					}
				}
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
			if l.jsonb {
				builder.open(jsonbObject)
			}
			stack = append(stack, jsonObjectFirst)
		case '[':
			if l.jsonb {
				builder.open(jsonbArray)
			}
			stack = append(stack, jsonArrayFirst)
		case 'n':
			if l.jsonb {
				pendingNumber = l.text[l.start:l.end]
			}
		case 's':
			if l.jsonb {
				builder.add(jsonbNode{kind: jsonbString, text: l.decoded})
			}
		case 't', 'f':
			if l.jsonb {
				builder.add(jsonbNode{kind: jsonbBoolean, boolean: l.kind == 't'})
			}
		case 'z':
			if l.jsonb {
				builder.add(jsonbNode{kind: jsonbNull})
			}
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
	if l.offset-l.checkedOffset >= 4096 {
		l.checkedOffset = l.offset
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
		return l.stringToken()
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

// Both modes use the same grammar and diagnostic endpoints. JSONB additionally
// decodes each escape while lexing, before later syntax or discarded duplicates.
func (l *jsonLexer) stringToken() error {
	var b strings.Builder
	segment := l.offset
	escaped := false
	var high rune
	for l.offset < len(l.text) {
		// Check consumed bytes, not an exact offset: escapes advance by more
		// than one byte, and a high surrogate continues before normal decoding.
		// Loop-entry polling also precedes the closing quote's early return.
		if err := l.canceled(); err != nil {
			return err
		}
		c := l.text[l.offset]
		if c < 32 {
			l.end = l.offset
			return l.fail(fmt.Sprintf("Character with value 0x%02x must be escaped.", c))
		}
		l.offset++
		l.end = l.offset
		if c != '\\' {
			if l.jsonb && high != 0 {
				return l.fail("Unicode low surrogate must follow a high surrogate.")
			}
			if c == '"' {
				if l.jsonb {
					if escaped {
						b.WriteString(l.text[segment : l.offset-1])
						l.decoded = b.String()
					} else {
						l.decoded = l.text[l.start+1 : l.offset-1]
					}
				}
				return nil
			}
		} else {
			if l.jsonb {
				b.WriteString(l.text[segment : l.offset-1])
				escaped = true
			}
			if l.offset == len(l.text) {
				return l.invalid()
			}
			c = l.text[l.offset]
			l.offset++
			l.end = l.offset
			var decoded rune
			switch c {
			case '"', '\\', '/':
				decoded = rune(c)
			case 'b':
				decoded = '\b'
			case 'f':
				decoded = '\f'
			case 'n':
				decoded = '\n'
			case 'r':
				decoded = '\r'
			case 't':
				decoded = '\t'
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
					decoded <<= 4
					switch {
					case c <= '9':
						decoded += rune(c - '0')
					case c <= 'F':
						decoded += rune(c - 'A' + 10)
					default:
						decoded += rune(c - 'a' + 10)
					}
				}
				if l.jsonb {
					if decoded >= 0xd800 && decoded <= 0xdbff {
						if high != 0 {
							return l.fail("Unicode high surrogate must not follow a high surrogate.")
						}
						high = decoded
						segment = l.offset
						continue
					}
					if decoded >= 0xdc00 && decoded <= 0xdfff {
						if high == 0 {
							return l.fail("Unicode low surrogate must follow a high surrogate.")
						}
						decoded = 0x10000 + (high-0xd800)*0x400 + decoded - 0xdc00
						high = 0
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
			if l.jsonb {
				if high != 0 {
					return l.fail("Unicode low surrogate must follow a high surrogate.")
				}
				if decoded == 0 {
					err := l.fail(`\u0000 cannot be converted to text.`).(*sqlstate.Error)
					err.Code, err.Message = "22P05", "unsupported Unicode escape sequence"
					return err
				}
				b.WriteRune(decoded)
				segment = l.offset
			}
		}
	}
	return l.invalid()
}
