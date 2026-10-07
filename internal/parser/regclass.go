package parser

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ParseRegclassName parses qualified-name type input, not SQL tokens.
// Only dots and ASCII scanner whitespace delimit unquoted components;
// punctuation and comment-looking bytes remain part of the name.
func ParseRegclassName(input string) (ObjectName, error) {
	parts := make([]Identifier, 0, 3)
	offset, positionOffset, characters := 0, 0, 0
	for {
		for offset < len(input) && space(input[offset]) {
			offset++
		}
		if offset == len(input) || input[offset] == '.' {
			return ObjectName{}, fmt.Errorf("invalid name syntax")
		}
		start := offset
		characters += utf8.RuneCountInString(input[positionOffset:start])
		positionOffset = start
		pos := Position{Offset: start, Character: characters + 1}
		var text string
		if input[offset] == '"' {
			offset++
			segment := offset
			var decoded strings.Builder
			for {
				if offset == len(input) {
					return ObjectName{}, fmt.Errorf("invalid name syntax")
				}
				if input[offset] != '"' {
					offset++
					continue
				}
				if offset+1 < len(input) && input[offset+1] == '"' {
					decoded.WriteString(input[segment : offset+1])
					offset += 2
					segment = offset
					continue
				}
				if segment == start+1 {
					text = input[segment:offset]
				} else {
					decoded.WriteString(input[segment:offset])
					text = decoded.String()
				}
				offset++
				break
			}
			text = truncateName(text)
		} else {
			needsFold := false
			for offset < len(input) && input[offset] != '.' && !space(input[offset]) {
				c := input[offset]
				needsFold = needsFold || offset-start < 63 && c >= 'A' && c <= 'Z'
				offset++
			}
			text = truncateName(input[start:offset])
			if needsFold {
				text = fold(text)
			}
		}
		parts = append(parts, Identifier{Text: text, Pos: pos})
		for offset < len(input) && space(input[offset]) {
			offset++
		}
		if offset == len(input) {
			return ObjectName{Parts: parts}, nil
		}
		if input[offset] != '.' || len(parts) == 3 {
			return ObjectName{}, fmt.Errorf("invalid name syntax")
		}
		offset++
	}
}
