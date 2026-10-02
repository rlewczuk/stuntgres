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
	if !utf8.ValidString(query) {
		return nil, &sqlstate.Error{Code: "42601", Message: "invalid UTF-8 in SQL"}
	}
	if strings.IndexByte(query, 0) >= 0 {
		return nil, &sqlstate.Error{Code: "42601", Message: "NUL in SQL"}
	}
	if sqlParse(l) != 0 || l.err != nil {
		return nil, l.err
	}
	return l.statements, nil
}

type lexeme struct {
	text string
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
	if l.err == nil {
		l.err = &sqlstate.Error{Code: code, Message: fmt.Sprintf("%s at byte %d", message, l.current.pos.Offset)}
	}
}

func (l *lexer) Error(message string) {
	code := "42601"
	if unsupportedWords[l.current.text] || strings.Contains("><![", l.current.text) && l.current.text != "" || l.current.text == "." || l.current.text == "::" || l.current.text == "select" || l.current.text == "into" {
		code = "0A000"
	}
	l.fail(code, message)
}
