package parser

import "strconv"

func roleLimit(y sqlLexer, tok lexeme, sign string) (int64, bool) {
	v, err := strconv.ParseInt(sign+tok.text, 10, 32)
	if err != nil {
		y.(*lexer).syntax(tok)
		return 0, false
	}
	return v, true
}
