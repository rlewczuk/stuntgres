package parser

import "fmt"

// VisitPLCompoundNames visits the non-overlapping two- and three-part names
// recognized by PostgreSQL's procedural scanner, before SQL classifies them as
// columns, callees, types, or other constructs. The span is original body source;
// strings and comments are not names. Scratch is required and belongs to this
// pass. The callback must not retain/mutate its borrowed parts or reenter with
// the same scratch.
func VisitPLCompoundNames(source string, start, end Position, parts *[3]Identifier, visit func(parts []Identifier) error) error {
	if start == (Position{}) && end == (Position{}) {
		return nil
	}
	if start.Character <= 0 || end.Character < start.Character || start.Offset < 0 || end.Offset <= start.Offset || end.Offset > len(source) {
		return fmt.Errorf("stuntgres: invalid PL compound-name source span")
	}
	s := plCompoundScanner{lexer: lexer{
		input: source[:end.Offset], offset: start.Offset,
		positionOffset: start.Offset, positionCharacters: start.Character - 1,
		options: Options{StandardConformingStrings: true},
	}}
	for {
		first := s.next()
		if s.lexer.err != nil {
			return s.lexer.err
		}
		if first.code == 0 {
			return nil
		}
		name, ok := plCompoundIdentifier(first, true)
		if !ok {
			continue
		}
		parts[0] = name
		dot := s.next()
		if s.lexer.err != nil {
			return s.lexer.err
		}
		if dot.code != '.' {
			s.pending[0], s.count = dot, 1
			continue
		}
		second := s.next()
		if s.lexer.err != nil {
			return s.lexer.err
		}
		name, ok = plCompoundIdentifier(second, false)
		if !ok {
			s.pending, s.count = [2]rawToken{second, dot}, 2
			continue
		}
		parts[1] = name
		dot = s.next()
		if s.lexer.err != nil {
			return s.lexer.err
		}
		if dot.code != '.' {
			s.pending[0], s.count = dot, 1
			if err := visit(parts[:2]); err != nil {
				return err
			}
			continue
		}
		third := s.next()
		if s.lexer.err != nil {
			return s.lexer.err
		}
		name, ok = plCompoundIdentifier(third, false)
		if !ok {
			s.pending, s.count = [2]rawToken{third, dot}, 2
			if err := visit(parts[:2]); err != nil {
				return err
			}
			continue
		}
		parts[2] = name
		if err := visit(parts[:]); err != nil {
			return err
		}
	}
}

type plCompoundScanner struct {
	lexer   lexer
	pending [2]rawToken
	count   int
}

func (s *plCompoundScanner) next() rawToken {
	if s.count != 0 {
		s.count--
		return s.pending[s.count]
	}
	token := s.lexer.nextRaw()
	if token.unicode && s.lexer.err == nil {
		token = s.lexer.filterUnicodeToken(token)
	}
	return token
}

func quotedIdentifierLexeme(token lexeme) bool {
	raw := token.raw
	return len(raw) != 0 && raw[0] == '"' || len(raw) >= 3 && (raw[0] == 'u' || raw[0] == 'U') && raw[1] == '&' && raw[2] == '"'
}

func plCompoundIdentifier(token rawToken, first bool) (Identifier, bool) {
	if first && token.code == PARAM {
		// PL's internal scanner preserves yytext here, including leading zeros.
		return Identifier{Text: token.lexeme.raw, Pos: token.lexeme.pos}, true
	}
	if token.code != IDENT && token.lexeme.keyword == nil {
		return Identifier{}, false
	}
	if !quotedIdentifierLexeme(token.lexeme) {
		// REL_18_6 pl_reserved_kwlist.h, not SQL's keyword categories. All
		// other SQL keywords reach the procedural scanner as identifiers.
		switch token.lexeme.text {
		case "all", "begin", "by", "case", "declare", "else", "end", "execute", "for", "foreach", "from", "if", "in", "into", "loop", "not", "null", "or", "strict", "then", "to", "using", "when", "while":
			return Identifier{}, false
		}
	}
	return Identifier{Text: token.lexeme.text, Pos: token.lexeme.pos}, true
}
