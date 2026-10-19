package parser

import (
	"strconv"
	"strings"
)

func timezoneIntervalPrecision(l sqlLexer, tok lexeme) (int64, bool) {
	if tok.numeric {
		l.(*lexer).syntax(tok)
		return 0, false
	}
	text := strings.ReplaceAll(tok.text, "_", "")
	base := 10
	if len(text) > 2 && text[0] == '0' && strings.ContainsRune("xXoObB", rune(text[1])) {
		base = 0
	}
	n, err := strconv.ParseInt(text, base, 32)
	if err != nil {
		l.(*lexer).syntax(tok)
		return 0, false
	}
	return n, true
}
func qualifiedTypedLiteral(l sqlLexer, schema, name lexeme) (TypeName, bool) {
	if schema.text == "pg_catalog" && name.text != "tsvector" && name.text != "tsquery" && name.text != "tid" && name.text != "inet" && name.text != "cidr" && name.text != "timestamptz" && name.text != "timetz" && name.text != "json" && name.text != "jsonpath" && name.text != "money" {
		l.(*lexer).failAt("0A000", "typed literals are unsupported", schema.pos.Offset)
		return TypeName{}, false
	}
	raw := columnType(name)
	raw.Schema = &Identifier{Text: schema.text, Pos: schema.pos}
	raw.Pos = schema.pos
	return raw, true
}
