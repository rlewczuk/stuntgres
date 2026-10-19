package sqltype

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
)

// Cube creates an installed scalar identity; its associated array is lookup-only.
func Cube(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid cube descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "CUBE", cube: true}, nil
}
func (d *Descriptor) IsCube() bool { return d != nil && d.cube }

type cubePayload struct {
	dimension   int
	point       bool
	coordinates []float64
}

func (v Value) validCube() bool {
	if v.cube == nil || v.cube.dimension < 0 || v.cube.dimension > 100 {
		return false
	}
	n := v.cube.dimension
	if !v.cube.point {
		n *= 2
	}
	return len(v.cube.coordinates) == n
}
func (v Value) RemapCube(target *Descriptor) (Value, error) {
	if !v.typ.IsCube() || !target.IsCube() || !v.null && !v.validCube() {
		return Value{}, fmt.Errorf("incompatible imported cube")
	}
	v.typ = target
	return v, nil
}
func cubeSyntax(detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for cube", Detail: detail}
}

type cubeScanner struct {
	text string
	pos  int
}

func (s *cubeScanner) whitespace() {
	for s.pos < len(s.text) && space(s.text[s.pos]) {
		s.pos++
	}
}
func (s *cubeScanner) token() string {
	s.whitespace()
	start := s.pos
	if start == len(s.text) {
		return ""
	}
	i := start
	if s.text[i] == '+' || s.text[i] == '-' {
		i++
	}
	// Flex chooses the longest numeric token; an unrecognized character is one token.
	for _, word := range []string{"Infinity", "Inf", "NaN"} {
		if word == "NaN" && i != start {
			continue
		}
		if len(s.text)-i >= len(word) && strings.EqualFold(s.text[i:i+len(word)], word) {
			s.pos = i + len(word)
			return s.text[start:s.pos]
		}
	}
	digits := 0
	for i < len(s.text) && s.text[i] >= '0' && s.text[i] <= '9' {
		i++
		digits++
	}
	if i < len(s.text) && s.text[i] == '.' {
		i++
		for i < len(s.text) && s.text[i] >= '0' && s.text[i] <= '9' {
			i++
			digits++
		}
	}
	if digits > 0 {
		if i < len(s.text) && (s.text[i] == 'e' || s.text[i] == 'E') {
			end := i + 1
			if end < len(s.text) && (s.text[end] == '+' || s.text[end] == '-') {
				end++
			}
			first := end
			for end < len(s.text) && s.text[end] >= '0' && s.text[end] <= '9' {
				end++
			}
			if end > first {
				i = end
			}
		}
		s.pos = i
		return s.text[start:i]
	}
	s.pos = start + 1
	return s.text[start:s.pos]
}
func (s *cubeScanner) syntax() error {
	token := s.token()
	if token == "" {
		return cubeSyntax("syntax error at end of input")
	}
	return cubeSyntax(fmt.Sprintf("syntax error at or near \"%s\"", token))
}
func (s *cubeScanner) take(ch byte) bool {
	s.whitespace()
	if s.pos < len(s.text) && s.text[s.pos] == ch {
		s.pos++
		return true
	}
	return false
}

// list recognizes a complete coordinate list without converting its tokens.
// Counts and raw spans include over-limit lists; only the first 100 tokens
// need storage because dimension errors precede coordinate conversion.
func (s *cubeScanner) list(ctx context.Context, dst []string, parens bool) (int, string, error) {
	s.whitespace()
	start := s.pos
	if parens && s.take(')') {
		return 0, "", nil
	}
	n := 0
	for {
		if err := ctx.Err(); err != nil {
			return 0, "", err
		}
		saved := s.pos
		token := s.token()
		if token == "" || !(token[0] >= '0' && token[0] <= '9' || token[0] == '.' || token[0] == '+' || token[0] == '-' || strings.EqualFold(token, "NaN") || strings.EqualFold(token, "Inf") || strings.EqualFold(token, "Infinity")) {
			s.pos = saved
			return 0, "", s.syntax()
		}
		if token == "+" || token == "-" || token == "." {
			s.pos = saved
			return 0, "", s.syntax()
		}
		if n < 100 {
			dst[n] = token
		}
		n++
		s.whitespace()
		if !s.take(',') {
			break
		}
	}
	end := s.pos
	if parens && !s.take(')') {
		return 0, "", s.syntax()
	}
	return n, s.text[start:end], nil
}

