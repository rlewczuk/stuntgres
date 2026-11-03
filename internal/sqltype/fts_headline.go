package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/fts"
)

type FTSHeadlineFunction struct{ Explicit, Options bool }

func (o FTSHeadlineFunction) Arity() int {
	n := 2
	if o.Explicit {
		n++
	}
	if o.Options {
		n++
	}
	return n
}
func (o FTSHeadlineFunction) Formal(i int) ID {
	if o.Explicit {
		if i == 0 {
			return Regconfig
		}
		i--
	}
	if i == 1 {
		return TSQuery
	}
	if i == 0 || i == 2 && o.Options {
		return Text
	}
	return 0
}

type FTSHeadlinePlan struct {
	operation FTSHeadlineFunction
	inputs    [4]*Descriptor
	result    *Descriptor
}

func (p FTSHeadlinePlan) Operation() FTSHeadlineFunction { return p.operation }
func BindFTSHeadline(op FTSHeadlineFunction, result Usage, args [4]Usage) (FTSHeadlinePlan, error) {
	if args[0].Type == nil {
		return FTSHeadlinePlan{}, fmt.Errorf("missing headline authority")
	}
	vector := args[0].Type.ftsVector
	if vector == nil || result.Type == nil || result.Type != vector.ftsText || result.Typmod != result.Type.Usage().Typmod || !result.Valid() {
		return FTSHeadlinePlan{}, fmt.Errorf("foreign headline result authority")
	}
	p := FTSHeadlinePlan{operation: op, result: result.Type}
	for i, arg := range args {
		if i >= op.Arity() {
			if arg.Type != nil {
				return FTSHeadlinePlan{}, fmt.Errorf("extra headline operand")
			}
			continue
		}
		expected := rankCompanion(vector, op.Formal(i))
		if expected == nil || arg.Type != expected || arg.Typmod != expected.Usage().Typmod || !arg.Valid() {
			return FTSHeadlinePlan{}, fmt.Errorf("foreign headline operand authority")
		}
		p.inputs[i] = expected
	}
	return p, nil
}
func (p FTSHeadlinePlan) ValidateValues(ctx context.Context, values [4]Value) error {
	if p.result == nil {
		return fmt.Errorf("invalid headline plan")
	}
	var usages [4]Usage
	for i := range p.operation.Arity() {
		if err := ctx.Err(); err != nil {
			return err
		}
		v := values[i]
		if v.typ == nil || v.typ != p.inputs[i] || !v.null && v.typ.id == TSQuery && !v.validFTS() {
			return fmt.Errorf("foreign headline value authority")
		}
		usages[i] = v.typ.Usage()
	}
	checked, err := BindFTSHeadline(p.operation, p.result.Usage(), usages)
	if err != nil || checked != p {
		return fmt.Errorf("invalid headline plan authority")
	}
	return nil
}

// FTSHeadlineResult checks the query/options/result generation again at the
// detached runtime boundary, then constructs an owned TEXT without reparsing.
func FTSHeadlineResult(ctx context.Context, result *Descriptor, document *fts.HeadlineDocument, query, options Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if result == nil || result.id != Text || query.typ == nil || query.typ.ftsText != result || query.typ.id != TSQuery || query.null || !query.validFTS() || options.typ != result || options.null {
		return Value{}, fmt.Errorf("foreign headline runtime authority")
	}
	vector := result.ftsVector
	if vector == nil || vector.ftsQuery != query.typ || vector.ftsDistance == nil {
		return Value{}, fmt.Errorf("invalid headline companion authority")
	}
	parsed, err := fts.ParseHeadlineOptions(ctx, options.text, func(text string) (int32, error) {
		v, err := vector.ftsDistance.integerInput(text)
		return int32(v.integer), err
	})
	if err != nil {
		return Value{}, err
	}
	text, err := document.Headline(ctx, query.ftsQuery, parsed)
	if err != nil {
		return Value{}, err
	}
	return String(result, text), ctx.Err()
}
