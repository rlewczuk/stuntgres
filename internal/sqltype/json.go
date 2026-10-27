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
	decode, discard                                    bool
	decoded                                            string
	origin                                             string
	originOffset                                       int
}

func (d *Descriptor) jsonInput(ctx context.Context, text string) (Value, error) {
	return d.scanJSON(ctx, text, nil)
}

// scanJSON is the shared iterative grammar for input and selected processing.
// Processing retains selected top-level fields without building a JSONB tree.
func (d *Descriptor) scanJSON(ctx context.Context, text string, scan *jsonScanResult) (Value, error) {
	build := scan == nil && d.id == JSONB
	elements := scan != nil && (scan.mode == jsonScanElements || scan.mode == jsonScanElementsText)
	// Classification needs syntax only. Uniqueness validates decoded escapes
	// throughout the document; processing keeps its existing decoding demand.
	l := jsonLexer{ctx: ctx, text: text, line: 1, decode: build || scan != nil && (scan.unique || !scan.classify && scan.mode != jsonScanElements)}
	if scan != nil {
		l.origin, l.originOffset = scan.origin, scan.originOffset
	}
	var local [32]jsonState
	stack := local[:1]
	stack[0] = jsonValue
	var localKeys [32]map[string]struct{}
	objectKeys := localKeys[:]
	var frames [32]jsonbFrame
	builder := jsonbBuilder{}
	if build {
		builder.frames = frames[:0]
	}
	var pendingNumber string
	var member jsonMember
	memberActive := false
	for {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		// A completed immediate value ends at the last consumed token, not
		// at the next comma/brace or its surrounding whitespace.
		if scan != nil && scan.extract != nil {
			scan.extract.finish(&l, len(stack)-1, stack[len(stack)-1])
		}
		if scan != nil && scan.record != nil {
			scan.record.finish(&l, len(stack)-1, stack[len(stack)-1])
		}
		if scan != nil && scan.record == nil && scan.extract == nil && memberActive && len(stack) == 2 && (stack[1] == jsonObjectNext || elements && stack[1] == jsonArrayNext) {
			member.end = l.end
			scan.members = append(scan.members, member)
			memberActive = false
		}
		state := stack[len(stack)-1]
		// Decode recursive uniqueness keys; processing modes below select
		// any additional immediate keys or string values they retain.
		l.discard = scan != nil && !(scan.unique && (state == jsonObjectFirst || state == jsonObjectKey))
		if scan != nil && scan.extract != nil {
			l.discard = !scan.extract.keepDecoded(len(stack)-1, state)
		} else if scan != nil && !scan.classify && len(stack) == 2 {
			if elements {
				l.discard = scan.mode != jsonScanElementsText || (state != jsonArrayFirst && state != jsonArrayValue)
			} else {
				l.discard = state != jsonObjectFirst && state != jsonObjectKey && !(scan.mode == jsonScanEachText && state == jsonObjectValue)
			}
		}
		if scan != nil && scan.record != nil {
			l.discard = false
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
		if scan != nil && scan.extract != nil {
			if err := scan.extract.token(&l, len(stack)-1, state); err != nil {
				return Value{}, err
			}
		}
		if scan != nil && scan.record != nil {
			scan.record.token(&l, len(stack)-1, state)
		}
		if scan != nil && state == jsonValue {
			scan.root = l.kind
			if scan.recordSet && scan.recordName != "" && l.kind == 's' {
				if err := jsonRecordCheckString(ctx, text, scan.origin, scan.originOffset); err != nil {
					return Value{}, err
				}
			}
			if err := scan.checkRoot(); err != nil {
				return Value{}, err
			}
		}
		objectValue := !elements && state == jsonObjectValue
		arrayValue := elements && (state == jsonArrayFirst || state == jsonArrayValue) && l.kind != ']'
		if scan != nil && scan.record == nil && scan.extract == nil && !scan.classify && scan.mode != jsonScanKeys && len(stack) == 2 && (objectValue || arrayValue) {
			member.start, member.kind, member.decoded = l.start, l.kind, l.decoded
			memberActive = true
		}
		if state == jsonEnd && l.kind == 0 {
			if scan != nil {
				return Value{}, ctx.Err()
			}
			if build {
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
				if build {
					if err := builder.close(ctx); err != nil {
						return Value{}, err
					}
				}
				stack = stack[:len(stack)-1]
				continue
			}
		case jsonArrayNext:
			if l.kind == ']' {
				if build {
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
				if scan != nil && scan.unique && len(stack) < len(objectKeys) {
					objectKeys[len(stack)] = nil
				}
				if build {
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
			if scan != nil {
				if scan.record == nil && scan.extract == nil && !scan.classify && !elements && len(stack) == 2 {
					if scan.mode == jsonScanKeys {
						scan.keys = append(scan.keys, l.decoded)
					} else {
						member = jsonMember{key: l.decoded}
					}
				}
				if scan.unique {
					depth := len(stack)
					for len(objectKeys) <= depth {
						objectKeys = append(objectKeys, nil)
					}
					keys := objectKeys[depth]
					if keys == nil {
						keys = make(map[string]struct{})
						objectKeys[depth] = keys
					}
					if _, exists := keys[l.decoded]; exists {
						scan.duplicate = true
					}
					keys[l.decoded] = struct{}{}
				}
			}
			if build {
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
				if scan != nil && scan.unique {
					objectKeys[len(stack)] = nil
				}
				if build {
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
			if build {
				builder.open(jsonbObject)
			}
			stack = append(stack, jsonObjectFirst)
		case '[':
			if build {
				builder.open(jsonbArray)
			}
			stack = append(stack, jsonArrayFirst)
		case 'n':
			if build {
				pendingNumber = l.text[l.start:l.end]
			}
		case 's':
			if build {
				builder.add(jsonbNode{kind: jsonbString, text: l.decoded})
			}
		case 't', 'f':
			if build {
				builder.add(jsonbNode{kind: jsonbBoolean, boolean: l.kind == 't'})
			}
		case 'z':
			if build {
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
	if l.origin != "" {
		// Only error formatting needs the preceding rows. Normal traversal
		// borrows a row slice without copying or rescanning the prefix.
		full := *l
		prefix := l.origin[:l.originOffset]
		full.text, full.origin = l.origin, ""
		full.start += l.originOffset
		full.end += l.originOffset
		full.line += strings.Count(prefix, "\n")
		if l.line == 1 {
			full.lineStart = strings.LastIndexByte(prefix, '\n') + 1
		} else {
			full.lineStart += l.originOffset
		}
		return full.fail(detail)
	}
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
		i, valid, err := scanJSONNumber(l.ctx, l.text, l.start)
		if err != nil {
			return err
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

// All consumers share grammar and diagnostic endpoints. Decoding consumers
// validate every escape before later syntax, even in nested/discarded values.
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
			if l.decode && high != 0 {
				return l.fail("Unicode low surrogate must follow a high surrogate.")
			}
			if c == '"' {
				if l.decode && !l.discard {
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
			if l.decode && !l.discard {
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
				if l.decode {
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
			if l.decode {
				if high != 0 {
					return l.fail("Unicode low surrogate must follow a high surrogate.")
				}
				if decoded == 0 {
					err := l.fail(`\u0000 cannot be converted to text.`).(*sqlstate.Error)
					err.Code, err.Message = "22P05", "unsupported Unicode escape sequence"
					return sqlstate.SoftInputError(err)
				}
				if !l.discard {
					b.WriteRune(decoded)
					segment = l.offset
				}
			}
		}
	}
	return l.invalid()
}
