package plan

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type LiteralExpr struct {
	expr.Info
	Value        sqltype.Value
	UnknownInput *expr.UnknownInput
}

func (e LiteralExpr) UnknownInputSource() *expr.UnknownInput { return e.UnknownInput }

func (e LiteralExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type == nil || e.Value.Type() != e.Usage.Type ||
		!e.Usage.Type.Concrete() && !(e.Value.IsNull() && e.Usage.QueryNullUsage()) ||
		e.UnknownNull && (!e.Value.IsNull() || e.Usage.Type.ID() != sqltype.Text) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid literal expression")
	}
	return e.Value, nil
}

type ColumnExpr struct {
	expr.Info
	Origin             ids.ObjectRef
	Scope, Frame       uint32
	Slot               int
	Virtual            expr.Expr
	VirtualOwner       *catalog.Relation
	VirtualAttribute   int32
	RowStart, RowWidth int
	// Zero denotes an owner-row read; SQL occurrence IDs are encoded plus one.
	QueryOccurrence int
	MergeSource     bool // Missing MERGE source tuples short-circuit virtual recipes.
}

func (e ColumnExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	// A plain column passes only its frame coordinates, not the whole node.
	if e.Virtual == nil && !e.MergeSource {
		return runtime.FrameValue(e.Scope, e.Frame, e.Slot, e.Usage)
	}
	return runtime.EvalColumn(e)
}

type ParameterExpr struct {
	expr.Info
	Number int
}

func (e ParameterExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.ParameterValue(e.Number, e.Usage)
}

type CoercionExpr struct {
	expr.Info
	Child            expr.Expr
	Plan             sqltype.CoercionPlan
	Mode             sqltype.CoercionContext
	UnknownInputCast *UnknownInputCast
	// The explicit conversion belongs to the enclosing ARRAY or ROW cast, not
	// to a written cast at this element's raw syntax occurrence.
	SyntheticConstructorElement bool
	// XMLSerializeResult owns XMLSERIALIZE's ordinary implicit result
	// conversion. Its child is the actual selected coercion, not raw syntax.
	XMLSerializeResult bool
}

// COLLATE changes analyzed usage only; values retain the same type and datum.
type CollationExpr struct {
	expr.Info
	Child expr.Expr
}

func (e CollationExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	return e.Child.Eval(ctx)
}

func (e CoercionExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	v, err := e.Child.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.ApplyCoercion(e.Plan, v, sqltype.ContainsDomainConstraints(e.Usage.Type))
}

type ComparisonExpr struct {
	expr.Info
	RoutineRef  ids.ObjectRef
	Left, Right expr.Expr
	Predicate   sqltype.ComparisonPredicate
	Operator    string
	Namespace   string
}

func (e ComparisonExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.Left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.Right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Predicate.Evaluate(ctx.FunctionContext(), a, b, ctx.FunctionInput())
}

type Truth uint8

const (
	False Truth = iota
	True
	Unknown
)

func TruthOf(v sqltype.Value) (Truth, error) {
	if v.Type() != nil && v.Type().IsDomain() {
		var err error
		v, err = v.BaseValue()
		if err != nil {
			return False, err
		}
	}
	if v.Type() == nil || v.Type().ID() != sqltype.Bool {
		return False, fmt.Errorf("stuntgres: predicate is not typed boolean")
	}
	if v.IsNull() {
		return Unknown, nil
	}
	b, err := v.Boolean()
	if err != nil {
		return False, err
	}
	if b {
		return True, nil
	}
	return False, nil
}

type ArithmeticExpr struct {
	expr.Info
	Left, Right expr.Expr
	Provider    sqltype.Arithmetic
	Operator    sqltype.ArithmeticOperator
	Namespace   string
}

func (e ArithmeticExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.Left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Right == nil {
		return e.Provider.Unary(ctx.FunctionContext(), a, ctx.FunctionInput())
	}
	v, err := e.Right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Provider.Binary(ctx.FunctionContext(), a, v, ctx.FunctionInput())
}
