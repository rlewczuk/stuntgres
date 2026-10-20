package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func subscriptChildren(n plan.SubscriptExpr, storage *[13]expr.Expr) []expr.Expr {
	count := 0
	if n.Base != nil {
		storage[count] = n.Base
		count++
	}
	for _, s := range n.Subscripts {
		if s.Lower != nil {
			storage[count] = s.Lower
			count++
		}
		if s.Upper != nil {
			storage[count] = s.Upper
			count++
		}
	}
	return storage[:count]
}

func (c *EvalContext) EvalTableOID(n plan.TableOIDExpr) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.Scope != 1 || c.Frame != 1 || n.Owner.Address.ObjectID == 0 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid generated tableoid frame")
	}
	owner := n.Owner
	if c.physicalTable != (ids.ObjectRef{}) {
		owner = c.physicalTable
	}
	return sqltype.OIDValue(n.Usage.Type, uint32(owner.Address.ObjectID))
}

// Generation recipes own their original references, independently of an outer
// query or definer view. Selecting that provenance never changes the invoker.
func selectVirtualGenerationRecipe(c *EvalContext, owner *catalog.Relation, definition catalog.ColumnGeneration) {
	c.routineRecipe, c.routineGraft = true, false
	c.routineChecks = nil
	c.routineReferences = definition.Dependencies
	c.routineConsumer = owner
}

// Frozen projections do not freeze the queried logical relation's recipe.
// Physical child identity belongs to tableoid/provenance, not recipe selection.
// Rechecks borrow the complete latest logical tuple without rereading a source
// whose inheritance mapping scratch may still hold that same tuple.
func (c *EvalContext) columnCapturedGeneration(e plan.ColumnExpr, source *querySource, index int, current []sqltype.Value) (expr.Expr, []sqltype.Value, error) {
	data := source.view.Load()
	if data == nil {
		return nil, nil, fmt.Errorf("stuntgres: virtual source snapshot is released")
	}
	logical, ok := data.Catalog.Relation(source.ref.Address)
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: missing virtual source descriptor")
	}
	definition, ok := logical.ColumnMeta.Generations[e.Origin.Address.SubID]
	if !ok || definition.Kind != catalog.GenerationVirtual || definition.Planned == nil {
		return nil, nil, fmt.Errorf("stuntgres: missing captured virtual generation")
	}
	selectVirtualGenerationRecipe(c, logical, definition)
	if current != nil {
		if len(current) != len(logical.Columns) {
			return nil, nil, fmt.Errorf("stuntgres: invalid virtual recheck tuple")
		}
		return definition.Planned, current, nil
	}
	return definition.Planned, source.row(index), nil
}

func (c *EvalContext) EvalGrouping(e plan.GroupingExpr) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	frame := e.Frame
	if frame == 0 {
		frame = 1
	}
	for c != nil && (c.Scope != 1 || c.Frame != frame) {
		c = c.Outer
	}
	if c == nil || e.Slot < 0 || e.Slot >= len(c.Row) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid grouping frame")
	}
	occurrence, err := c.Row[e.Slot].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	if occurrence < 0 || occurrence >= int64(len(e.Masks)) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid grouping occurrence")
	}
	return sqltype.Integer(e.Usage.Type, int64(e.Masks[occurrence]))
}

func (row queryPhysicalRow) deletion() visibility.DeleteCandidate {
	return visibility.DeleteCandidate{Ref: row.leaf.ref, Storage: row.leaf.storage, Row: row.version.Row, Version: row.version.Version, Base: row.version.Base, Ordinal: row.hint, Own: row.version.Born == 0}
}

func (c *EvalContext) EvalMergeAction(e plan.MergeActionExpr) (sqltype.Value, error) {
	owner, err := returningRowContext(c, e.Scope, e.Frame)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !owner.mergeReturning {
		return sqltype.Value{}, errors.New("stuntgres: MERGE output action is missing")
	}
	var text string
	switch owner.mergeAction {
	case parser.MergeInsertAction:
		text = "INSERT"
	case parser.MergeUpdateAction:
		text = "UPDATE"
	case parser.MergeDeleteAction:
		text = "DELETE"
	default:
		return sqltype.Value{}, errors.New("stuntgres: invalid MERGE output action")
	}
	return sqltype.String(e.Usage.Type, text), nil
}

const (
	mergeDefaultSide plan.MergeReturningSide = iota
	mergeOldSide
	mergeNewSide
)

