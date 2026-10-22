package sqltype

import (
	"context"
	"fmt"
)

type FTSOperator uint8

const (
	FTSAnd FTSOperator = iota
	FTSOr
	FTSNot
	FTSPhrase
	FTSPhraseDistance
	FTSContains
	FTSContained
	FTSConcat
)

var ftsOperations = [...]struct{ name, routine string }{
	{"&&", "tsquery_and"}, {"||", "tsquery_or"}, {"!!", "tsquery_not"},
	{"<->", "tsquery_phrase"}, {"tsquery_phrase", "tsquery_phrase"},
	{"@>", "tsq_mcontains"}, {"<@", "tsq_mcontained"}, {"||", "tsvector_concat"},
}

func FTSKind(name string, arity int, vector bool) (FTSOperator, bool) {
	if vector && name == "||" {
		return FTSConcat, arity == 2
	}
	for i, o := range ftsOperations {
		op := FTSOperator(i)
		if (name == o.name || name == o.routine) && arity == op.Arity() {
			return op, true
		}
	}
	return 0, false
}
func (o FTSOperator) Name() string {
	if int(o) >= len(ftsOperations) {
		return ""
	}
	return ftsOperations[o].name
}
func (o FTSOperator) Routine() string {
	if int(o) >= len(ftsOperations) {
		return ""
	}
	return ftsOperations[o].routine
}
func (o FTSOperator) Arity() int {
	if o == FTSNot {
		return 1
	}
	if o == FTSPhraseDistance {
		return 3
	}
	return 2
}
func (o FTSOperator) Formal(i int) ID {
	if o == FTSConcat {
		return TSVector
	}
	if i == 2 {
		return Int4
	}
	return TSQuery
}
func (o FTSOperator) ResultID() ID {
	if o == FTSConcat {
		return TSVector
	}
	if o == FTSContains || o == FTSContained {
		return Bool
	}
	return TSQuery
}

type FTSPlan struct {
	operation FTSOperator
	inputs    [3]*Descriptor
	result    *Descriptor
}

func BindFTS(op FTSOperator, result Usage, args [3]Usage) (FTSPlan, error) {
	if op.Name() == "" || result.Type == nil || result.Type.id != op.ResultID() || result != result.Type.Usage() {
		return FTSPlan{}, fmt.Errorf("invalid FTS result signature")
	}
	p := FTSPlan{operation: op, result: result.Type}
	for i, arg := range args {
		if i >= op.Arity() {
			if arg.Type != nil {
				return FTSPlan{}, fmt.Errorf("extra FTS operand")
			}
			continue
		}
		if arg.Type == nil || arg.Type.id != op.Formal(i) || arg != arg.Type.Usage() {
			return FTSPlan{}, fmt.Errorf("invalid FTS operand signature")
		}
		p.inputs[i] = arg.Type
	}
	source := p.inputs[0]
	if op.Arity() > 1 && source != p.inputs[1] {
		return FTSPlan{}, fmt.Errorf("foreign FTS operand authority")
	}
	expected := source
	if op.ResultID() == Bool {
		expected = source.ftsBoolean
	}
	if result.Type != expected || op == FTSPhraseDistance && p.inputs[2] != source.ftsDistance {
		return FTSPlan{}, fmt.Errorf("foreign FTS result authority")
	}
	return p, nil
}
func (p FTSPlan) Operation() FTSOperator { return p.operation }
func (p FTSPlan) Name() string           { return p.operation.Name() }
func (p FTSPlan) Evaluate(ctx context.Context, values [3]Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result == nil || p.operation.Name() == "" {
		return Value{}, fmt.Errorf("invalid FTS plan")
	}
	for i, v := range values[:p.operation.Arity()] {
		if v.typ == nil || v.typ != p.inputs[i] {
			return Value{}, fmt.Errorf("invalid FTS operand")
		}
	}
	for i, v := range values[:p.operation.Arity()] {
		if i < 2 && !v.null && !v.validFTS() {
			return Value{}, fmt.Errorf("invalid FTS payload")
		}
	}
	for _, v := range values[:p.operation.Arity()] {
		if v.null {
			return Null(p.result), nil
		}
	}
	result := Value{typ: p.result}
	var err error
	a, b := values[0].ftsQuery, values[1].ftsQuery
	switch p.operation {
	case FTSAnd:
		result.ftsQuery, err = a.And(ctx, b)
	case FTSOr:
		result.ftsQuery, err = a.Or(ctx, b)
	case FTSNot:
		result.ftsQuery, err = a.Not(ctx)
	case FTSPhrase, FTSPhraseDistance:
		distance := int32(1)
		if p.operation == FTSPhraseDistance {
			distance = int32(values[2].integer)
		}
		result.ftsQuery, err = a.Phrase(ctx, b, distance)
	case FTSContains:
		result.boolean, err = a.Contains(ctx, b)
	case FTSContained:
		result.boolean, err = b.Contains(ctx, a)
	case FTSConcat:
		result.ftsVector, err = values[0].ftsVector.Concat(ctx, values[1].ftsVector)
	}
	if err != nil {
		return Value{}, err
	}
	return result, nil
}
