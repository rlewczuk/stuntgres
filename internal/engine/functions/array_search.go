package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type ArraySearchContext interface {
	Context
	FunctionInput() sqltype.InputEnvironment
}

type ArraySearch[E Expression[C], C ArraySearchContext, P any] struct {
	Info[P]
	Plan      sqltype.ArraySearchPlan
	Args      []E
	Namespace string
}

func NewArraySearch[E Expression[C], C ArraySearchContext, P any](info Info[P], plan sqltype.ArraySearchPlan, args []E, namespace string) ArraySearch[E, C, P] {
	return ArraySearch[E, C, P]{Info: info, Plan: plan, Args: args, Namespace: namespace}
}

func (e ArraySearch[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	return e.Plan.Evaluate(ctx, values[:len(e.Args)], c.FunctionInput())
}

// Reconstruct receives only descriptors and comparison facts already mapped by
// the engine. The selected old plan and borrowed arguments remain untouched.
func (e ArraySearch[E, C, P]) Reconstruct(info Info[P], args []E, array, result, integer *sqltype.Descriptor, ref sqltype.CollationRef, conflict bool) (ArraySearch[E, C, P], error) {
	plan, err := sqltype.BindArraySearch(e.Plan.Name(), array, result, integer, ref, conflict)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

func (e ArraySearch[E, C, P]) Validate() error {
	if len(e.Args) < 2 || len(e.Args) > 3 {
		return fmt.Errorf("stuntgres: invalid stored array search arity")
	}
	ref, conflict := e.Plan.InputCollation()
	plan, err := sqltype.BindArraySearch(e.Plan.Name(), e.Args[0].ResultUsage().Type, e.Usage.Type, e.Plan.Integer(), ref, conflict)
	if err != nil || !plan.Equal(e.Plan) {
		return fmt.Errorf("stuntgres: invalid stored array search provider")
	}
	return nil
}

func AnalyzeArraySearch[E any](b ArgumentAnalyzer[E], raw []parser.Expr) ([]Argument[E], error) {
	args := make([]Argument[E], len(raw))
	for i, arg := range raw {
		next, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		args[i] = next
	}
	return args, nil
}

// ArraySearchArity excludes the optional INT4 start from common leaf selection.
func ArraySearchArity(name string, visible, named bool, count int) (int, bool) {
	arity := 2
	if name == "array_replace" {
		arity = 3
	}
	return arity, visible && name == strings.ToLower(name) && !named && (count == arity || name == "array_position" && count == 3)
}

func ArraySearchLeafUsage(usage sqltype.Usage) (sqltype.Usage, bool) {
	if usage.Type == nil {
		return usage, true
	}
	base := usage.Type.BaseType()
	if !base.IsArray() {
		return sqltype.Usage{}, false
	}
	leaf := base.Element().Usage()
	leaf.Typmod, leaf.Collation = usage.Typmod, usage.Collation
	return leaf, true
}

// Targets are supplied by the engine's generation-owned association lookup.
// Probe every known edge before converting any literal or parameter occurrence.
func SelectArraySearch[E any](name string, args []Argument[E], array, common, integer sqltype.Usage) ([3]sqltype.Usage, bool) {
	targets := [3]sqltype.Usage{array, common}
	if name == "array_replace" {
		targets[2] = common
	}
	if name == "array_position" && len(args) == 3 {
		targets[2] = integer
	}
	for i, arg := range args {
		if arg.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(arg.Usage, targets[i], sqltype.Implicit); err != nil {
				return targets, false
			}
		}
	}
	return targets, true
}

func FinishArraySearch[E any](b ArgumentCoercer[E], name string, args []Argument[E], targets [3]sqltype.Usage) ([]E, error) {
	result := make([]E, len(args))
	for i, arg := range args {
		next, err := b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: targets[i], Label: name})
		if err != nil {
			return nil, err
		}
		result[i] = next
	}
	return result, nil
}
