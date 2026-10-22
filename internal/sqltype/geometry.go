package sqltype

import (
	"fmt"
	"math"
	"strings"

	"stuntgres/internal/sqlstate"
)

// BOX payloads are immutable and retain raw pairs, including the deliberately
// noncanonical result of component writes. POINT is inline: x is floating and
// y's unmodified binary64 bits occupy integer, avoiding allocation on fetch.
type geometryPayload struct{ coordinates [4]float64 }

func pointValue(d *Descriptor, x, y float64) Value {
	return Value{typ: d, floating: x, integer: int64(math.Float64bits(y))}
}

func pointCoordinates(v Value) (float64, float64) {
	return v.floating, math.Float64frombits(uint64(v.integer))
}

func (d *Descriptor) SubscriptElement() *Descriptor {
	if d.id == Box {
		return d.boxPoint
	}
	return d.element
}

func (v Value) sameGeometry(other Value) bool {
	if v.geometry == nil || other.geometry == nil {
		return v.geometry == other.geometry
	}
	for i, n := range v.geometry.coordinates {
		if math.Float64bits(n) != math.Float64bits(other.geometry.coordinates[i]) {
			return false
		}
	}
	return true
}

func (d *Descriptor) geometryInput(text string, env InputEnvironment) (Value, error) {
	s := cubeScanner{text: text}
	invalid := func() error {
		return failure("22P02", fmt.Sprintf("invalid input syntax for type %s: \"%s\"", d.name, text))
	}
	var coordinates [4]float64
	number := func() (float64, error) {
		token := s.token()
		f := Descriptor{id: Float8}
		v, err := f.floatInput(token)
		if err != nil {
			if e, ok := err.(*sqlstate.Error); ok && e.Code == "22P02" {
				return 0, invalid()
			}
			return 0, err
		}
		s.whitespace()
		return v.floating, nil
	}
	pair := func(offset int) error {
		paren := s.take('(')
		var err error
		coordinates[offset], err = number()
		if err != nil {
			return err
		}
		if !s.take(',') {
			return invalid()
		}
		coordinates[offset+1], err = number()
		if err != nil {
			return err
		}
		if paren && !s.take(')') {
			return invalid()
		}
		s.whitespace()
		return nil
	}
	outer := false
	open := false
	if d.id == Box || d.id == Line {
		s.whitespace()
		if d.id == Line && s.take('[') {
			outer, open = true, true
		} else if s.pos < len(text) && text[s.pos] == '(' {
			next := s.pos + 1
			for next < len(text) && space(text[next]) {
				next++
			}
			if next < len(text) && text[next] == '(' || strings.LastIndexByte(text, '(') == s.pos {
				outer, s.pos = true, next
			}
		}
	}
	if err := pair(0); err != nil {
		return Value{}, err
	}
	if d.id == Box || d.id == Line {
		s.take(',')
		if err := pair(2); err != nil {
			return Value{}, err
		}
		s.take(',')
		// Released path_decode accepts ')' at any outer depth, including
		// an open '[' path; ']' is admitted only for that open path.
		if outer && !s.take(')') && !(open && s.take(']')) {
			return Value{}, invalid()
		}
	}
	s.whitespace()
	if s.pos != len(text) {
		return Value{}, invalid()
	}
	if d.id == Point {
		return pointValue(d, coordinates[0], coordinates[1]), nil
	}
	if d.id == Line {
		return d.lineFromPoints(coordinates)
	}
	if d.id == Box && !env.RawBoxReplay && !env.ExactSnapshot {
		for i := range 2 {
			if compareFloats(coordinates[i], coordinates[i+2]) < 0 {
				coordinates[i], coordinates[i+2] = coordinates[i+2], coordinates[i]
			}
		}
	}
	return Value{typ: d, geometry: &geometryPayload{coordinates: coordinates}}, nil
}

