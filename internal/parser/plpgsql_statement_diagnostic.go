package parser

// Only a head recorded by the procedural statement-list grammar can reach this
// failure-only probe. Assignment operators clear that provenance before their
// scalar operands are read. Nothing here returns syntax or admits a statement.
func (l *lexer) plUnknownStatementDiagnostic() bool {
	head := l.plUnknownHead
	if head.pos.Character == 0 {
		return false
	}
	probe := lexer{input: l.input, offset: head.pos.Offset, positionOffset: head.pos.Offset, positionCharacters: head.pos.Character - 1, options: l.options, backslashEscapes: l.backslashEscapes}
	probe.options.Notice = nil
	probe.options.LineComment = nil
	depth := 0
	for {
		token := probe.nextRaw()
		if probe.err != nil {
			l.err = probe.err
			return true
		}
		if token.code == 0 {
			l.failAt("42601", "unexpected end of function definition at end of input", len(l.input))
			return true
		}
		if token.code == ';' && depth == 0 {
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
