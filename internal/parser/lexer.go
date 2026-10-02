package parser

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

type lexer struct {
	input      string
	offset     int
	current    lexeme
	err        error
	statements []Statement
}

var keywords = map[string]int{
	"create": CREATE, "table": TABLE, "insert": INSERT, "into": INTO, "values": VALUES,
	"select": SELECT, "from": FROM, "where": WHERE, "order": ORDER, "by": BY,
	"asc": ASC, "desc": DESC, "not": NOT, "null": NULL, "true": TRUE, "false": FALSE,
	"integer": INTEGER, "int": INTEGER, "int4": INTEGER, "bigint": BIGINT, "int8": BIGINT,
	"text": TEXT, "boolean": BOOLEAN, "bool": BOOLEAN,
}

// These tokens keep unsupported but PostgreSQL-valid types usable as ColId.
var unsupportedTypes = wordSet("smallint int2 numeric decimal dec real float float4 float8 double precision money char character varchar bit varbit date time timestamp interval bytea uuid json jsonb xml serial bigserial smallserial serial2 serial4 serial8 name oid inet cidr macaddr macaddr8 point line lseg box path polygon circle tsvector tsquery record")
var unsupportedWords = wordSet("begin start commit rollback savepoint release abort end transaction update delete merge truncate drop alter explain copy vacuum analyze grant revoke with union intersect except join inner left right full cross natural on as returning limit offset fetch group having distinct all window over for using nulls first last primary key unique check references default constraint if exists temporary temp unlogged view index schema database sequence function procedure trigger cast collate conflict do set reset show prepare execute deallocate discard listen notify unlisten lock reindex cluster refresh values and or is in between like ilike similar any some not case when then else array")

// Nonreserved keywords remain legal unquoted identifiers. Reserved words
// (e.g. AS, JOIN and DEFAULT) do not acquire identifier semantics here.
var unsupportedIdentifiers = wordSet("begin start commit rollback savepoint release abort end transaction update delete merge truncate drop alter explain copy vacuum analyze grant revoke first last temporary temp unlogged view index schema database sequence function procedure trigger if key conflict do set reset show prepare execute deallocate discard listen notify unlisten lock reindex cluster refresh between exists")