func (d *Descriptor) geometryOutput(v Value, env OutputEnvironment) (string, error) {
	if d.id == Point {
		x, y := pointCoordinates(v)
		return "(" + floatOutputWithEnvironment(x, Float8, env) + "," + floatOutputWithEnvironment(y, Float8, env) + ")", nil
	}
	if v.geometry == nil {
		return "", fmt.Errorf("missing geometry payload")
	}
	c := v.geometry.coordinates
	if d.id == Line {
		return "{" + floatOutputWithEnvironment(c[0], Float8, env) + "," + floatOutputWithEnvironment(c[1], Float8, env) + "," + floatOutputWithEnvironment(c[2], Float8, env) + "}", nil
	}
	pair := func(i int) string {
		return "(" + floatOutputWithEnvironment(c[i], Float8, env) + "," + floatOutputWithEnvironment(c[i+1], Float8, env) + ")"
	}
	out := pair(0)
	if d.id == Box {
		out += "," + pair(2)
	}
	return out, nil
}

func boxFuzzyEqual(a, b float64) bool { return a == b || math.Abs(a-b) <= 1e-6 }

func boxArea(v Value) (float64, error) {
	if v.geometry == nil {
		return 0, fmt.Errorf("missing BOX payload")
	}
	c := v.geometry.coordinates
	width, height := c[0]-c[2], c[1]-c[3]
	if math.IsInf(width, 0) && !math.IsInf(c[0], 0) && !math.IsInf(c[2], 0) || math.IsInf(height, 0) && !math.IsInf(c[1], 0) && !math.IsInf(c[3], 0) {
		return 0, failure("22003", "value out of range: overflow")
	}
	area := width * height
	if math.IsInf(area, 0) && !math.IsInf(width, 0) && !math.IsInf(height, 0) {
		return 0, failure("22003", "value out of range: overflow")
	}
	if area == 0 && width != 0 && height != 0 {
		return 0, failure("22003", "value out of range: underflow")
	}
	return area, nil
}

func boxPredicate(op ComparisonOperator, a, b Value) (bool, error) {
	x, err := boxArea(a)
	if err != nil {
		return false, err
	}
	y, err := boxArea(b)
	if err != nil {
		return false, err
	}
	switch op {
	case CompareEqual:
		return boxFuzzyEqual(x, y), nil
	case CompareLess:
		return x+1e-6 < y, nil
	case CompareLessEqual:
		return x <= y+1e-6, nil
	case CompareGreater:
		return x > y+1e-6, nil
	case CompareGreaterEqual:
		return x+1e-6 >= y, nil
	}
	return false, failure("42883", "incompatible comparison operands")
}

func boxSame(a, b Value) (bool, error) {
	if a.geometry == nil || b.geometry == nil {
		return false, fmt.Errorf("missing BOX payload")
	}
	for i, n := range a.geometry.coordinates {
		if !boxFuzzyEqual(n, b.geometry.coordinates[i]) {
			return false, nil
		}
	}
	return true, nil
}

func boxFetch(v Value, bounds []ArraySubscriptValue) (Value, error) {
	d := v.Type().BaseType()
	if v.null || len(bounds) != 1 || bounds[0].UpperNull || bounds[0].Upper < 0 || bounds[0].Upper > 1 {
		return Null(d.boxPoint), nil
	}
	if v.geometry == nil {
		return Value{}, fmt.Errorf("missing BOX payload")
	}
	i := int(bounds[0].Upper) * 2
	return pointValue(d.boxPoint, v.geometry.coordinates[i], v.geometry.coordinates[i+1]), nil
}

func boxSet(v Value, bounds []ArraySubscriptValue, rhs Value, slice bool) (Value, error) {
	if v.null || rhs.null {
		v.typ = v.typ.BaseType()
		return v, nil
	}
	if slice {
		return Value{}, failure("0A000", "updates on slices of fixed-length arrays not implemented")
	}
	if len(bounds) != 1 {
		return Value{}, failure("2202E", "wrong number of array subscripts")
	}
	i := bounds[0].Upper
	if i < 0 || i > 1 {
		return Value{}, failure("2202E", "array subscript out of range")
	}
	if v.geometry == nil || rhs.typ.BaseType().id != Point {
		return Value{}, fmt.Errorf("invalid BOX component assignment")
	}
	payload := *v.geometry
	payload.coordinates[i*2], payload.coordinates[i*2+1] = pointCoordinates(rhs)
	return Value{typ: v.typ.BaseType(), geometry: &payload}, nil
}
