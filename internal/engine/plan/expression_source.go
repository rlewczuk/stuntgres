package plan

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

type UnnestExpr struct {
	expr.Info
	Args      []expr.Expr
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

func (e UnnestExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted unnest call")
}

// UnnestInputTypes is shared by applicability and the selected provider.
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
type SeriesExpr struct {
	expr.Info
	Args []expr.Expr
}

func (e SeriesExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted generate_series set call")
}

// A subscripts call retains only its analyzed arguments. Each execution opens
// fresh bounds-only state in the shared arithmetic function cursor.
type SubscriptsExpr struct {
	expr.Info
	Args []expr.Expr
}

func (e SubscriptsExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted generate_subscripts set call")
}
