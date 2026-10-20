package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type domainValueExpression struct{ expr.Info }

func (e domainValueExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalDomainValue(e)
}

type tableOIDExpression struct {
	expr.Info
	owner ids.ObjectRef
}

func (n tableOIDExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalTableOID(n)
}

// The retained plan reads a physical leaf window, preserving the named column's
// ACL identity and the caller's context. Definitions themselves contain plain
// current-row references; no query-specific window enters the catalog.
func (e columnExpression) withGeneration(d *catalog.Relation, physical, start int) columnExpression {
	if d == nil {
		return e
	}
	if definition, ok := d.ColumnMeta.Generations[d.Attributes[physical]]; ok && definition.Kind == catalog.GenerationVirtual {
		e.virtual = definition.Planned
		e.virtualOwner = d
		e.virtualAttribute = d.Attributes[physical]
		e.rowStart = start
		e.rowWidth = len(d.Columns)
	}
	return e
}

// The owner is lexical, not whichever query is currently evaluating a child.
type mergeActionExpression struct {
	expr.Info
	scope, frame uint32
}

func (e mergeActionExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalMergeAction(e)
}

type mergeReturningSide uint8

// The provider is the sole executable child. Its lexical children retain the
// selected call's ordered, already-coerced operands for SQL rendering.
type selectedOperatorExpression struct {
	expr.Info
	operator *catalog.Operator
	ref      ids.ObjectRef
	provider expr.Expr
}

func (e selectedOperatorExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	return e.provider.Eval(ctx)
}

// unknownOutput is an occurrence, not a SQL value. Assignment or ordinary
// SELECT output finalization must eliminate it before the query can execute.
type unknownOutput struct {
	expr.Info
	raw parser.Expr
}

func (unknownOutput) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: unresolved query output")
}

type cachedOrderExpression struct {
	expr.Info
	slot int
}

func (e cachedOrderExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalCachedOrder(e)
}

// Side-aware columns preserve dependencies even when their tuple is absent.
type returningColumnExpression struct {
	columnExpression
	old, absent bool
	merge       bool
	side        mergeReturningSide
	// A lowered logical view column can be an arbitrary physical expression.
	value expr.Expr
}

func (e returningColumnExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalReturningColumn(e)
}

// Named transition rows retain their lexical target owner and actual presence.
// This does not introduce generic ROW or composite field syntax.
type returningWholeRowExpression struct {
	expr.Info
	members      []expr.Expr
	scope, frame uint32
	old          bool
	merge        bool
	side         mergeReturningSide
}

func (e returningWholeRowExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalReturningWholeRow(e)
}

type updateOutputExpression struct {
	expr.Info
	index, group int
}

func (e updateOutputExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalUpdateOutput(e)
}

// View qualification is a relational guard, not a change to ordinary Boolean
// expression evaluation. Unsafe user work never sees a row hidden by any layer.
type viewQualificationExpression struct {
	expr.Info
	predicates []expr.Expr // outer-to-inner storage; evaluated inner-to-outer
	user       expr.Expr
}

func (e viewQualificationExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	value, known, err := queryPredicateProofValue(ctx, e.user)
	if err != nil {
		return sqltype.Value{}, err
	}
	if known {
		truth, err := TruthOf(value)
		if err != nil {
			return sqltype.Value{}, err
		}
		if truth != True {
			// A proven empty user input cannot expose a row or demand a
			// retained qualification. Preserve its original FALSE/NULL value.
			return value, ctx.FunctionContext().Err()
		}
	}
	for i := len(e.predicates) - 1; i >= 0; i-- {
		value, err := e.predicates[i].Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		truth, err := TruthOf(value)
		if err != nil {
			return sqltype.Value{}, err
		}
		if truth != True {
			return sqltype.Boolean(e.Usage.Type, false), nil
		}
	}
	if known {
		return value, ctx.FunctionContext().Err()
	}
	if e.user == nil {
		return sqltype.Boolean(e.Usage.Type, true), ctx.FunctionContext().Err()
	}
	return e.user.Eval(ctx)
}
