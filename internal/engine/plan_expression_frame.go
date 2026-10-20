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
	Owner ids.ObjectRef
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
func (e columnExpression) WithGeneration(d *catalog.Relation, physical, start int) columnExpression {
	if d == nil {
		return e
	}
	if definition, ok := d.ColumnMeta.Generations[d.Attributes[physical]]; ok && definition.Kind == catalog.GenerationVirtual {
		e.Virtual = definition.Planned
		e.VirtualOwner = d
		e.VirtualAttribute = d.Attributes[physical]
		e.RowStart = start
		e.RowWidth = len(d.Columns)
	}
	return e
}

// The owner is lexical, not whichever query is currently evaluating a child.
type mergeActionExpression struct {
	expr.Info
	Scope, Frame uint32
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
	Operator *catalog.Operator
	Ref      ids.ObjectRef
	Provider expr.Expr
}

func (e selectedOperatorExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	return e.Provider.Eval(ctx)
}

// unknownOutput is an occurrence, not a SQL value. Assignment or ordinary
// SELECT output finalization must eliminate it before the query can execute.
type unknownOutput struct {
	expr.Info
	Raw parser.Expr
}

func (unknownOutput) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: unresolved query output")
}

type cachedOrderExpression struct {
	expr.Info
	Slot int
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
	Old, Absent bool
	Merge       bool
	Side        mergeReturningSide
	// A lowered logical view column can be an arbitrary physical expression.
	Value expr.Expr
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
	Members      []expr.Expr
	Scope, Frame uint32
	Old          bool
	Merge        bool
	Side         mergeReturningSide
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
	Index, Group int
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
	Predicates []expr.Expr // outer-to-inner storage; evaluated inner-to-outer
	User       expr.Expr
}

func (e viewQualificationExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	value, known, err := QueryPredicateProofValue(ctx, e.User)
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
	for i := len(e.Predicates) - 1; i >= 0; i-- {
		value, err := e.Predicates[i].Eval(ctx)
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
	if e.User == nil {
		return sqltype.Boolean(e.Usage.Type, true), ctx.FunctionContext().Err()
	}
	return e.User.Eval(ctx)
}