func wordSet(words string) map[string]bool {
	m := make(map[string]bool)
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

func space(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}
func initial(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 128
}
func continuation(c byte) bool { return initial(c) || c >= '0' && c <= '9' || c == '$' }
func digit(c byte) bool        { return c >= '0' && c <= '9' }
func fold(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
func truncateName(s string) string {
	if len(s) <= 63 {
		return s
	}
	n := 63
	for !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// skip returns whether the separator contained a newline, for SQL string
// continuation. Nested block comments are counted rather than split.
func (l *lexer) skip(allowBlock bool) bool {
	newline := false
	for l.offset < len(l.input) {
		c := l.input[l.offset]
		if space(c) {
			newline = newline || c == '\n' || c == '\r'
			l.offset++
			continue
		}
		if strings.HasPrefix(l.input[l.offset:], "--") {
			l.offset += 2
			for l.offset < len(l.input) && l.input[l.offset] != '\n' && l.input[l.offset] != '\r' {
				l.offset++
			}
			continue
		}
		if allowBlock && strings.HasPrefix(l.input[l.offset:], "/*") {
			l.offset += 2
			depth := 1
			for depth > 0 && l.offset < len(l.input) {
				if strings.HasPrefix(l.input[l.offset:], "/*") {
					depth++
					l.offset += 2
				} else if strings.HasPrefix(l.input[l.offset:], "*/") {
					depth--
					l.offset += 2
				} else {
					c = l.input[l.offset]
					newline = newline || c == '\n' || c == '\r'
					l.offset++
				}
			}
			if depth != 0 {
				l.fail("42601", "unterminated comment")
				return newline
			}
			continue
		}
		break
	}
	return newline
}

func (l *lexer) quoted(quote byte) (string, bool) {
	l.offset++
	var b strings.Builder
	for l.offset < len(l.input) {
		c := l.input[l.offset]
		l.offset++
		if c == 0 {
			l.fail("42601", "NUL in quoted input")
			return "", false
		}
		if c == quote {
			if l.offset < len(l.input) && l.input[l.offset] == quote {
				b.WriteByte(c)
				l.offset++
				continue
			}
			return b.String(), true
		}
		b.WriteByte(c)
	}
	l.fail("42601", "unterminated quoted input")
	return "", false
}
func (l *lexer) parameter(s string) int { n, _ := strconv.ParseInt(s, 10, 32); return int(n) }
func (l *lexer) Lex(v *sqlSymType) int {
	if l.err != nil {
		return 0
	}
	l.skip(true)
	if l.err != nil {
		return 0
	}
	start := l.offset
	l.current = lexeme{pos: Position{Offset: start}}
	if start == len(l.input) {
		return 0
	}
	c := l.input[start]
	token := 0
	switch {
	case c == '"':
		s, ok := l.quoted(c)
		if !ok {
			return 0
		}
		if s == "" {
			l.fail("42601", "empty quoted identifier")
			return 0
		}
		l.current.text = truncateName(s)
		token = IDENT
	case c == '\'':
		s, ok := l.quoted(c)
		if !ok {
			return 0
		}
		for {
			saved := l.offset
			newline := l.skip(false)
			if l.err != nil {
				return 0
			}
			if !newline || l.offset >= len(l.input) || l.input[l.offset] != '\'' {
				l.offset = saved
				break
			}
			next, ok := l.quoted(c)
			if !ok {
				return 0
			}
			s += next
		}
		l.current.text = s
		token = STRING
	case c == '$':
		l.offset++
		if l.offset < len(l.input) && (l.input[l.offset] == '$' || initial(l.input[l.offset])) {
			l.fail("0A000", "dollar quoting is unsupported")
			return 0
		}
		begin := l.offset
		for l.offset < len(l.input) && digit(l.input[l.offset]) {
			l.offset++
		}
		s := l.input[begin:l.offset]
		if s == "" || l.offset < len(l.input) && continuation(l.input[l.offset]) {
			l.fail("42601", "invalid parameter")
			return 0
		}
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			l.fail("42601", "parameter number too large")
			return 0
		}
		if n == 0 {
			l.fail("42P02", "parameter number must be positive")
			return 0
		}
		l.current.text = s
		token = PARAM
	case digit(c):
		for l.offset < len(l.input) && digit(l.input[l.offset]) {
			l.offset++
		}
		if l.offset < len(l.input) {
			next := l.input[l.offset]
			if next == '.' || next == 'e' || next == 'E' || next == 'x' || next == 'X' || next == 'b' || next == 'B' || next == 'o' || next == 'O' || next == '_' {
				l.fail("0A000", "non-decimal-integer literal is unsupported")
				return 0
			}
			if initial(next) {
				l.fail("42601", "trailing junk after integer")
				return 0
			}
		}
		l.current.text = l.input[start:l.offset]
		token = NUMBER
	case initial(c):
		for l.offset < len(l.input) && continuation(l.input[l.offset]) {
			l.offset++
		}
		raw := fold(l.input[start:l.offset])
		if l.offset < len(l.input) && ((raw == "e" || raw == "b" || raw == "x" || raw == "n") && l.input[l.offset] == '\'' || raw == "u" && strings.HasPrefix(l.input[l.offset:], "&")) {
			l.fail("0A000", "special quoted syntax is unsupported")
			return 0
		}
		l.current.text = truncateName(raw)
		if t, ok := keywords[raw]; ok {
			token = t
		} else if unsupportedTypes[raw] {
			token = UNSUP_TYPE
		} else if unsupportedIdentifiers[raw] {
			token = UNSUP_IDENT
		} else if unsupportedWords[raw] {
			token = UNSUP
		} else {
			token = IDENT
		}
	default:
		l.offset++
		l.current.text = string(c)
		if c == 0 {
			l.fail("42601", "NUL in SQL")
			return 0
		}
		if c == '.' && l.offset < len(l.input) && digit(l.input[l.offset]) {
			l.fail("0A000", "non-integer literal is unsupported")
			return 0
		}
		if c == ':' && l.offset < len(l.input) && l.input[l.offset] == ':' {
			l.current.text = "::"
			l.offset++
		}
		token = int(c)
	}
	v.tok = l.current
	return token
}
