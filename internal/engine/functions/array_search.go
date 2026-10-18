package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type ArraySearch struct {
	expr.Info
	Plan      sqltype.ArraySearchPlan
	Args      []expr.Expr
	Namespace string
}

func NewArraySearch(info expr.Info, plan sqltype.ArraySearchPlan, args []expr.Expr, namespace string) ArraySearch {
	return ArraySearch{Info: info, Plan: plan, Args: args, Namespace: namespace}
}

func (e ArraySearch) Eval(c expr.Context) (sqltype.Value, error) {
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
func (e ArraySearch) Reconstruct(info expr.Info, args []expr.Expr, array, result, integer *sqltype.Descriptor, ref sqltype.CollationRef, conflict bool) (ArraySearch, error) {
	plan, err := sqltype.BindArraySearch(e.Plan.Name(), array, result, integer, ref, conflict)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

func (e ArraySearch) Validate() error {
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

func AnalyzeArraySearch(b ArgumentAnalyzer, raw []parser.Expr) ([]Argument, error) {
	args := make([]Argument, len(raw))
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
func SelectArraySearch(name string, args []Argument, array, common, integer sqltype.Usage) ([3]sqltype.Usage, bool) {
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

func FinishArraySearch(b ArgumentCoercer, name string, args []Argument, targets [3]sqltype.Usage) ([]expr.Expr, error) {
	result := make([]expr.Expr, len(args))
	for i, arg := range args {
		next, err := b.CoerceArgument(arg, CoercionRequest{Intent: ConditionalArgument, Target: targets[i], Label: name})
		if err != nil {
			return nil, err
		}
		result[i] = next
	}
	return result, nil
}

// ArraySearch implements expr.Composite. Rebuild keeps the bound array and
// integer descriptors and comparison collation.
var _ expr.Composite = ArraySearch{}

func (e ArraySearch) Children() []expr.Expr { return e.Args }
func (e ArraySearch) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("ArraySearch", children, len(e.Args)); err != nil {
		return nil, err
	}
	ref, conflict := e.Plan.InputCollation()
	return rebuilt(e.Reconstruct(info, children, e.Plan.Array(), info.Usage.Type, e.Plan.Integer(), ref, conflict))
}

func (e ArraySearch) IntrinsicVolatility() expr.Volatility {
	return stableIf(e.Plan.SessionDependent())
}

func (e ArraySearch) ValidateStoredProvider() error { return nil }

// Retag remaps the comparison collation and retains the array and integer
// descriptor generations.
func (e ArraySearch) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	ref, conflict := e.Plan.InputCollation()
	next, err := env.Collation(ref)
	if err != nil {
		return nil, false, err
	}
	array, integer := env.Type(e.Plan.Array()), env.Type(e.Plan.Integer())
	if !changed && next == ref && array == e.Plan.Array() && integer == e.Plan.Integer() {
		return e, false, nil
	}
	node, err := e.Reconstruct(info, children, array, info.Usage.Type, integer, next, conflict)
	return retagged(node, true, err)
}

var _ expr.Renderable = ArraySearch{}

// Render implements expr.Renderable.
func (e ArraySearch) Render() (expr.Rendering, error) {
	return expr.Rendering{Tree: "ArraySearch " + e.Plan.Name() + " namespace " + e.Namespace, Inputs: e.Args}, nil
}
