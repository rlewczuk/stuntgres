package sqltype

import "fmt"

func pointSyntax(text string) error {
	return failure("22P02", fmt.Sprintf("invalid input syntax for type point: \"%s\"", text))
}

func (d *Descriptor) pointInput(text string) (Value, error) {
	s := geometryScanner{text: text}
	s.whitespace()
	paren := s.take('(')
	x, err := s.number(pointSyntax)
	if err != nil {
		return Value{}, err
	}
	if !s.take(',') {
		return Value{}, pointSyntax(text)
	}
	y, err := s.number(pointSyntax)
	if err != nil {
		return Value{}, err
	}
	if paren && !s.take(')') {
		return Value{}, pointSyntax(text)
	}
	s.whitespace()
	if s.pos != len(text) {
		return Value{}, pointSyntax(text)
	}
	return pointValue(d, x, y), nil
}

func pointFetch(v Value, bounds []ArraySubscriptValue) (Value, error) {
	d := v.typ.BaseType()
	if v.null || len(bounds) != 1 || bounds[0].UpperNull || bounds[0].Upper < 0 || bounds[0].Upper > 1 {
		return Null(d.pointFloat), nil
	}
	x, y := pointCoordinates(v)
	if bounds[0].Upper == 1 {
		x = y
	}
	return Float(d.pointFloat, x)
}

func pointSet(v Value, bounds []ArraySubscriptValue, rhs Value, slice bool) (Value, error) {
	d := v.typ.BaseType()
	if v.null || rhs.null {
		v.typ = d
		return v, nil
	}
	if slice {
		return Value{}, failure("0A000", "updates on slices of fixed-length arrays not implemented")
	}
	if len(bounds) != 1 {
		return Value{}, failure("2202E", "wrong number of array subscripts")
	}
	index := bounds[0].Upper
	if index < 0 || index > 1 {
		return Value{}, failure("2202E", "array subscript out of range")
	}
	if rhs.typ != d.pointFloat {
		return Value{}, fmt.Errorf("invalid point coordinate assignment")
	}
	x, y := pointCoordinates(v)
	if index == 0 {
		x = rhs.floating
	} else {
		y = rhs.floating
	}
	return pointValue(d, x, y), nil
}
