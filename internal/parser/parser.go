package parser

//go:generate go run golang.org/x/tools/cmd/goyacc@v0.50.0 -l -p sql -o grammar_gen.go -v /dev/null grammar.y

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

// Parse consumes a complete SQL script. Execution policy (including the
// single-statement limit for normal driver calls) belongs to the caller.
func Parse(query string) ([]Statement, error) {
	l := &lexer{input: query}
	invalidOffset := -1
	if !utf8.ValidString(query) {
		for offset := 0; offset < len(query); {
			r, size := utf8.DecodeRuneInString(query[offset:])
			if r == utf8.RuneError && size == 1 {
				invalidOffset = offset
				break
			}
			offset += size
		}
	}
	if nulOffset := strings.IndexByte(query, 0); nulOffset >= 0 &&
		(invalidOffset < 0 || nulOffset < invalidOffset) {
		l.failAt("42601", "NUL in SQL", nulOffset)
		return nil, l.err
	}
	if invalidOffset >= 0 {
		l.failAt("42601", "invalid UTF-8 in SQL", invalidOffset)
		return nil, l.err
	}
	if sqlParse(l) != 0 || l.err != nil {
		return nil, l.err
	}
	return l.statements, nil
}

type lexeme struct {
	text string
	raw  string
	pos  Position
}

func integerValue(l sqlLexer, tok lexeme, sign string) (Value, bool) {
	base, offset := uint64(10), 0
	if len(tok.text) > 2 && tok.text[0] == '0' {
		switch tok.text[1] {
		case 'b', 'B':
			base, offset = 2, 2
		case 'o', 'O':
			base, offset = 8, 2
		case 'x', 'X':
			base, offset = 16, 2
		}
	}
	limit := uint64(1<<63 - 1)
	if sign == "-" {
		limit++
	}
	var magnitude uint64
	for ; offset < len(tok.text); offset++ {
		if tok.text[offset] == '_' {
			continue
		}
		d := numericDigit(tok.text[offset])
		if magnitude > (limit-d)/base {
			l.(*lexer).failAt("22003", "integer literal out of range", tok.pos.Offset)
			return Value{}, false
		}
		magnitude = magnitude*base + d
	}
	var n int64
	if sign == "-" && magnitude != 0 {
		// Map magnitude 2^63 to MinInt64 without overflowing conversion or
		// negation; zero uses the ordinary conversion below.
		n = -int64(magnitude-1) - 1
	} else {
		n = int64(magnitude)
	}
	return Value{Kind: IntegerValue, Integer: n, Pos: tok.pos}, true
}

func settingInteger(text string, negative bool) string {
	// PostgreSQL's scanner canonicalizes an unsigned int32 integer token.
	// Larger decimal tokens retain their original spelling as NumericOnly text;
	// the unary sign is applied separately, without broadening numeric syntax.
	if n, err := strconv.ParseInt(text, 10, 32); err == nil {
		if negative {
			n = -n
		}
		return strconv.FormatInt(n, 10)
	}
	if negative {
		return "-" + text
	}
	return text
}

func (l *lexer) fail(code, message string) {
	l.failAt(code, message, l.current.pos.Offset)
}

func (l *lexer) failAt(code, message string, offset int) {
	l.failDiagnostic(code, message, "", offset)
}

// A negative offset deliberately omits Position (decoded encoding errors).
func (l *lexer) failDiagnostic(code, message, hint string, offset int) {
	if l.err == nil {
		e := &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR",
			Code: code, Message: message, Hint: hint,
		}
		if offset >= 0 {
			e.Position = strconv.Itoa(utf8.RuneCountInString(l.input[:offset]) + 1)
		}
		l.err = e
	}
}

func (l *lexer) Error(message string) {
	code := "42601"
	if unsupportedWords[l.current.text] || strings.Contains("><![", l.current.text) && l.current.text != "" || l.current.text == "." || l.current.text == "::" || l.current.text == "select" || l.current.text == "into" {
		code = "0A000"
	}
	if code == "42601" {
		if l.current.raw == "" {
			message = "syntax error at end of input"
		} else {
			message = fmt.Sprintf("syntax error at or near \"%s\"", l.current.raw)
		}
	}
	l.fail(code, message)
}
