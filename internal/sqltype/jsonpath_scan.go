package sqltype

// Lexical rules adapted from PostgreSQL REL_18_0 jsonpath_scan.l.
// Copyright (c) 2019-2025, PostgreSQL Global Development Group.
// Distributed under the PostgreSQL License.
import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

type jsonpathToken struct {
	kind, text, raw string
	start, end      int
}
type jsonpathScanner struct {
	ctx  context.Context
	text string
	pos  int
}

func jsonpathSyntax(message, raw string) error {
	if raw == "" {
		return failure("42601", message+" at end of jsonpath input")
	}
	return failure("42601", fmt.Sprintf("%s at or near \"%s\" of jsonpath input", message, raw))
}
func jsonpathBlank(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }
func jsonpathOther(c byte) bool {
	return !jsonpathBlank(c) && !strings.ContainsRune(`?%$.[]{}()|&!=<>@#,*:-+/\"`, rune(c))
}
func jsonpathDigit(c byte, base uint64) bool { _, ok := digit(c, base); return ok }
func (s *jsonpathScanner) next() (jsonpathToken, error) {
restart:
	for s.pos < len(s.text) {
		if err := s.ctx.Err(); err != nil {
			return jsonpathToken{}, err
		}
		if jsonpathBlank(s.text[s.pos]) {
			s.pos++
			continue
		}
		if strings.HasPrefix(s.text[s.pos:], "/*") {
			s.pos += 2
			for s.pos < len(s.text) && !strings.HasPrefix(s.text[s.pos:], "*/") {
				if err := s.ctx.Err(); err != nil {
					return jsonpathToken{}, err
				}
				s.pos++
			}
			if s.pos == len(s.text) {
				return jsonpathToken{}, jsonpathSyntax("unexpected end of comment", "")
			}
			s.pos += 2
			continue
		}
		break
	}
	start := s.pos
	if start == len(s.text) {
		return jsonpathToken{kind: "eof", start: start, end: start}, nil
	}
	c := s.text[start]
	if c == '"' || c == '$' && start+1 < len(s.text) && s.text[start+1] == '"' {
		kind := "quoted"
		if c == '$' {
			kind = "variable"
			s.pos++
		}
		s.pos++
		text, err := s.name(true)
		return jsonpathToken{kind: kind, text: text, raw: `"`, start: start, end: s.pos}, err
	}
	if c == '$' && start+1 < len(s.text) && jsonpathOther(s.text[start+1]) {
		s.pos++
		for s.pos < len(s.text) && jsonpathOther(s.text[s.pos]) {
			if err := s.ctx.Err(); err != nil {
				return jsonpathToken{}, err
			}
			s.pos++
		}
		return jsonpathToken{kind: "variable", text: s.text[start+1 : s.pos], raw: s.text[start:s.pos], start: start, end: s.pos}, nil
	}
	if c >= '0' && c <= '9' || c == '.' && start+1 < len(s.text) && jsonpathDigit(s.text[start+1], 10) {
		if tok, ok, err := s.numeric(); ok || err != nil {
			return tok, err
		}
	}
	for _, pair := range []string{"&&", "||", "**", "<=", ">=", "==", "!=", "<>"} {
		if strings.HasPrefix(s.text[start:], pair) {
			s.pos += 2
			kind := pair
			if kind == "<>" {
				kind = "!="
			}
			return jsonpathToken{kind: kind, text: pair, raw: pair, start: start, end: s.pos}, nil
		}
	}
	if !jsonpathOther(c) && c != '\\' {
		s.pos++
		return jsonpathToken{kind: string(c), text: string(c), raw: string(c), start: start, end: s.pos}, nil
	}
	text, err := s.name(false)
	if err != nil {
		return jsonpathToken{}, err
	}
	// The released xnq comment rule changes back to INITIAL without returning
	// the pending identifier. A comment directly adjoining a name therefore
	// differs from a whitespace-separated comment.
	if strings.HasPrefix(s.text[s.pos:], "/*") {
		goto restart
	}
	end := s.pos
	raw := ""
	if s.pos < len(s.text) {
		raw = string(s.text[s.pos])
	}
	if s.pos < len(s.text) && jsonpathBlank(s.text[s.pos]) {
		startBlank := s.pos
		for s.pos < len(s.text) && jsonpathBlank(s.text[s.pos]) {
			if err := s.ctx.Err(); err != nil {
				return jsonpathToken{}, err
			}
			s.pos++
		}
		raw = s.text[startBlank:s.pos]
	}
	kind := "name"
	lower := text
	for i := range len(text) {
		if text[i] >= 128 {
			lower = ""
			break
		}
	}
	lower = strings.ToLower(lower)
	switch lower {
	case "true", "false", "null":
		if lower == text {
			kind = lower
		}
	case "is", "to", "abs", "lax", "date", "flag", "last", "size", "time", "type", "with", "floor", "bigint", "double", "exists", "number", "starts", "strict", "string", "boolean", "ceiling", "decimal", "integer", "time_tz", "unknown", "datetime", "keyvalue", "timestamp", "like_regex", "timestamp_tz":
		kind = lower
	}
	return jsonpathToken{kind: kind, text: text, raw: raw, start: start, end: end}, err
}
func (s *jsonpathScanner) name(quoted bool) (string, error) {
	begin := s.pos
	// Borrow an immutable source span when no decoding is necessary.
	for s.pos < len(s.text) {
		if err := s.ctx.Err(); err != nil {
			return "", err
		}
		c := s.text[s.pos]
		if c == '\\' {
			break
		}
		if quoted && c == '"' {
			end := s.pos
			s.pos++
			return s.text[begin:end], nil
		}
		if !quoted && !jsonpathOther(c) {
			return s.text[begin:s.pos], nil
		}
		s.pos++
	}
	if s.pos == len(s.text) {
		if quoted {
			return "", jsonpathSyntax("unterminated quoted string", "")
		}
		return s.text[begin:s.pos], nil
	}
	var b strings.Builder
	b.WriteString(s.text[begin:s.pos])
	for s.pos < len(s.text) {
		if err := s.ctx.Err(); err != nil {
			return "", err
		}
		c := s.text[s.pos]
		if quoted && c == '"' {
			s.pos++
			return b.String(), nil
		}
		if !quoted && !jsonpathOther(c) && c != '\\' {
			return b.String(), nil
		}
		if c != '\\' {
			b.WriteByte(c)
			s.pos++
			continue
		}
		start := s.pos
		s.pos++
		if s.pos == len(s.text) {
			return "", jsonpathSyntax("unexpected end after backslash", `\`)
		}
		c = s.text[s.pos]
		s.pos++
		switch c {
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case 'x':
			v, ok, err := s.hex(2, false)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", jsonpathSyntax("invalid hexadecimal character sequence", s.text[start:s.pos])
			}
			if v == 0 {
				return "", jsonpathUnicodeZero()
			}
			b.WriteRune(rune(v))
		case 'u':
			// A maximal run of Unicode escapes is decoded together. An unfinished
			// later escape wins over surrogate validation, just as the flex rule does.
			var local [8]int
			codes := local[:0]
			for {
				v, ok, err := s.hex(4, true)
				if err != nil {
					return "", err
				}
				if !ok {
					return "", jsonpathSyntax("invalid Unicode escape sequence", s.text[start:s.pos])
				}
				codes = append(codes, v)
				if !strings.HasPrefix(s.text[s.pos:], `\u`) {
					break
				}
				s.pos += 2
			}
			high := -1
			for _, v := range codes {
				if err := s.ctx.Err(); err != nil {
					return "", err
				}
				switch {
				case v >= 0xd800 && v <= 0xdbff:
					if high != -1 {
						return "", jsonpathDetail("22P02", "Unicode high surrogate must not follow a high surrogate.")
					}
					high = v
					continue
				case v >= 0xdc00 && v <= 0xdfff:
					if high == -1 {
						return "", jsonpathDetail("22P02", "Unicode low surrogate must follow a high surrogate.")
					}
					v = 0x10000 + (high-0xd800)*1024 + (v - 0xdc00)
					high = -1
				case high != -1:
					return "", jsonpathDetail("22P02", "Unicode low surrogate must follow a high surrogate.")
				}
				if v == 0 {
					return "", jsonpathUnicodeZero()
				}
				if v > utf8.MaxRune {
					return "", failure("42601", "invalid Unicode code point")
				}
				b.WriteRune(rune(v))
			}
			if high != -1 {
				return "", jsonpathDetail("22P02", "Unicode low surrogate must follow a high surrogate.")
			}
		default:
			b.WriteByte(c)
		}
	}
	if quoted {
		return "", jsonpathSyntax("unterminated quoted string", "")
	}
	return b.String(), nil
}
func (s *jsonpathScanner) hex(count int, braces bool) (int, bool, error) {
	braced := braces && s.pos < len(s.text) && s.text[s.pos] == '{'
	if braced {
		s.pos++
		count = 6
	}
	v := 0
	n := 0
	for n < count && s.pos < len(s.text) && jsonpathDigit(s.text[s.pos], 16) {
		if err := s.ctx.Err(); err != nil {
			return 0, false, err
		}
		d, _ := digit(s.text[s.pos], 16)
		v = v*16 + int(d)
		s.pos++
		n++
	}
	if braced {
		if n == 0 || s.pos == len(s.text) || s.text[s.pos] != '}' {
			return 0, false, nil
		}
		s.pos++
		return v, true, nil
	}
	return v, n == count, nil
}
func (s *jsonpathScanner) numeric() (jsonpathToken, bool, error) {
	start := s.pos
	i := start
	kind := "int"
	scan := func(base uint64) error {
		for i < len(s.text) && jsonpathDigit(s.text[i], base) {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			i++
			if i+1 < len(s.text) && s.text[i] == '_' && jsonpathDigit(s.text[i+1], base) {
				i++
			}
		}
		return nil
	}
	// Radix tokens compete with the longer identifier rule; malformed radix
	// names therefore remain identifiers, with end-of-input syntax diagnostics.
	if i+1 < len(s.text) && s.text[i] == '0' && strings.ContainsRune("xXoObB", rune(s.text[i+1])) {
		base := uint64(16)
		switch s.text[i+1] {
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		i += 2
		if i == len(s.text) || !jsonpathDigit(s.text[i], base) {
			return jsonpathToken{}, false, nil
		}
		if err := scan(base); err != nil {
			return jsonpathToken{}, false, err
		}
		if i < len(s.text) && jsonpathOther(s.text[i]) {
			return jsonpathToken{}, false, nil
		}
		s.pos = i
		return jsonpathToken{kind: kind, text: s.text[start:i], raw: s.text[start:i], start: start, end: i}, true, nil
	}
	if s.text[i] != '.' {
		if s.text[i] == '0' {
			i++
		} else {
			if err := scan(10); err != nil {
				return jsonpathToken{}, false, err
			}
		}
	}
	if i < len(s.text) && s.text[i] == '.' {
		kind = "numeric"
		i++
		if err := scan(10); err != nil {
			return jsonpathToken{}, false, err
		}
	}
	if i < len(s.text) && (s.text[i] == 'e' || s.text[i] == 'E') {
		exponent := i
		i++
		if i < len(s.text) && (s.text[i] == '+' || s.text[i] == '-') {
			i++
		}
		if i < len(s.text) && jsonpathDigit(s.text[i], 10) {
			kind = "numeric"
			if err := scan(10); err != nil {
				return jsonpathToken{}, false, err
			}
		} else if i > exponent+1 {
			return jsonpathToken{}, false, jsonpathSyntax("invalid numeric literal", s.text[start:i])
		} else {
			i = exponent
		}
	}
	if i < len(s.text) && jsonpathOther(s.text[i]) {
		// flex numeric_junk consumes exactly one 'other' character.
		_, width := utf8.DecodeRuneInString(s.text[i:])
		i += width
		// An identifier with a longer raw match wins over numeric_junk.
		end := start
		for end < len(s.text) && jsonpathOther(s.text[end]) {
			end++
		}
		if end > i {
			return jsonpathToken{}, false, nil
		}
		return jsonpathToken{}, false, jsonpathSyntax("trailing junk after numeric literal", s.text[start:i])
	}
	s.pos = i
	return jsonpathToken{kind: kind, text: s.text[start:i], raw: s.text[start:i], start: start, end: i}, true, nil
}
