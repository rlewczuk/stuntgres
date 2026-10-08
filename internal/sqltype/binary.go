package sqltype

import (
	"context"
	"fmt"
	"math"
)

// BinaryPlan implements only the selected immutable raw-octet signatures.
type BinaryPlan struct {
	name   string
	args   [2]Usage
	arity  int
	result Usage
}

func BindBinary(name string, result Usage, args ...Usage) (BinaryPlan, error) {
	p := BinaryPlan{name: name, result: result, arity: len(args)}
	arity, id := 2, Bytea
	switch name {
	case "length", "octet_length", "bit_length":
		arity, id = 1, Int4
	case "||", "btrim", "ltrim", "rtrim":
	default:
		return p, fmt.Errorf("invalid binary operation")
	}
	if len(args) != arity {
		return p, fmt.Errorf("invalid binary arity")
	}
	for i, u := range args {
		if !u.Valid() || u != u.Type.Usage() || u.Type.ID() != Bytea {
			return p, fmt.Errorf("invalid binary argument usage")
		}
		if i > 0 && u.Type != args[0].Type {
			return p, fmt.Errorf("mismatched binary descriptor owner")
		}
		p.args[i] = u
	}
	if !result.Valid() || result != result.Type.Usage() || result.Type.ID() != id {
		return p, fmt.Errorf("invalid binary result usage")
	}
	if id == Bytea && result.Type != args[0].Type {
		return p, fmt.Errorf("mismatched binary result owner")
	}
	return p, nil
}
func (p BinaryPlan) Name() string       { return p.name }
func (p BinaryPlan) ResultUsage() Usage { return p.result }

func binaryLength(d *Descriptor, count int64, bits bool) (Value, error) {
	if bits {
		if count > math.MaxInt32/8 {
			return Value{}, failure("22003", "integer out of range")
		}
		count *= 8
	}
	return Integer(d, count)
}

func (p BinaryPlan) Evaluate(ctx context.Context, a, b Value) (Value, error) {
	if p.result.Type == nil || a.typ != p.args[0].Type || (p.arity == 2 && b.typ != p.args[1].Type) {
		return Value{}, fmt.Errorf("invalid binary values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || (p.arity == 2 && b.null) {
		return Null(p.result.Type), nil
	}
	if p.arity == 1 {
		return binaryLength(p.result.Type, int64(len(a.text)), p.name == "bit_length")
	}
	if p.name == "||" {
		if len(a.text) == 0 {
			return b, nil
		}
		if len(b.text) == 0 {
			return a, nil
		}
		if len(a.text) > int(^uint(0)>>1)-len(b.text) {
			return Value{}, fmt.Errorf("binary concatenation size overflow")
		}
		return Binary(p.result.Type, a.text+b.text)
	}
	if len(a.text) == 0 || len(b.text) == 0 {
		return a, nil
	}
	var set [256]bool
	for i := range len(b.text) {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		set[b.text[i]] = true
	}
	start, end := 0, len(a.text)
	if p.name != "rtrim" {
		for start < end && set[a.text[start]] {
			if start&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			start++
		}
	}
	if p.name != "ltrim" {
		for end > start && set[a.text[end-1]] {
			if end&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
			}
			end--
		}
	}
	if start == 0 && end == len(a.text) {
		return a, nil
	}
	return Binary(p.result.Type, a.text[start:end])
}
