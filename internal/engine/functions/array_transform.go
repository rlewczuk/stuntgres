package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type ArrayTransformKind uint8

const (
	ArrayAppend ArrayTransformKind = iota + 1
	ArrayPrepend
	ArrayConcatenate
	ArrayFill
	ArrayReverse
	ArraySort
	ArrayTrim
	ArrayToString
)

func ArrayTransformFunction(name string) ArrayTransformKind {
	switch name {
	case "array_append":
		return ArrayAppend
	case "array_prepend":
		return ArrayPrepend
	case "array_cat":
		return ArrayConcatenate
	case "array_fill":
		return ArrayFill
	case "array_reverse":
		return ArrayReverse
	case "array_sort":
		return ArraySort
	case "trim_array":
		return ArrayTrim
	case "array_to_string":
		return ArrayToString
	}
	return 0
}
func (k ArrayTransformKind) Name() string {
	switch k {
	case ArrayAppend:
		return "array_append"
	case ArrayPrepend:
		return "array_prepend"
	case ArrayConcatenate:
		return "array_cat"
	case ArrayFill:
		return "array_fill"
	case ArrayReverse:
		return "array_reverse"
	case ArraySort:
		return "array_sort"
	case ArrayTrim:
		return "trim_array"
	case ArrayToString:
		return "array_to_string"
	}
	return ""
}
func (k ArrayTransformKind) Arity(n int) bool {
	switch k {
	case ArrayAppend, ArrayPrepend, ArrayConcatenate, ArrayTrim:
		return n == 2
	case ArrayReverse:
		return n == 1
	case ArrayFill, ArrayToString:
		return n == 2 || n == 3
	case ArraySort:
		return n >= 1 && n <= 3
	}
	return false
}

type ArrayTransform struct {
	expr.Info
	Kind ArrayTransformKind
	// Args and labels retain written order; Slots maps each written child to its
	// formal position. Evaluation uses formal order without rebinding children.
	Args      []expr.Expr
	Slots     [3]uint8
	Labels    [3]string
	Namespace string
	Operator  bool
	Sort      sqltype.ArraySortPlan
}

func (e ArrayTransform) IntrinsicStable() bool {
	return e.Kind == ArrayToString || e.Kind == ArraySort && e.Sort.SessionDependent()
}
func (e ArrayTransform) Formal(i int) expr.Expr {
	for j := range e.Args {
		if int(e.Slots[j]) == i {
			return e.Args[j]
		}
	}
	var zero expr.Expr
	return zero
}

func (e ArrayTransform) Validate() error {
	bad := func() error { return fmt.Errorf("stuntgres: invalid stored array transformation provider") }
	if !e.Kind.Arity(len(e.Args)) || e.Usage.Type == nil || e.Namespace != "" && e.Namespace != "pg_catalog" || e.Operator && (e.Kind < ArrayAppend || e.Kind > ArrayConcatenate) {
		return bad()
	}
	var seen [3]bool
	for i, arg := range e.Args {
		if any(arg) == nil || int(e.Slots[i]) >= len(e.Args) || seen[e.Slots[i]] {
			return bad()
		}
		seen[e.Slots[i]] = true
		if e.Labels[i] != "" && (e.Kind != ArraySort || len(e.Args) == 1 || e.Labels[i] != [3]string{"array", "descending", "nulls_first"}[e.Slots[i]]) {
			return bad()
		}
	}
	first := e.Formal(0).ResultUsage()
	array := first.Type
	if e.Kind == ArrayPrepend {
		array = e.Formal(1).ResultUsage().Type
	}
	if e.Kind == ArrayFill {
		array = e.Usage.Type
	}
	if array == nil {
		return bad()
	}
	pseudo := first.CatalogNullUsage()
	if pseudo {
		if e.Kind != ArrayReverse && e.Kind != ArraySort && e.Kind != ArrayTrim && e.Kind != ArrayToString {
			return bad()
		}
	} else if !array.Concrete() || !array.IsArray() {
		return bad()
	}
	if e.Kind == ArrayToString {
		if e.Usage.Type.ID() != sqltype.Text || !e.Usage.Type.Concrete() {
			return bad()
		}
	} else if e.Usage.Type != array {
		return bad()
	}
	if e.Kind == ArrayAppend && e.Formal(1).ResultUsage().Type != array.Element() ||
		e.Kind == ArrayPrepend && first.Type != array.Element() ||
		e.Kind == ArrayFill && first.Type != array.Element() ||
		e.Kind == ArrayConcatenate && e.Formal(1).ResultUsage().Type != array {
		return bad()
	}
	for i := 1; i < len(e.Args); i++ {
		d := e.Formal(i).ResultUsage().Type
		switch e.Kind {
		case ArraySort:
			if d == nil || d.ID() != sqltype.Bool || !d.Concrete() {
				return bad()
			}
		case ArrayTrim:
			if d == nil || d.ID() != sqltype.Int4 || !d.Concrete() {
				return bad()
			}
		case ArrayToString:
			if d != e.Usage.Type {
				return bad()
			}
		case ArrayFill:
			if d == nil || !d.Concrete() || !d.IsArray() || d.Element().ID() != sqltype.Int4 {
				return bad()
			}
			if i == 2 && d != e.Formal(1).ResultUsage().Type {
				return bad()
			}
		}
	}
	if e.Kind == ArraySort && !pseudo {
		ref, conflict := e.Sort.InputCollation()
		plan, err := sqltype.BindArraySort(array, ref, conflict)
		if err != nil || !plan.Equal(e.Sort) {
			return bad()
		}
	}
	return nil
}

