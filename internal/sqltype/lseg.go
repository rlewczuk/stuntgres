package sqltype

import (
	"fmt"
	"strings"
)

func lsegSyntax(text string) error {
	return failure("22P02", fmt.Sprintf("invalid input syntax for type lseg: \"%s\"", text))
}

// lsegInput follows REL_18_6 path_decode with exactly two pair_decode calls.
// Optional commas and the asymmetric open-path closing rule are intentional.
// Endpoint coordinates remain in input order, including coincident endpoints.
func (d *Descriptor) lsegInput(text string) (Value, error) {
	s := geometryScanner{text: text}
	s.whitespace()
	open := s.take('[')
	outer := open
	if !open && s.pos < len(text) && text[s.pos] == '(' {
		next := s.pos + 1
		for next < len(text) && space(text[next]) {
			next++
		}
		if next < len(text) && text[next] == '(' || strings.LastIndexByte(text, '(') == s.pos {
			outer, s.pos = true, next
		}
	}
	var coordinates [4]float64
	for i := 0; i < 4; i += 2 {
		s.whitespace()
		paren := s.take('(')
		x, err := s.number(lsegSyntax)
		if err != nil {
			return Value{}, err
		}
		if !s.take(',') {
			return Value{}, lsegSyntax(text)
		}
		y, err := s.number(lsegSyntax)
		if err != nil {
			return Value{}, err
		}
		if paren && !s.take(')') {
			return Value{}, lsegSyntax(text)
		}
		coordinates[i], coordinates[i+1] = x, y
		s.whitespace()
		s.take(',')
	}
	if outer {
		if !s.take(')') && !(open && s.take(']')) {
			return Value{}, lsegSyntax(text)
		}
		s.whitespace()
	}
	if s.pos != len(text) {
		return Value{}, lsegSyntax(text)
	}
	return Value{typ: d, geometry: &geometryPayload{coordinates: coordinates}}, nil
}
