package sqltype

import (
	"context"
	"fmt"
)

// CitextPattern retains original descriptor authority and prepared text adapters.
// Regex execution state belongs to the calling statement, not this immutable plan.
type CitextPattern struct {
	args        [2]Usage
	result      Usage
	conversions [2]CoercionPlan
	like        LikePlan
	regex       PosixPlan
	kind        uint8
}

func BindCitextPattern(kind uint8, left, right, result, text Usage, collation CollationRef, conflict bool) (CitextPattern, error) {
	p := CitextPattern{args: [2]Usage{left, right}, result: result, kind: kind}
	if kind >= 4 || !left.Type.IsCitext() || (right.Type != left.Type && (right.Type == nil || !right.Type.Concrete() || right.Type.ID() != Text)) || !text.Valid() || text.Type.ID() != Text {
		return p, fmt.Errorf("invalid citext pattern signature")
	}
	for i, arg := range p.args {
		conversion, err := PlanCoercion(arg, text, Implicit)
		if err != nil {
			return p, err
		}
		p.conversions[i] = conversion
	}
	var err error
	if kind < 2 {
		p.like, err = BindLike([2]string{"~~*", "!~~*"}[kind], text, text, result, conflict)
	} else {
		p.regex, err = BindPosix([2]string{"~*", "!~*"}[kind-2], text, text, result, collation, conflict)
	}
	return p, err
}
func (p CitextPattern) Evaluate(ctx context.Context, a, b Value, state *PGRegexExecution) (Value, error) {
	if a.typ != p.args[0].Type || b.typ != p.args[1].Type {
		return Value{}, fmt.Errorf("invalid citext pattern values")
	}
	if err := ctx.Err(); err != nil {
		state.Reset()
		return Value{}, err
	}
	if a.null || b.null {
		state.Reset()
		return Null(p.result.Type), nil
	}
	a, err := p.conversions[0].Apply(ctx, a, InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		return Value{}, err
	}
	b, err = p.conversions[1].Apply(ctx, b, InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		return Value{}, err
	}
	if p.kind < 2 {
		return p.like.Evaluate(ctx, a, b)
	}
	return p.regex.Evaluate(ctx, a, b, state)
}
