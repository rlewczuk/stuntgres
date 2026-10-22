// Package fts owns immutable text-search input values, not SQL or matching.
package fts

import (
	"context"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

const maxWordBytes = 2046
const maxStringOffset = 1048575

func failure(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func space(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
func operator(c byte) bool {
	return c == '!' || c == '&' || c == '|' || c == '(' || c == ')' || c == '<'
}

type lexer struct {
	ctx        context.Context
	text       string
	pos, steps int
	query      bool
}

func (l *lexer) check() error {
	l.steps++
	if l.steps&255 == 0 {
		return l.ctx.Err()
	}
	return nil
}
func (l *lexer) skip() error {
	for l.pos < len(l.text) && space(l.text[l.pos]) {
		l.pos++
		if err := l.check(); err != nil {
			return err
		}
	}
	return nil
}
func (l *lexer) syntax() error {
	typ := "tsvector"
	if l.query {
		typ = "tsquery"
	}
	return sqlstate.SoftInputError(failure("42601", "syntax error in "+typ+": \""+l.text+"\""))
}

// word borrows a source substring unless FTS quoting requires decoding. It leaves
// position/modifier punctuation for the owning vector/query grammar.
func (l *lexer) word() (string, error) {
	if l.pos == len(l.text) {
		return "", l.syntax()
	}
	quoted := l.text[l.pos] == '\''
	if l.query && (operator(l.text[l.pos]) || l.text[l.pos] == ':') {
		return "", l.syntax()
	}
	if quoted {
		l.pos++
	}
	start, segment := l.pos, l.pos
	var out strings.Builder
	decoded := false
	for l.pos < len(l.text) {
		if err := l.check(); err != nil {
			return "", err
		}
		c := l.text[l.pos]
		if c == '\\' {
			if !decoded {
				out.Grow(l.pos - start + 16)
				decoded = true
			}
			out.WriteString(l.text[segment:l.pos])
			l.pos++
			if l.pos == len(l.text) {
				return "", sqlstate.SoftInputError(failure("42601", "there is no escaped character: \""+l.text+"\""))
			}
			_, n := utf8.DecodeRuneInString(l.text[l.pos:])
			out.WriteString(l.text[l.pos : l.pos+n])
			l.pos += n
			segment = l.pos
			continue
		}
		if quoted && c == '\'' {
			if l.pos+1 < len(l.text) && l.text[l.pos+1] == '\'' {
				if !decoded {
					out.Grow(l.pos - start + 16)
					decoded = true
				}
				out.WriteString(l.text[segment:l.pos])
				out.WriteByte('\'')
				l.pos += 2
				segment = l.pos
				continue
			}
			end := l.pos
			l.pos++
			if decoded {
				out.WriteString(l.text[segment:end])
				if out.Len() == 0 {
					return "", l.syntax()
				}
				return out.String(), nil
			}
			if end == start {
				return "", l.syntax()
			}
			return l.text[start:end], nil
		}
		if !quoted && (space(c) || (l.query && operator(c)) || (c == ':' && l.pos > start)) {
			break
		}
		_, n := utf8.DecodeRuneInString(l.text[l.pos:])
		l.pos += n
	}
	if quoted {
		return "", l.syntax()
	}
	if decoded {
		out.WriteString(l.text[segment:l.pos])
		if out.Len() == 0 {
			return "", l.syntax()
		}
		return out.String(), nil
	}
	if l.pos == start {
		return "", l.syntax()
	}
	return l.text[start:l.pos], nil
}

func quote(out *strings.Builder, word string) {
	out.WriteByte('\'')
	for i := range len(word) {
		if word[i] == '\'' || word[i] == '\\' {
			out.WriteByte(word[i])
		}
		out.WriteByte(word[i])
	}
	out.WriteByte('\'')
}
