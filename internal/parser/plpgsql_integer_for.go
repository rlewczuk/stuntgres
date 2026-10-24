package parser

import (
	"strconv"
	"unicode/utf8"
)

// Classify only the current procedural FOR header. Ordinary scalar grammar
// still parses every operand. Raw lookahead cannot mutate parser state or emit
// callbacks/notices twice, and never scans beyond the header's statement.
func (l *lexer) plIntegerForAhead() (integer bool, reverse Position) {
	probe := *l
	probe.options.Notice = nil
	probe.options.LineComment = nil
	probe.typeInputNotice = nil
	depth := 0
	in, first := false, false
	previous := 0
	for {
		token := probe.nextRaw()
		if probe.err != nil {
			l.err = probe.err
			return false, Position{}
		}
		if token.code == 0 || token.code == ';' {
			return false, reverse
		}
		if depth == 0 {
			if !in {
				if token.code == IN {
					in, first = true, true
				}
			} else {
				if first && !quotedIdentifierLexeme(token.lexeme) && token.lexeme.text == "reverse" {
					reverse = token.lexeme.pos
				}
				first = false
				if token.code == DOT_DOT {
					return true, reverse
				}
				if previous != '.' && !quotedIdentifierLexeme(token.lexeme) && token.lexeme.text == "loop" {
					return false, reverse
				}
			}
		}
		switch token.code {
		case '(', '[':
			depth++
		case ')', ']':
			if depth > 0 {
				depth--
			}
		}
		previous = token.code
	}
}

// PLIntegerForTargetName checks target spelling against original bounded source.
// It scans identifiers/dots only, never returning executable expression syntax.
func PLIntegerForTargetName(source string, start, end Position) (ObjectName, bool) {
	if start.Offset < 0 || end.Offset <= start.Offset || end.Offset > len(source) || start.Character != utf8.RuneCountInString(source[:start.Offset])+1 {
		return ObjectName{}, false
	}
	probe := lexer{input: source[:end.Offset], offset: start.Offset, positionOffset: start.Offset, positionCharacters: start.Character - 1, options: Options{StandardConformingStrings: true}}
	token := probe.nextRaw()
	if token.unicode {
		token = probe.filterUnicodeToken(token)
	}
	var name ObjectName
	if token.code == PARAM {
		n, err := strconv.Atoi(token.lexeme.text)
		if err != nil {
			return ObjectName{}, false
		}
		name.Parts = append(name.Parts, Identifier{Text: "$" + strconv.Itoa(n), Pos: token.lexeme.pos})
	} else if token.code == IDENT || token.lexeme.keyword != nil {
		name.Parts = append(name.Parts, Identifier{Text: token.lexeme.text, Pos: token.lexeme.pos})
	} else {
		return ObjectName{}, false
	}
	for {
		token = probe.nextRaw()
		if probe.err != nil {
			return ObjectName{}, false
		}
		if token.code == IN {
			return name, true
		}
		if token.code != '.' {
			return ObjectName{}, false
		}
		token = probe.nextRaw()
		if token.unicode {
			token = probe.filterUnicodeToken(token)
		}
		if probe.err != nil || token.code != IDENT && token.lexeme.keyword == nil {
			return ObjectName{}, false
		}
		name.Parts = append(name.Parts, Identifier{Text: token.lexeme.text, Pos: token.lexeme.pos})
	}
}