// normalizedCubeList is used only for mismatch errors, including lists too
// large for token scratch. Its input is an already-recognized list span.
func normalizedCubeList(ctx context.Context, text string) (string, error) {
	s := cubeScanner{text: text}
	var out strings.Builder
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		out.WriteString(s.token())
		if !s.take(',') {
			return out.String(), nil
		}
		out.WriteByte(',')
	}
}
func (d *Descriptor) cubeInput(ctx context.Context, text string) (Value, error) {
	s := cubeScanner{text: text}
	bracket := s.take('[')
	parens := s.take('(')
	if bracket && !parens {
		return Value{}, s.syntax()
	}
	var tokens [200]string
	n, first, err := s.list(ctx, tokens[:100], parens)
	if err != nil {
		return Value{}, err
	}
	box := parens && s.take(',')
	m := 0
	var second string
	if box {
		if !s.take('(') {
			return Value{}, s.syntax()
		}
		m, second, err = s.list(ctx, tokens[100:], true)
		if err != nil {
			return Value{}, err
		}
	} else if bracket {
		return Value{}, s.syntax()
	}
	// The bracket is part of the box production, unlike any trailing garbage.
	// Finish that production before mismatch, limit, or float diagnostics.
	if bracket && !s.take(']') {
		return Value{}, s.syntax()
	}
	if box {
		if n != m {
			first, err = normalizedCubeList(ctx, first)
			if err != nil {
				return Value{}, err
			}
			second, err = normalizedCubeList(ctx, second)
			if err != nil {
				return Value{}, err
			}
			return Value{}, cubeSyntax(fmt.Sprintf("Different point dimensions in (%s) and (%s).", first, second))
		}
	}
	if n > 100 {
		return Value{}, cubeSyntax("A cube cannot have more than 100 dimensions.")
	}
	// Conversion follows the released semantic action, before suffix validation.
	var scratch [200]float64
	corners := 1
	if box {
		corners = 2
	}
	for corner := range corners {
		for i := range n {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			number, err := (&Descriptor{id: Float8}).floatInput(tokens[corner*100+i])
			if err != nil {
				return Value{}, err
			}
			scratch[corner*n+i] = number.floating
		}
	}
	point := true
	if box {
		for i := range n {
			if scratch[i] != scratch[n+i] {
				point = false
				break
			}
		}
	}
	s.whitespace()
	if s.pos != len(text) {
		return Value{}, s.syntax()
	}
	count := n
	if !point {
		count *= 2
	}
	coordinates := make([]float64, count)
	copy(coordinates, scratch[:count])
	return Value{typ: d, cube: &cubePayload{dimension: n, point: point, coordinates: coordinates}}, nil
}
func (d *Descriptor) cubeOutput(ctx context.Context, v Value, env OutputEnvironment) (string, error) {
	if v.typ != d || v.null || !v.validCube() {
		return "", fmt.Errorf("invalid cube payload")
	}
	var out strings.Builder
	corners := 1
	if !v.cube.point {
		corners = 2
	}
	for corner := range corners {
		if corner > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('(')
		for i := range v.cube.dimension {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if i > 0 {
				out.WriteString(", ")
			}
			out.WriteString(floatOutputWithEnvironment(v.cube.coordinates[corner*v.cube.dimension+i], Float8, env))
		}
		out.WriteByte(')')
	}
	return out.String(), nil
}
