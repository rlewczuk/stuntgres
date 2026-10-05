package sqltype

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func isFloat(id ID) bool             { return id == Float4 || id == Float8 }
func numericFloatOperand(id ID) bool { return isFloat(id) || id == Int4 || id == Int8 }

// Float imports an already binary value; textual float4 input instead parses
// directly at 32 bits to avoid decimal double rounding.
func Float(d *Descriptor, n float64) (Value, error) {
	if d == nil || !isFloat(d.id) {
		return Value{}, fmt.Errorf("invalid float descriptor")
	}
	if d.id == Float4 {
		rounded := float64(float32(n))
		if !math.IsInf(n, 0) && math.IsInf(rounded, 0) {
			return Value{}, failure("22003", "value out of range: overflow")
		}
		if n != 0 && rounded == 0 {
			return Value{}, failure("22003", "value out of range: underflow")
		}
		n = rounded
	}
	return Value{typ: d, floating: n}, nil
}
func (v Value) Float() (float64, error) {
	if v.null || v.typ == nil || !isFloat(v.typ.id) {
		return 0, fmt.Errorf("not a float value")
	}
	return v.floating, nil
}
func floatOperand(v Value) float64 {
	if isFloat(v.typ.id) {
		return v.floating
	}
	return float64(v.integer)
}
func compareFloats(a, b float64) int {
	if math.IsNaN(a) {
		if math.IsNaN(b) {
			return 0
		}
		return 1
	}
	if math.IsNaN(b) {
		return -1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func (d *Descriptor) floatInput(original string) (Value, error) {
	name, size := "real", 32
	if d.id == Float8 {
		name, size = "double precision", 64
	}
	invalid := func() (Value, error) {
		return Value{}, failure("22P02", fmt.Sprintf("invalid input syntax for type %s: \"%s\"", name, original))
	}
	s := original
	for len(s) > 0 && space(s[0]) {
		s = s[1:]
	}
	special := trim(s)
	switch {
	case strings.EqualFold(special, "NaN"):
		return Value{typ: d, floating: math.NaN()}, nil
	case strings.EqualFold(special, "Infinity"), strings.EqualFold(special, "+Infinity"), strings.EqualFold(special, "Inf"), strings.EqualFold(special, "+Inf"):
		return Value{typ: d, floating: math.Inf(1)}, nil
	case strings.EqualFold(special, "-Infinity"), strings.EqualFold(special, "-Inf"):
		return Value{typ: d, floating: math.Inf(-1)}, nil
	}
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits, nonzero := 0, false
	scan := func() {
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			digits++
			nonzero = nonzero || s[i] != '0'
			i++
		}
	}
	scan()
	if i < len(s) && s[i] == '.' {
		i++
		scan()
	}
	if digits == 0 {
		return invalid()
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		end := i
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if start == i {
			i = end
		}
	}
	prefix := s[:i]
	n, err := strconv.ParseFloat(prefix, size)
	if math.IsInf(n, 0) || n == 0 && nonzero {
		return Value{}, failure("22003", fmt.Sprintf("\"%s\" is out of range for type %s", prefix, name))
	}
	if err != nil || trim(s[i:]) != "" {
		return invalid()
	}
	return Value{typ: d, floating: n}, nil
}
