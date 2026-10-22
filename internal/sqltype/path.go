package sqltype

import (
	"fmt"
	"math"
	"strings"
)

// A published path owns its nonempty point slice. Value copies share this
// immutable payload; neither coordinates nor closure are normalized on input.
type pathPayload struct {
	closed bool
	points [][2]float64
}

func (d *Descriptor) pathInput(text string) (Value, error) {
	invalid := func() error { return failure("22P02", fmt.Sprintf("invalid input syntax for type path: \"%s\"", text)) }
	commas := strings.Count(text, ",")
	if commas == 0 || commas%2 == 0 {
		return Value{}, invalid()
	}
	s := cubeScanner{text: text}
	s.whitespace()
	// path_in's single leading parenthesis is a separate shallow wrapper
	// around path_decode's optional bracket/parenthesis-delimited list.
	inputWrapper := s.pos < len(text) && text[s.pos] == '(' && strings.IndexByte(text[s.pos+1:], '(') < 0
	if inputWrapper {
		s.pos++
		s.whitespace()
	}
	outer, open := false, false
	if s.take('[') {
		outer, open = true, true
	} else if s.pos < len(text) && text[s.pos] == '(' {
		next := s.pos + 1
		for next < len(text) && space(text[next]) {
			next++
		}
		if next < len(text) && text[next] == '(' {
			outer, s.pos = true, next
		}
	}
	points := make([][2]float64, (commas+1)/2)
	number := func() (float64, error) {
		numeric := geometryScanner{text: text, pos: s.pos}
		n, err := numeric.number(func(string) error { return invalid() })
		if err != nil {
			return 0, err
		}
		s.pos = numeric.pos
		return n, nil
	}
	for i := range points {
		paren := s.take('(')
		var err error
		points[i][0], err = number()
		if err != nil {
			return Value{}, err
		}
		if !s.take(',') {
			return Value{}, invalid()
		}
		points[i][1], err = number()
		if err != nil {
			return Value{}, err
		}
		if paren && !s.take(')') {
			return Value{}, invalid()
		}
		s.take(',')
	}
	// Released path_decode accepts ')' even for an open '[' outer list.
	if outer && !s.take(')') && !(open && s.take(']')) {
		return Value{}, invalid()
	}
	if inputWrapper && !s.take(')') {
		return Value{}, invalid()
	}
	s.whitespace()
	if s.pos != len(text) {
		return Value{}, invalid()
	}
	return Value{typ: d, path: &pathPayload{closed: !open, points: points}}, nil
}

func pathOutput(v Value, env OutputEnvironment) (string, error) {
	return formatPath(v, env, false)
}

// Exact logical persistence differs from SQL display only for signed NaNs.
// The geometric input scanner admits their sign; canonical float output hides it.
func formatPath(v Value, env OutputEnvironment, exact bool) (string, error) {
	if v.path == nil || len(v.path.points) == 0 {
		return "", fmt.Errorf("missing PATH payload")
	}
	var b strings.Builder
	if v.path.closed {
		b.WriteByte('(')
	} else {
		b.WriteByte('[')
	}
	for i, p := range v.path.points {
		if i != 0 {
			b.WriteByte(',')
		}
		b.WriteByte('(')
		b.WriteString(pathCoordinateOutput(p[0], env, exact))
		b.WriteByte(',')
		b.WriteString(pathCoordinateOutput(p[1], env, exact))
		b.WriteByte(')')
	}
	if v.path.closed {
		b.WriteByte(')')
	} else {
		b.WriteByte(']')
	}
	return b.String(), nil
}

func pathCoordinateOutput(n float64, env OutputEnvironment, exact bool) string {
	if exact && math.IsNaN(n) && math.Signbit(n) {
		return "-NaN"
	}
	return floatOutputWithEnvironment(n, Float8, env)
}

func samePath(a, b Value) bool {
	if a.path == nil || b.path == nil || a.path.closed != b.path.closed || len(a.path.points) != len(b.path.points) {
		return false
	}
	for i, p := range a.path.points {
		for j, n := range p {
			if math.Float64bits(n) != math.Float64bits(b.path.points[i][j]) {
				return false
			}
		}
	}
	return true
}

func pathPredicate(op ComparisonOperator, a, b Value) (bool, error) {
	if a.path == nil || b.path == nil || len(a.path.points) == 0 || len(b.path.points) == 0 {
		return false, fmt.Errorf("missing PATH payload")
	}
	x, y := len(a.path.points), len(b.path.points)
	switch op {
	case CompareEqual:
		return x == y, nil
	case CompareLess:
		return x < y, nil
	case CompareLessEqual:
		return x <= y, nil
	case CompareGreater:
		return x > y, nil
	case CompareGreaterEqual:
		return x >= y, nil
	}
	return false, failure("42883", "incompatible comparison operands")
}