func (e ArrayTransform) Reconstruct(info expr.Info, args []expr.Expr, ref sqltype.CollationRef, conflict bool) (ArrayTransform, error) {
	e.Info, e.Args = info, args
	if !e.Kind.Arity(len(args)) {
		return e, fmt.Errorf("stuntgres: invalid reconstructed array transformation arity")
	}
	if e.Kind == ArraySort {
		// Validate slots before using Formal, including malformed stored trees.
		var seen [3]bool
		for i := range args {
			if int(e.Slots[i]) >= len(args) || seen[e.Slots[i]] || any(args[i]) == nil {
				return e, fmt.Errorf("stuntgres: invalid reconstructed array transformation slots")
			}
			seen[e.Slots[i]] = true
		}
		if !e.Formal(0).ResultUsage().CatalogNullUsage() {
			plan, err := sqltype.BindArraySort(e.Formal(0).ResultUsage().Type, ref, conflict)
			if err != nil {
				return e, err
			}
			e.Sort = plan
		}
	}
	return e, e.Validate()
}

func (e ArrayTransform) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	for i := range e.Args {
		v, err := e.Formal(i).Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	null := sqltype.ResultNull(e.Usage)
	a := values[0]
	switch e.Kind {
	case ArrayAppend:
		return sqltype.ArrayPush(ctx, a, values[1], false)
	case ArrayPrepend:
		return sqltype.ArrayPush(ctx, values[1], a, true)
	case ArrayConcatenate:
		return sqltype.ArrayCat(ctx, a, values[1])
	case ArrayFill:
		var bounds *sqltype.Value
		if len(e.Args) == 3 {
			bounds = &values[2]
		}
		return sqltype.ArrayFill(ctx, e.Usage.Type, a, values[1], bounds)
	case ArrayReverse:
		if a.IsNull() {
			return null, nil
		}
		return sqltype.ArrayReverse(ctx, a)
	case ArrayTrim:
		if a.IsNull() || values[1].IsNull() {
			return null, nil
		}
		n, err := values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.ArrayTrim(ctx, a, n)
	case ArraySort:
		for _, v := range values[:len(e.Args)] {
			if v.IsNull() {
				return null, nil
			}
		}
		descending, nullsFirst := false, false
		if len(e.Args) > 1 {
			descending, err = values[1].Boolean()
			if err != nil {
				return sqltype.Value{}, err
			}
			nullsFirst = descending
		}
		if len(e.Args) > 2 {
			nullsFirst, err = values[2].Boolean()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		return e.Sort.Evaluate(ctx, a, descending, nullsFirst, c.FunctionInput())
	case ArrayToString:
		if a.IsNull() || values[1].IsNull() {
			return null, nil
		}
		separator, err := values[1].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		marker := ""
		replace := len(e.Args) == 3 && !values[2].IsNull()
		if replace {
			marker, err = values[2].Text()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		var out strings.Builder
		emitted := false
		output := c.FunctionStringOutput()
		for i := range a.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return sqltype.Value{}, err
			}
			v := a.ArrayElement(i)
			text := marker
			if v.IsNull() {
				if !replace {
					continue
				}
			} else {
				text, err = v.Type().OutputWithEnvironment(ctx, v, output)
				if err != nil {
					return sqltype.Value{}, err
				}
			}
			if emitted {
				if err := formatWrite(ctx, &out, separator); err != nil {
					return sqltype.Value{}, err
				}
			}
			if err := formatWrite(ctx, &out, text); err != nil {
				return sqltype.Value{}, err
			}
			emitted = true
		}
		return sqltype.String(e.Usage.Type, out.String()), nil
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid array transformation")
}

// ArrayTransform implements expr.Composite. Rebuild keeps the bound sort
// collation.
var _ expr.Composite = ArrayTransform{}

func (e ArrayTransform) Children() []expr.Expr { return e.Args }
func (e ArrayTransform) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("ArrayTransform", children, len(e.Args)); err != nil {
		return nil, err
	}
	ref, conflict := e.Sort.InputCollation()
	return rebuilt(e.Reconstruct(info, children, ref, conflict))
}

