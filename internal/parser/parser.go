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
	if l.pendingMessage != "" {
		l.failAt("0A000", l.pendingMessage, l.pendingOffset)
		return nil, l.err
	}
	return l.statements, nil
}

type lexeme struct {
	text    string
	raw     string
	pos     Position
	keyword *keyword
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

func settingInteger(l sqlLexer, tok lexeme, negative bool) (string, bool) {
	text := tok.text
	// NUMBER is shared with INSERT/SELECT, which also accept radix/separator
	// spellings. Settings deliberately support decimal digits only.
	for i := range text {
		if text[i] < '0' || text[i] > '9' {
			l.(*lexer).failAt("0A000", "non-decimal setting integers are unsupported", tok.pos.Offset)
			return "", false
		}
	}
	// PostgreSQL's scanner canonicalizes an unsigned int32 integer token.
	// Larger decimal tokens retain their original spelling as NumericOnly text;
	// the unary sign is applied separately, without broadening numeric syntax.
	if n, err := strconv.ParseInt(text, 10, 32); err == nil {
		if negative {
			n = -n
		}
		return strconv.FormatInt(n, 10), true
	}
	if negative {
		return "-" + text, true
	}
	return text, true
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
			// Unicode decoding can place the release-derived byte limit inside
			// a source rune after dequoting/continuation. Count rune starts in
			// the complete input, including the rune crossing that limit.
			position := 1
			for start := range l.input {
				if start >= offset {
					break
				}
				position++
			}
			e.Position = strconv.Itoa(position)
		}
		l.err = e
	}
}

func (l *lexer) syntax(tok lexeme) {
	message := "syntax error at end of input"
	if tok.raw != "" {
		message = fmt.Sprintf("syntax error at or near \"%s\"", tok.raw)
	}
	l.failAt("42601", message, tok.pos.Offset)
}

func (l *lexer) Error(_ string) { l.syntax(l.current) }

func (l *lexer) deferUnsupported(tok lexeme, message string) {
	if l.pendingMessage == "" {
		l.pendingMessage, l.pendingOffset = message, tok.pos.Offset
	}
}

// These are capability boundaries in explicit grammar positions, not lexical
// classifications. Quoted names never acquire keyword identity.
var unsupportedTypes = wordSet("smallint int2 numeric decimal dec real float float4 float8 double precision money char character varchar bit varbit date time timestamp interval bytea uuid json jsonb xml serial bigserial smallserial serial2 serial4 serial8 name oid inet cidr macaddr macaddr8 point line lseg box path polygon circle tsvector tsquery record")
var unsupportedCommands = wordSet("begin start commit rollback savepoint release abort end update delete merge truncate drop alter explain copy vacuum analyze analyse grant revoke with prepare execute deallocate discard listen notify unlisten lock reindex cluster refresh reset")
var unsupportedCreateObjects = wordSet("temporary temp unlogged view index schema database sequence function procedure trigger")
var unsupportedSuffixes = wordSet("union intersect except join inner left right full cross natural on returning limit offset fetch group having window for using primary unique check references default constraint collate and or is in between like ilike similar")

func (l *lexer) capability(tok lexeme, words map[string]bool, message string) {
	if tok.keyword != nil && words[tok.text] {
		l.failAt("0A000", message, tok.pos.Offset)
	} else {
		l.syntax(tok)
	}
}

// These C-keywords have dedicated release expression productions, not generic
// function-name legality. Recognizing the capability never executes a call.
var specialCalls = wordSet("coalesce extract greatest grouping least normalize nullif overlay position row substring treat trim xmlconcat xmlelement xmlexists xmlforest xmlparse xmlpi xmlroot xmlserialize json json_array json_arrayagg json_exists json_object json_objectagg json_query json_scalar json_serialize json_value merge_action exists")

func (l *lexer) call(callee, opener lexeme) {
	if callee.keyword.typeFunctionName() {
		l.failAt("0A000", "functions are unsupported", callee.pos.Offset)
	} else if callee.keyword != nil && specialCalls[callee.text] {
		l.failAt("0A000", "functions are unsupported", callee.pos.Offset)
	} else {
		l.syntax(opener) // The name was valid as ColId; '(' is not.
	}
}

func columnType(l sqlLexer, tok lexeme) (TypeName, bool) {
	switch tok.text {
	case "int4":
		return TypeName{Name:"integer",Pos:tok.pos}, true
	case "int8":
		return TypeName{Name:"bigint",Pos:tok.pos}, true
	case "bool":
		return TypeName{Name:"boolean",Pos:tok.pos}, true
	}
	if unsupportedTypes[tok.text] {
		l.(*lexer).failAt("0A000", "unsupported column type", tok.pos.Offset)
	} else {
		l.(*lexer).syntax(tok)
	}
	return TypeName{}, false
}
