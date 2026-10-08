package sqltype

import (
	"context"
	"fmt"
	"math"
)

// BinaryPlan implements only the selected immutable raw-octet signatures.
type BinaryPlan struct {
	name   string
	args   [4]Usage
	arity  int
	result Usage
}

func BindBinary(name string, result Usage, args ...Usage) (BinaryPlan, error) {
	p := BinaryPlan{name: name, result: result, arity: len(args)}
	ids, arity, id := [4]ID{Bytea, Bytea}, 2, Bytea
	switch name {
	case "length", "octet_length", "bit_length":
		arity, id = 1, Int4
	case "||", "btrim", "ltrim", "rtrim":
	case "position":
		id = Int4
	case "substring", "substr":
		ids, arity = [4]ID{Bytea, Int4, Int4}, 2
		if len(args) == 3 {
			arity = 3
		}
	case "overlay":
		ids, arity = [4]ID{Bytea, Bytea, Int4, Int4}, 3
		if len(args) == 4 {
			arity = 4
		}
	default:
		return p, fmt.Errorf("invalid binary operation")
	}
	if len(args) != arity {
		return p, fmt.Errorf("invalid binary arity")
	}
	for i, u := range args {
		if !u.Valid() || u != u.Type.Usage() || u.Type.ID() != ids[i] {
			return p, fmt.Errorf("invalid binary argument usage")
		}
		for j := range i {
			if ids[j] == ids[i] && u.Type != args[j].Type {
				return p, fmt.Errorf("mismatched binary descriptor owner")
			}
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

func (p BinaryPlan) Evaluate(ctx context.Context, values ...Value) (Value, error) {
	if p.result.Type == nil || len(values) != p.arity {
		return Value{}, fmt.Errorf("invalid binary values")
	}
	for i, v := range values {
		if v.typ != p.args[i].Type {
			return Value{}, fmt.Errorf("invalid binary values")
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	for _, v := range values {
		if v.null {
			return Null(p.result.Type), nil
		}
	}
	a := values[0]
	if p.name == "substring" || p.name == "substr" {
		start := values[1].integer
		end := int64(len(a.text)) + 1
		if p.arity == 3 {
			count := values[2].integer
			if count < 0 {
				return Value{}, failure("22011", "negative substring length not allowed")
			}
			sum := start + count
			if sum >= math.MinInt32 && sum <= math.MaxInt32 {
				end = sum
			}
		}
		lo := min(max(start, 1)-1, int64(len(a.text)))
		hi := min(max(end-1, lo), int64(len(a.text)))
		return Binary(p.result.Type, a.text[lo:hi])
	}
	if p.name == "overlay" {
		start := values[2].integer
		if start <= 0 {
			return Value{}, failure("22011", "negative substring length not allowed")
		}
		count := int64(len(values[1].text))
		if p.arity == 4 {
			count = values[3].integer
		}
		end := start + count
		if end < math.MinInt32 || end > math.MaxInt32 {
			return Value{}, failure("22003", "integer out of range")
		}
		lo := min(start-1, int64(len(a.text)))
		hi := min(max(end, 1)-1, int64(len(a.text)))
		out := a.text[:lo] + values[1].text + a.text[hi:]
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		return Binary(p.result.Type, out)
	}
	if p.name == "position" {
		index, err := binaryPosition(ctx, a.text, values[1].text)
		if err != nil {
			return Value{}, err
		}
		return Integer(p.result.Type, int64(index+1))
	}
	if p.arity == 1 {
		return binaryLength(p.result.Type, int64(len(a.text)), p.name == "bit_length")
	}
	b := values[1]
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
