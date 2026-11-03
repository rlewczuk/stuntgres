package sqltype

import (
	"context"
	"fmt"
	"math"
)

type FTSRankFunction struct{ Density, Weighted, Normalized bool }

func (o FTSRankFunction) Name() string {
	if o.Density {
		return "ts_rank_cd"
	}
	return "ts_rank"
}
func (o FTSRankFunction) Arity() int {
	n := 2
	if o.Weighted {
		n++
	}
	if o.Normalized {
		n++
	}
	return n
}
func (o FTSRankFunction) Formal(i int) ID {
	if o.Weighted {
		if i == 0 {
			return 1021
		}
		i--
	}
	switch i {
	case 0:
		return TSVector
	case 1:
		return TSQuery
	case 2:
		if o.Normalized {
			return Int4
		}
	}
	return 0
}

type FTSRankPlan struct {
	operation FTSRankFunction
	inputs    [4]*Descriptor
	result    *Descriptor
}

func (p FTSRankPlan) Operation() FTSRankFunction { return p.operation }

func rankCompanion(vector *Descriptor, id ID) *Descriptor {
	if vector == nil {
		return nil
	}
	switch id {
	case TSVector:
		return vector
	case TSQuery:
		return vector.ftsQuery
	case Int4:
		return vector.ftsDistance
	case Float4:
		return vector.ftsReal
	case 1021:
		return vector.ftsRealArray
	case Text:
		return vector.ftsText
	case Regconfig:
		return vector.ftsConfig
	}
	return nil
}
func BindFTSRank(op FTSRankFunction, result Usage, args [4]Usage) (FTSRankPlan, error) {
	if args[0].Type == nil {
		return FTSRankPlan{}, fmt.Errorf("missing rank authority")
	}
	vector := args[0].Type.ftsVector
	if vector == nil || vector.id != TSVector || result.Type == nil || result.Type != vector.ftsReal || result != result.Type.Usage() {
		return FTSRankPlan{}, fmt.Errorf("foreign rank result authority")
	}
	p := FTSRankPlan{operation: op, result: result.Type}
	for i, arg := range args {
		if i >= op.Arity() {
			if arg.Type != nil {
				return FTSRankPlan{}, fmt.Errorf("extra rank operand")
			}
			continue
		}
		expected := rankCompanion(vector, op.Formal(i))
		if expected == nil || arg.Type != expected || arg.Typmod != expected.Usage().Typmod || !arg.Valid() {
			return FTSRankPlan{}, fmt.Errorf("foreign rank operand authority")
		}
		if expected.id == 1021 && expected.element != vector.ftsReal {
			return FTSRankPlan{}, fmt.Errorf("foreign rank array element authority")
		}
		p.inputs[i] = expected
	}
	return p, nil
}
func (p FTSRankPlan) ValidateValues(ctx context.Context, values [4]Value) error {
	if p.result == nil {
		return fmt.Errorf("invalid rank plan")
	}
	var usages [4]Usage
	for i := range p.operation.Arity() {
		v := values[i]
		if v.typ == nil || v.typ != p.inputs[i] {
			return fmt.Errorf("foreign rank value authority")
		}
		if !v.null {
			if IsFTS(v.typ.id) && !v.validFTS() {
				return fmt.Errorf("invalid rank FTS payload")
			}
			if v.typ.id == 1021 {
				if v.array == nil {
					return fmt.Errorf("invalid rank array payload")
				}
				product := int64(1)
				if len(v.array.dimensions) == 0 {
					product = 0
				}
				if len(v.array.dimensions) > 6 {
					return fmt.Errorf("invalid rank array rank")
				}
				for _, dim := range v.array.dimensions {
					if dim.Length <= 0 || int64(dim.Lower)+int64(dim.Length)-1 >= 1<<31-1 || product > int64(1<<31-1)/int64(dim.Length) {
						return fmt.Errorf("invalid rank array dimensions")
					}
					product *= int64(dim.Length)
				}
				if product != int64(len(v.array.elements)) {
					return fmt.Errorf("invalid rank array shape")
				}
				for _, element := range v.array.elements {
					if err := ctx.Err(); err != nil {
						return err
					}
					if element.typ != v.typ.element {
						return fmt.Errorf("foreign rank array element")
					}
				}
			}
		}
		usages[i] = v.typ.Usage()
	}
	checked, err := BindFTSRank(p.operation, p.result.Usage(), usages)
	if err != nil || checked != p {
		return fmt.Errorf("invalid rank plan authority")
	}
	return ctx.Err()
}
func (p FTSRankPlan) Evaluate(ctx context.Context, values [4]Value) (Value, error) {
	if err := p.ValidateValues(ctx, values); err != nil {
		return Value{}, err
	}
	for _, v := range values[:p.operation.Arity()] {
		if v.null {
			return Null(p.result), ctx.Err()
		}
	}
	weights := [4]float32{.1, .2, .4, 1}
	vectorIndex := 0
	if p.operation.Weighted {
		array := values[0].array
		if len(array.dimensions) != 1 {
			return Value{}, failure("2202E", "array of weight must be one-dimensional")
		}
		if len(array.elements) < 4 {
			return Value{}, failure("2202E", "array of weight is too short")
		}
		for _, element := range array.elements {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			if element.null {
				return Value{}, failure("22004", "array of weight must not contain nulls")
			}
		}
		for i := range weights {
			w := float32(array.elements[i].floating)
			if w >= 0 {
				if w > 1 {
					return Value{}, failure("22023", "weight out of range")
				}
				weights[i] = w
			}
		}
		vectorIndex = 1
	}
	var normalization int32
	if p.operation.Normalized {
		normalization = int32(values[p.operation.Arity()-1].integer)
	}
	value, err := values[vectorIndex].ftsVector.Rank(ctx, values[vectorIndex+1].ftsQuery, weights, normalization, p.operation.Density)
	if err != nil {
		return Value{}, err
	}
	return float4FromBits(p.result, math.Float32bits(value)), nil
}
