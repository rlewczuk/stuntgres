package parser

import "strings"

func jsonEncoding(l sqlLexer, format, encoding lexeme) (*JSONFormat, bool) {
	name := strings.ToUpper(encoding.text)
	switch name {
	case "UTF8", "UTF16", "UTF32":
		return &JSONFormat{Pos: format.pos, Encoding: name, EncodingPos: encoding.pos}, true
	default:
		l.(*lexer).failAt("22023", "unrecognized JSON encoding: "+encoding.text, encoding.pos.Offset)
		return nil, false
	}
}
