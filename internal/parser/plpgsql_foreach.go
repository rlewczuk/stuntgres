package parser

// plForeachDiagnostic runs only after the executable grammar failed. Released
// PL first admits the LOOP-delimited source, then its body, before SQL expression
// analysis. If that body's very first token cannot start a statement, retain its
// admission error instead of an earlier eager scalar-grammar error. No token or
// AST from this probe is returned to executable parsing.
func (l *lexer) plForeachDiagnostic() bool {
	origin := l.plForeachSource
	if origin.Character == 0 || l.plBoundary != plLoopBoundary {
		return false
	}
	probe := lexer{input: l.input, offset: origin.Offset, positionOffset: origin.Offset, positionCharacters: origin.Character - 1, options: l.options}
	probe.nextRaw() // The ARRAY introducer.
	depth := 0
	for {
		token := probe.nextRaw()
		if probe.err != nil {
			l.err = probe.err
			return true
		}
		if token.code == 0 {
			return false
		}
		if depth == 0 && (token.code == IDENT || token.lexeme.keyword != nil) && !quotedIdentifierLexeme(token.lexeme) && token.lexeme.text == "loop" {
			first := probe.nextRaw()
			if probe.err != nil {
				l.err = probe.err
				return true
			}
			if !plForeachBodyHead(first) {
				l.syntax(first.lexeme)
				return true
			}
			return false
		}
		switch token.code {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		}
	}
}

func plForeachBodyHead(token rawToken) bool {
	switch token.code {
	case IDENT, PARAM, BEGIN, DECLARE, IF, CASE, FOR, END, FETCH, MOVE, CLOSE, NULL, RETURN_BODY:
		return true
	}
	// Unreserved procedural words and assignable names remain ordinary raw
	// tokens. Quoted names are IDENT, while strings/numbers never start a body.
	return token.lexeme.text == "<<" || token.lexeme.keyword != nil && token.lexeme.keyword.category != Reserved
}
