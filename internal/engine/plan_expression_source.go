package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func SameIntaggIdentity(a *catalog.Procedure, ar ids.ObjectRef, b *catalog.Procedure, br ids.ObjectRef) bool {
	if ar != br || (a == nil) != (b == nil) {
		return false
	}
	return a == nil || a.Address() == b.Address() && a.Revision() == b.Revision()
}

type unnestExpression struct {
	expr.Info
	Args      []expr.Expr
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (e unnestExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted unnest call")
}

// unnestInputTypes is shared by applicability and the selected provider.
func UnnestInputTypes(usage sqltype.Usage) (input, result *sqltype.Descriptor) {
	if usage.Type == nil {
		return nil, nil
	}
	input = usage.Type.BaseType()
	if input.IsArray() {
		result = input.Element()
	} else {
		result = sqltype.MultirangeUnnestResult(input)
	}
	return input, result
}

// A series is a retained typed target marker, never an executable generator.
// Invocation-private arithmetic lives in the owner's source cursor.
type seriesExpression struct {
	expr.Info
	Args []expr.Expr
}

func (e seriesExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted generate_series set call")
}

// A subscripts call retains only its analyzed arguments. Each execution opens
// fresh bounds-only state in the shared arithmetic function cursor.
type subscriptsExpression struct {
	expr.Info
	Args []expr.Expr
}

func (e subscriptsExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted generate_subscripts set call")
}
