package parser

import "fmt"

// ParseRegclassName reuses SQL identifier scanning and normalization, including
// quoted embedded dots and UTF8 clipping, without executing a SQL statement.
func ParseRegclassName(input string) (ObjectName, error) {
	l := &lexer{input: input}
	var parts []Identifier
	for {
		token := l.nextRaw()
		if l.err != nil || token.code == 0 {
			return ObjectName{}, fmt.Errorf("invalid name syntax")
		}
		raw := token.lexeme.raw
		if raw == "" || raw[0] != '"' && !initial(raw[0]) {
			return ObjectName{}, fmt.Errorf("invalid name syntax")
		}
		parts = append(parts, Identifier{Text: token.lexeme.text, Pos: token.lexeme.pos})
		if len(parts) > 3 {
			return ObjectName{}, fmt.Errorf("invalid name syntax")
		}
		separator := l.nextRaw()
		if l.err != nil {
			return ObjectName{}, l.err
		}
		if separator.code == 0 {
			return ObjectName{Parts: parts}, nil
		}
		if separator.code != '.' {
			return ObjectName{}, fmt.Errorf("invalid name syntax")
		}
	}
}
