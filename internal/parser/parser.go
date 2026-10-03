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
	n, err := strconv.ParseInt(sign+tok.text, 10, 64)
	if err != nil {
		l.(*lexer).fail("22003", "integer literal out of range")
		return Value{}, false
	}
	return Value{Kind: IntegerValue, Integer: n, Pos: tok.pos}, true
}

func (l *lexer) fail(code, message string) {
	l.failAt(code, message, l.current.pos.Offset)
}

func (l *lexer) failAt(code, message string, offset int) {
	if l.err == nil {
		l.err = &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR",
			Code: code, Message: message,
			Position: strconv.Itoa(utf8.RuneCountInString(l.input[:offset]) + 1),
		}
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
