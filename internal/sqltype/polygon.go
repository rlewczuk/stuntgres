package sqltype

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// The slice is privately owned at input and is never modified or exposed.
// Copying a Value or a retained snapshot shares this immutable payload.
type polygonPoint struct{ x, y float64 }
type polygonPayload struct{ points []polygonPoint }

func (v Value) validPolygon() bool { return v.polygon != nil && len(v.polygon.points) != 0 }

func (v Value) samePolygon(other Value) bool {
	if !v.validPolygon() || !other.validPolygon() || len(v.polygon.points) != len(other.polygon.points) {
		return false
	}
	for i, p := range v.polygon.points {
		q := other.polygon.points[i]
		if math.Float64bits(p.x) != math.Float64bits(q.x) || math.Float64bits(p.y) != math.Float64bits(q.y) {
			return false
		}
	}
	return true
}

func (d *Descriptor) polygonInput(ctx context.Context, text string) (Value, error) {
	invalid := func() error {
		return failure("22P02", fmt.Sprintf("invalid input syntax for type polygon: \"%s\"", text))
	}
	// Released pair_count precedes float conversion, including range failures.
	commas := 0
	for i := range len(text) {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		if text[i] == ',' {
			commas++
		}
	}
	if commas&1 == 0 {
		return Value{}, invalid()
	}
	n := commas/2 + 1
	if n > int(^uint(0)>>1)/16 {
		return Value{}, invalid()
	}
	s := geometryScanner{text: text}
	s.whitespace()
	outer := false
	if s.pos < len(text) && text[s.pos] == '(' {
		next := s.pos + 1
		for next < len(text) && space(text[next]) {
			next++
		}
		if next < len(text) && text[next] == '(' || strings.LastIndexByte(text, '(') == s.pos {
			outer, s.pos = true, next
		}
	}
	points := make([]polygonPoint, n)
	number := func() (float64, error) {
		return s.number(func(string) error { return invalid() })
	}
	for i := range points {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		s.whitespace()
		paren := s.take('(')
		var err error
		points[i].x, err = number()
		if err != nil {
			return Value{}, err
		}
		if !s.take(',') {
			return Value{}, invalid()
		}
		points[i].y, err = number()
		if err != nil {
			return Value{}, err
		}
		if paren && !s.take(')') {
			return Value{}, invalid()
		}
		s.whitespace()
		s.take(',')
	}
	if outer && !s.take(')') {
		return Value{}, invalid()
	}
	s.whitespace()
	if s.pos != len(text) {
		return Value{}, invalid()
	}
	return Value{typ: d, polygon: &polygonPayload{points: points}}, nil
}

func (d *Descriptor) polygonOutput(ctx context.Context, v Value, env OutputEnvironment) (string, error) {
	return d.formatPolygon(ctx, v, env, false)
}

// Exact logical persistence retains NaN signs hidden by canonical display.
func (d *Descriptor) formatPolygon(ctx context.Context, v Value, env OutputEnvironment, exact bool) (string, error) {
	if v.typ != d || v.null || !v.validPolygon() {
		return "", fmt.Errorf("invalid polygon payload")
	}
	var out strings.Builder
	out.Grow(len(v.polygon.points) * 8)
	out.WriteByte('(')
	for i, p := range v.polygon.points {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i != 0 {
			out.WriteByte(',')
		}
		out.WriteByte('(')
		out.WriteString(polygonCoordinateOutput(p.x, env, exact))
		out.WriteByte(',')
		out.WriteString(polygonCoordinateOutput(p.y, env, exact))
		out.WriteByte(')')
	}
	out.WriteByte(')')
	return out.String(), nil
}

func polygonCoordinateOutput(n float64, env OutputEnvironment, exact bool) string {
	if exact && math.IsNaN(n) && math.Signbit(n) {
		return "-NaN"
	}
	return floatOutputWithEnvironment(n, Float8, env)
}
