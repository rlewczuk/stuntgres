package parser

// Statement-head recognition must not steal lookahead from the empty grammar
// entries that open cursor and query-FOR SQL expressions.
func (l *lexer) plStatementStart() bool {
	if l.plDeclaring {
		return false
	}
	switch l.previousToken {
	case BEGIN, ';', THEN, PL_THEN, ELSE, PL_LOOP:
		return true
	}
	return false
}

func (l *lexer) plHeadAssignment(first rawToken) bool {
	probe := lexer{input: l.input, offset: first.end, options: l.options}
	next := probe.nextRaw()
	return next.code == '=' || next.lexeme.text == ":=" || next.code == '.' || next.code == '['
}

// plBeginCapture probes raw tokens only. SQL is still consumed by its ordinary
// grammar; only the structurally complete procedural destination is omitted.
func (l *lexer) plBeginCapture(first rawToken) {
	l.plExpression = true
	probe := lexer{input: l.input, offset: first.lexeme.pos.Offset, positionOffset: first.lexeme.pos.Offset, positionCharacters: first.lexeme.pos.Character - 1, options: l.options}
	depth, previous := 0, 0
	var into, start, end, delimiter Position
	strict := false
	for {
		t := probe.nextRaw()
		if t.code == 0 || t.code == ';' && depth == 0 {
			delimiter = t.lexeme.pos
			break
		}
		// INSERT/MERGE INTO introduce a relation, not a PL destination.
		if depth == 0 && t.code == INTO && previous != INSERT && previous != MERGE {
			if into.Character != 0 {
				l.scannerError("INTO specified more than once", t.lexeme.pos.Offset, t.end)
				return
			}
			into = t.lexeme.pos
			t = probe.nextRaw()
			if t.lexeme.text == "strict" && !quotedIdentifierLexeme(t.lexeme) {
				strict = true
				t = probe.nextRaw()
			}
			start = t.lexeme.pos
			var separators []Position
			for {
				if t.unicode && probe.err == nil {
					t = probe.filterUnicodeToken(t)
				}
				if probe.err != nil {
					l.err = probe.err
					return
				}
				if _, ok := plCompoundIdentifier(t, true); !ok {
					l.scannerError("syntax error", t.lexeme.pos.Offset, t.end)
					return
				}
				t = probe.nextRaw()
				for t.code == '.' {
					dot := t
					t = probe.nextRaw()
					if t.unicode && probe.err == nil {
						t = probe.filterUnicodeToken(t)
					}
					if probe.err != nil {
						l.err = probe.err
						return
					}
					if _, ok := plCompoundIdentifier(t, false); !ok {
						l.scannerError("syntax error", dot.lexeme.pos.Offset, dot.end)
						return
					}
					t = probe.nextRaw()
				}
				if probe.err != nil {
					l.err = probe.err
					return
				}
				// INTO reads datums, not the broader assignment/FETCH target
				// language. A subscript is rejected before parsing its contents.
				if t.code == '[' {
					l.scannerError("syntax error", t.lexeme.pos.Offset, t.end)
					return
				}
				if t.code != ',' {
					break
				}
				separators = append(separators, t.lexeme.pos)
				t = probe.nextRaw()
			}
			end = t.lexeme.pos
			targets := lexer{input: l.input[:end.Offset], offset: start.Offset, positionOffset: start.Offset, positionCharacters: start.Character - 1, options: l.options, plTargetsEntry: true}
			if parseLexer(&targets) != 0 || targets.err != nil {
				l.err = targets.err
				return
			}
			l.plCapture = &PLIntoStatement{Targets: targets.plTargetsResult, Separators: separators, Strict: strict, Start: first.lexeme.pos, IntoStart: into, TargetsStart: start, TargetsEnd: end}
			if t.code == ';' || t.code == 0 {
				delimiter = t.lexeme.pos
				break
			}
			if t.code == INTO {
				l.scannerError("INTO specified more than once", t.lexeme.pos.Offset, t.end)
				return
			}
		}
		switch t.code {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		}
		previous = t.code
	}
	if probe.err != nil {
		l.err = probe.err
		return
	}
	if l.plCapture == nil {
		l.plSQL = &PLSQLStatement{Query: nil, Start: first.lexeme.pos, End: delimiter, Delimiter: delimiter, Text: l.input[first.lexeme.pos.Offset:delimiter.Offset]}
		return
	}
	raw := l.plCapture
	// The delimiter also bounds trailing trivia and suffix INTO targets.
	raw.End = delimiter
	raw.Delimiter = delimiter
	text := []byte(l.input[raw.Start.Offset:delimiter.Offset])
	for i := raw.IntoStart.Offset - raw.Start.Offset; i < raw.TargetsEnd.Offset-raw.Start.Offset; i++ {
		if text[i] != '\n' && text[i] != '\r' {
			text[i] = ' '
		}
	}
	raw.Text = string(text)
}

// Utilities use their ordinary grammar without procedural INTO stripping.
func (l *lexer) plBeginCommand(first rawToken) {
	l.plExpression = true
	kind := PLCommandSQL
	if first.code == CALL {
		kind = PLCallSQL
	}
	l.plSQL = &PLSQLStatement{Kind: kind, Start: first.lexeme.pos}
}

func plSQLHead(code int) bool {
	switch code {
	case CREATE, ALTER, DROP, SET, RESET, SHOW, CALL, TRUNCATE, COMMENT, GRANT, REVOKE, LOCK, VACUUM, ANALYZE, REINDEX, REFRESH, COPY, DISCARD, DO, PREPARE, DEALLOCATE, NOTIFY, LISTEN, UNLISTEN, LOAD, REASSIGN, FDW_IMPORT:
		return true
	}
	return false
}

func (l *lexer) plSQLStatement(query Statement) PLStatement {
	if raw := l.plCapture; raw != nil {
		raw.Query = query
		l.plCapture = nil
		l.plExpression = false
		return PLStatement{Into: raw, Pos: raw.Start}
	}
	raw := l.plSQL
	if raw == nil {
		l.failAt("42601", "invalid embedded SQL statement", l.current.pos.Offset)
		return PLStatement{}
	}
	raw.Query = query
	l.plSQL = nil
	l.plExpression = false
	return PLStatement{SQL: raw, Pos: raw.Start}
}
