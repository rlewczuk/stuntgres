package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type literalExpression struct {
	expr.Info
	value sqltype.Value
}

func (e literalExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type == nil || !e.Usage.Type.Concrete() || e.value.Type() != e.Usage.Type ||
		e.UnknownNull && (!e.value.IsNull() || e.Usage.Type.ID() != sqltype.Text) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid literal expression")
	}
	return e.value, nil
}

type columnExpression struct {
	expr.Info
	origin             ids.ObjectRef
	scope, frame       uint32
	slot               int
	virtual            expr.Expr
	virtualOwner       *catalog.Relation
	virtualAttribute   int32
	rowStart, rowWidth int
	// Zero denotes an owner-row read; SQL occurrence IDs are encoded plus one.
	queryOccurrence int
	mergeSource     bool // Missing MERGE source tuples short-circuit virtual recipes.
}

func (e columnExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	// A plain column passes only its frame coordinates, not the whole node.
	if e.virtual == nil && !e.mergeSource {
		return runtime.FrameValue(e.scope, e.frame, e.slot, e.Usage)
	}
	return runtime.EvalColumn(e)
}

type parameterExpression struct {
	expr.Info
	number int
}

func (e parameterExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.ParameterValue(e.number, e.Usage)
}

type coercionExpression struct {
	expr.Info
	child expr.Expr
	plan  sqltype.CoercionPlan
	mode  sqltype.CoercionContext
	// The explicit conversion belongs to the enclosing ARRAY or ROW cast, not
	// to a written cast at this element's raw syntax occurrence.
	syntheticConstructorElement bool
}

// COLLATE changes analyzed usage only; values retain the same type and datum.
type collationExpression struct {
	expr.Info
	child expr.Expr
}

func (e collationExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	return e.child.Eval(ctx)
}

func (e coercionExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	v, err := e.child.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.ApplyCoercion(e.plan, v, sqltype.ContainsDomainConstraints(e.Usage.Type))
}

type comparisonExpression struct {
	expr.Info
	left, right expr.Expr
	predicate   sqltype.ComparisonPredicate
	operator    string
	namespace   string
}

func (e comparisonExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := e.right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.predicate.Evaluate(ctx.FunctionContext(), a, b, ctx.FunctionInput())
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

type arithmeticExpression struct {
	expr.Info
	left, right expr.Expr
	provider    sqltype.Arithmetic
	operator    sqltype.ArithmeticOperator
	namespace   string
}

func (e arithmeticExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	a, err := e.left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.right == nil {
		return e.provider.Unary(ctx.FunctionContext(), a, ctx.FunctionInput())
	}
	v, err := e.right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.provider.Binary(ctx.FunctionContext(), a, v, ctx.FunctionInput())
}
