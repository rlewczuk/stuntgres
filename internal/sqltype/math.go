package sqltype

import (
	"context"
	"fmt"
	"math"
)

// MathPlan is an immutable provider for the selected exact SQL signatures.
// Usages retain their catalog owner; results never inherit an argument typmod.
type MathPlan struct {
	name   string
	args   [2]Usage
	arity  int
	result Usage
}

func BindMath(name string, result Usage, args ...Usage) (MathPlan, error) {
	p := MathPlan{name: name, arity: len(args), result: result}
	if len(args) < 1 || len(args) > 2 {
		return p, fmt.Errorf("invalid math signature")
	}
	for i, u := range args {
		if !u.Valid() || u != u.Type.Usage() {
			return p, fmt.Errorf("invalid math argument usage")
		}
		p.args[i] = u
	}
	id := args[0].Type.id
	valid := false
	switch name {
	case "scale", "min_scale", "trim_scale":
		valid = len(args) == 1 && id == Numeric
	case "round", "trunc":
		valid = (len(args) == 1 && (id == Numeric || id == Float8)) || (len(args) == 2 && id == Numeric && args[1].Type.id == Int4)
	case "sign":
		valid = len(args) == 1 && (id == Numeric || id == Float8)
	case "mod":
		valid = len(args) == 2 && (id == Int2 || id == Int4 || id == Int8 || id == Numeric) && args[1] == args[0]
	}
	if !valid {
		return p, fmt.Errorf("invalid math signature")
	}
	resultID := id
	if name == "scale" || name == "min_scale" {
		resultID = Int4
	}
	if !result.Valid() || result != result.Type.Usage() || result.Type.id != resultID {
		return p, fmt.Errorf("invalid math result usage")
	}
	if resultID == id && result.Type != args[0].Type {
		return p, fmt.Errorf("mismatched math descriptor owner")
	}
	return p, nil
}

func (p MathPlan) Name() string { return p.name }

func (p MathPlan) ResultUsage() Usage { return p.result }

func (p MathPlan) Evaluate(ctx context.Context, a, b Value) (Value, error) {
	if p.result.Type == nil || a.typ != p.args[0].Type || (p.arity == 2 && b.typ != p.args[1].Type) {
		return Value{}, fmt.Errorf("invalid math values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || (p.arity == 2 && b.null) {
		return Null(p.result.Type), nil
	}
	id := a.typ.id
	if p.name == "mod" {
		if id == Numeric {
			return numericModulo(ctx, a, b)
		}
		if b.integer == 0 {
			return Value{}, failure("22012", "division by zero")
		}
		n := int64(0)
		if b.integer != -1 {
			n = a.integer % b.integer
		}
		return Integer(p.result.Type, n)
	}
	if id == Float8 {
		n := a.floating
		switch p.name {
		case "round":
			n = math.RoundToEven(n)
		case "trunc":
			n = math.Trunc(n)
		case "sign":
			if n > 0 {
				n = 1
			} else if n < 0 {
				n = -1
			} else {
				n = 0
			}
		}
		return Float(p.result.Type, n)
	}
	switch p.name {
	case "scale", "min_scale":
		if a.special != numericFinite {
			return Null(p.result.Type), nil
		}
		n := a.scale
		if p.name == "min_scale" {
			n = numericMinimumScale(a)
		}
		return Integer(p.result.Type, n)
	case "trim_scale":
		if a.special == numericFinite {
			a.scale = numericMinimumScale(a)
		}
		return a, nil
	case "sign":
		if a.special == numericNaN {
			return a, nil
		}
		n := "1"
		negative := a.negative || a.special == numericNegativeInfinity
		if a.special == numericFinite && a.text == "" {
			n = ""
			negative = false
		}
		return Value{typ: a.typ, text: n, negative: negative}, nil
	case "round", "trunc":
		if a.special != numericFinite {
			return a, nil
		}
		scale := int64(0)
		if p.arity == 2 {
			scale = b.integer
		}
		lower := int64(-131072)
		if p.name == "round" {
			lower--
		}
		scale = max(lower, min(scale, 16383))
		if p.name == "round" {
			a = roundNumeric(a, scale)
		} else {
			a = truncateNumeric(a, scale)
		}
		if int64(len(a.text))+a.exponent > 131072 {
			return Value{}, failure("22003", "value overflows numeric format")
		}
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		return a, nil
	}
	return Value{}, fmt.Errorf("invalid math operation")
}

func numericMinimumScale(v Value) int64 {
	if v.text == "" {
		return 0
	}
	return max(-v.exponent, 0)
}

func truncateNumeric(v Value, scale int64) Value {
	v.scale = max(scale, 0)
	cut := int64(len(v.text)) + v.exponent + scale
	if cut >= int64(len(v.text)) {
		return v
	}
	if cut <= 0 {
		v.text = ""
		v.exponent = 0
	} else {
		v.text = v.text[:cut]
		v.exponent = -scale
	}
	return normalizeNumeric(v)
}
