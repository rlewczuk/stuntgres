package plan

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// SubscriptHandler selects the container semantics of a SubscriptExpr, the
// type's pg_type.typsubscript. The zero value is the array handler.
type SubscriptHandler uint8

const (
	ArraySubscripts SubscriptHandler = iota
	// HstoreSubscripts is hstore 1.8's hstore_subscript_handler: one text key
	// fetching a text value, or assigning one key of the container.
	HstoreSubscripts
)

// hstoreFetch evaluates h[key] for a non-NULL container. A NULL key, a missing
// key and a NULL-valued key all fetch NULL.
func (n SubscriptExpr) hstoreFetch(ctx expr.Context, container sqltype.Value) (sqltype.Value, error) {
	if len(n.Subscripts) != 1 || n.Subscripts[0].Upper == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid hstore subscript")
	}
	key, err := n.Subscripts[0].Upper.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if key.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	base, err := container.BaseValue()
	if err != nil {
		return sqltype.Value{}, err
	}
	return base.HstoreFetch(ctx.FunctionContext(), base.Type(), key)
}

// applyHstore assigns h[key] := value. Like PostgreSQL, the key and the value
// are evaluated before the key's NULL check; a NULL container is empty.
func (t *ArrayAssignmentTarget) applyHstore(ctx expr.Context, current sqltype.Value, expression expr.Expr, rhs sqltype.Value) (sqltype.Value, error) {
	if len(t.Node.Subscripts) != 1 || t.Node.Subscripts[0].Upper == nil || t.Next != nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid hstore assignment target")
	}
	key, err := t.Node.Subscripts[0].Upper.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if expression != nil {
		if rhs, err = expression.Eval(ctx); err != nil {
			return sqltype.Value{}, err
		}
	}
	if key.IsNull() {
		return sqltype.Value{}, sqlError("22004", "hstore subscript in assignment must not be null")
	}
	source := t.Container.Type.BaseType()
	result, err := source.HstoreFromText(ctx.FunctionContext(), key, rhs)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !current.IsNull() {
		base, err := current.BaseValue()
		if err != nil {
			return sqltype.Value{}, err
		}
		if result, err = base.HstoreConcat(ctx.FunctionContext(), source, result); err != nil {
			return sqltype.Value{}, err
		}
	}
	return t.reapply(ctx, result)
}