func mergeReturningAbsent(c *EvalContext, side plan.MergeReturningSide) bool {
	return side == mergeOldSide && c.mergeAction == parser.MergeInsertAction || side == mergeNewSide && c.mergeAction == parser.MergeDeleteAction
}

func (c *EvalContext) EvalCachedOrder(e plan.CachedOrderExpr) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.Slot < 0 || e.Slot >= len(c.orderValues) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing ordering output")
	}
	return c.orderValues[e.Slot], nil
}

func returningRowContext(c *EvalContext, scope, frame uint32) (*EvalContext, error) {
	if err := evaluationContext(c); err != nil {
		return nil, err
	}
	for parent := c; parent != nil; parent = parent.Outer {
		if parent.Scope == scope && parent.Frame == frame {
			return parent, nil
		}
	}
	return nil, fmt.Errorf("stuntgres: RETURNING row frame is missing")
}

func (c *EvalContext) EvalReturningColumn(e plan.ReturningColumnExpr) (sqltype.Value, error) {
	parent, err := returningRowContext(c, e.Scope, e.Frame)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Merge && mergeReturningAbsent(parent, e.Side) {
		return sqltype.Null(e.Usage.Type), nil
	}
	if e.Absent || !e.Merge && e.Old && parent.OldRow == nil {
		return sqltype.Null(e.Usage.Type), nil
	}
	value := expr.Expr(e.ColumnExpr)
	if e.Value != nil {
		value = e.Value
	}
	if !e.Old {
		return value.Eval(c)
	}
	old := *parent
	old.Context, old.Row = c.Context, parent.OldRow
	return value.Eval(&old)
}

func (c *EvalContext) EvalReturningWholeRow(e plan.ReturningWholeRowExpr) (sqltype.Value, error) {
	parent, err := returningRowContext(c, e.Scope, e.Frame)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Merge && mergeReturningAbsent(parent, e.Side) || !e.Merge && e.Old && parent.OldRow == nil {
		return sqltype.Null(e.Usage.Type), nil
	}
	fields := make([]sqltype.Value, len(e.Members))
	for i, member := range e.Members {
		fields[i], err = member.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.OwnedCompositeValue(e.Usage.Type, fields)
}

func (c *EvalContext) subqueryJSONArray(e plan.SubqueryExpr, state *queryState, stage *indexedStage) (sqltype.Value, error) {
	p := e.JSONArray
	aggregate, err := p.Plan.Init(c.Context, c.FunctionJSONOutput())
	if err != nil {
		return sqltype.Value{}, err
	}
	var row [1]sqltype.Value
	for {
		present, err := stage.Next(c.Context, row[:])
		if err != nil {
			return sqltype.Value{}, err
		}
		if !present {
			return p.Plan.Finalize(c.Context, aggregate)
		}
		inner := state.evaluation(c.Context)
		inner.Frame = e.QueryPlan.Root.(*plan.ProjectNode).Frame
		inner.Row = row[:]
		value, err := p.Input.Eval(&inner)
		if err != nil {
			return sqltype.Value{}, err
		}
		if err = p.Plan.Advance(c.Context, aggregate, value); err != nil {
			return sqltype.Value{}, err
		}
	}
}

func sourceReturnsSet(e expr.Expr) bool { return expr.IsSetSource(e) }

func sourceAdmitted(e expr.Expr) bool { return expr.AdmitsSource(e) }

// Entries are immutable once published by Execute under Engine.mu. A candidate
// borrows the old group and copies only when a successful fold changes it.
type quoteOrigin struct {
	owner      ids.ObjectRef
	attribute  int32
	definition ids.OID
	revision   ids.DefinitionRevision
}

type quoteConstant struct {
	offset     int
	origin     quoteOrigin
	frame      uint32
	name       string
	anyelement bool
	source     sqltype.Usage
	result     sqltype.Usage
	value      sqltype.Value
}

type quoteCaptureCandidate struct {
	entries []quoteConstant
	private bool
	sealed  bool
}

func (c *EvalContext) EvalUpdateOutput(e plan.UpdateOutputExpr) (sqltype.Value, error) {
	if e.Index >= len(c.updateOutputs) || !c.updateOutputsReady {
		return sqltype.Value{}, fmt.Errorf("stuntgres: UPDATE output outside its assignment group")
	}
	return c.updateOutputs[e.Index], nil
}

func (c *EvalContext) EvalWindow(e *plan.WindowExpr) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.Slot < 0 || e.Slot >= len(c.Row) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing window output")
	}
	return c.Row[e.Slot], nil
}
