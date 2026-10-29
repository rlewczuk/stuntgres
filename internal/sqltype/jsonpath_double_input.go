package sqltype

import (
	"context"
	"math"
	"strconv"
	"strings"
)

var jsonpathDoubleDescriptor = Descriptor{id: Float8}

// Only JSONPath double has the host strtod hex and extended-NaN contract.
// Ordinary decimal input remains owned by the Float8 codec.
func jsonpathDoubleInput(ctx context.Context, source string) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	start, end := 0, len(source)
	for start < end && space(source[start]) {
		if start%jsonbWorkChunk == 0 {
			if err := ctx.Err(); err != nil { return Value{}, err }
		}
		start++
	}
	for end > start && space(source[end-1]) {
		if end%jsonbWorkChunk == 0 {
			if err := ctx.Err(); err != nil { return Value{}, err }
		}
		end--
	}
	s := source[start:end]
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') { i++ }
	body := s[i:]
	if len(body) >= 3 && strings.EqualFold(body[:3], "nan") {
		valid := len(body) == 3
		if len(body) >= 5 && body[3] == '(' && body[len(body)-1] == ')' {
			valid = true
			for j := 4; j < len(body)-1; j++ {
				if j%jsonbWorkChunk == 0 {
					if err := ctx.Err(); err != nil { return Value{}, err }
				}
				c := body[j]
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') { valid = false; break }
			}
		}
		if valid { return Value{typ: &jsonpathDoubleDescriptor, floating: math.NaN()}, ctx.Err() }
	}
	if len(body) < 2 || body[0] != '0' || body[1] != 'x' && body[1] != 'X' {
		v, err := jsonpathDoubleDescriptor.floatInput(source)
		if canceled := ctx.Err(); canceled != nil { return Value{}, canceled }
		return v, err
	}
	invalid := func() (Value, error) { return Value{}, failure("22P02", "invalid JSONPath double input") }
	i += 2
	digits, nonzero, point := 0, false, false
	for i < len(s) {
		if i%jsonbWorkChunk == 0 {
			if err := ctx.Err(); err != nil { return Value{}, err }
		}
		c := s[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			digits++; nonzero = nonzero || c != '0'; i++; continue
		}
		if c == '.' && !point { point = true; i++; continue }
		break
	}
	if digits == 0 { return invalid() }
	exponent := i < len(s) && (s[i] == 'p' || s[i] == 'P')
	if exponent {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') { i++ }
		first := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			if i%jsonbWorkChunk == 0 {
				if err := ctx.Err(); err != nil { return Value{}, err }
			}
			i++
		}
		if first == i { return invalid() }
	}
	if i != len(s) { return invalid() }
	if !exponent { s += "p0" }
	f, err := strconv.ParseFloat(s, 64)
	if canceled := ctx.Err(); canceled != nil { return Value{}, canceled }
	if err != nil || math.IsInf(f, 0) || f == 0 && nonzero { return invalid() }
	return Value{typ: &jsonpathDoubleDescriptor, floating: f}, nil
}