func (e ArrayTransform) IntrinsicVolatility() expr.Volatility { return stableIf(e.IntrinsicStable()) }

func (e ArrayTransform) PlanningFold() expr.Fold {
	if e.IntrinsicStable() {
		return expr.FoldNever
	}
	return expr.FoldConstant
}

func (e ArrayTransform) ValidateStoredProvider() error { return e.Validate() }

// Retag remaps the bound sort collation.
func (e ArrayTransform) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	ref, conflict := e.Sort.InputCollation()
	next, err := env.Collation(ref)
	if err != nil {
		return nil, false, err
	}
	if !changed && next == ref {
		return e, false, nil
	}
	node, err := e.Reconstruct(info, children, next, conflict)
	return retagged(node, true, err)
}

var _ expr.Renderable = ArrayTransform{}

// Render implements expr.Renderable.
func (e ArrayTransform) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	spec := expr.Rendering{
		Tree:   fmt.Sprintf("ArrayTransform %s namespace %s slots %v labels %v operator %t", e.Kind.Name(), e.Namespace, e.Slots, e.Labels, e.Operator),
		Inputs: e.Args, Args: listedArgs(e.Args),
	}
	spec.Replay.QuoteNamespace = true
	if e.Operator {
		spec.Form, spec.Name, spec.Namespace, spec.Grouping = expr.FormInfix, "||", e.Namespace, expr.GroupAlways
		return spec, nil
	}
	for i := range spec.Args {
		if e.Labels[i] != "" {
			spec.Args[i].Label = `"` + e.Labels[i] + `"`
		}
	}
	spec.Form, spec.Name, spec.Quote, spec.LabelQuote, spec.SimpleCast = expr.FormCall, "pg_catalog."+e.Kind.Name(), expr.QuoteVerbatim, expr.QuoteVerbatim, true
	spec.Replay.Name = e.Kind.Name()
	return spec, nil
}

// SameGroup implements expr.GroupIdentity.
func (e ArrayTransform) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(ArrayTransform)
	if !ok || e.Kind != o.Kind || len(e.Args) != len(o.Args) || !e.Sort.Equal(o.Sort) {
		return false
	}
	for i := range e.Args {
		if !same(e.Formal(i), o.Formal(i)) {
			return false
		}
	}
	return true
}

// EvaluationOrder implements expr.EvaluationOrdered: Args keep their written
// slots, and evaluation follows the formal slots.
func (e ArrayTransform) EvaluationOrder() []int {
	order := make([]int, 0, len(e.Args))
	for slot := range e.Args {
		for i := range e.Args {
			if int(e.Slots[i]) == slot {
				order = append(order, i)
			}
		}
	}
	return order
}

// LooksLikeFunction implements expr.FunctionSyntax.
func (e ArrayTransform) LooksLikeFunction() bool { return true }
