package exec

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

func (c *Context) EvalTableOID(n plan.TableOIDExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if n.Scope != 0 || n.Frame != 0 || n.QueryOccurrence != 0 {
		if n.Scope != 1 || n.Frame == 0 || n.QueryOccurrence <= 0 || n.Owner.Address.ClassID != ids.RelationClassID || n.Owner.Address.ObjectID == 0 || n.Owner.Address.SubID != 0 || n.Owner.Domain() == nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid query tableoid coordinates")
		}
		for c != nil && (c.Scope != n.Scope || c.Frame != n.Frame) {
			c = c.Outer
		}
		if c == nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing query tableoid frame")
		}
		absent, err := c.queryOperandAbsent(n.QueryOccurrence - 1)
		if err != nil {
			return sqltype.Value{}, err
		}
		if absent {
			return sqltype.Null(n.Usage.Type), nil
		}
		source, index, err := c.queryOperandSource(n.QueryOccurrence - 1)
		if err != nil {
			return sqltype.Value{}, err
		}
		if source == nil || index < 0 || index >= source.Count() {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid query tableoid row")
		}
		ref := source.PhysicalRef(index)
		if ref.Address.ClassID != ids.RelationClassID || ref.Address.ObjectID == 0 || ref.Domain() != n.Owner.Domain() {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing query tableoid physical identity")
		}
		return sqltype.OIDValue(n.Usage.Type, uint32(ref.Address.ObjectID))
	}
	if c.Scope != 1 || c.Frame != 1 || n.Owner.Address.ObjectID == 0 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid generated tableoid frame")
	}
	owner := n.Owner
	if c.PhysicalTable != (ids.ObjectRef{}) {
		owner = c.PhysicalTable
	}
	return sqltype.OIDValue(n.Usage.Type, uint32(owner.Address.ObjectID))
}

// Generation recipes own their original references, independently of an outer
// query or definer view. Selecting that provenance never changes the invoker.
func SelectVirtualGenerationRecipe(c *Context, owner *catalog.Relation, definition catalog.ColumnGeneration) {
	c.RoutineRecipe, c.RoutineGraft = true, false
	c.RoutineChecks = nil
	c.RoutineReferences = definition.Dependencies
	c.RoutineConsumer = owner
}

// Frozen projections do not freeze the queried logical relation's recipe.
// Physical child identity belongs to tableoid/provenance, not recipe selection.
// Rechecks borrow the complete latest logical tuple without rereading a source
// whose inheritance mapping scratch may still hold that same tuple.
func (c *Context) ColumnCapturedGeneration(e plan.ColumnExpr, source *Source, index int, current []sqltype.Value) (expr.Expr, []sqltype.Value, error) {
	data := source.View.Load()
	if data == nil {
		return nil, nil, fmt.Errorf("stuntgres: virtual source snapshot is released")
	}
	logical, ok := data.Catalog.Relation(source.Ref.Address)
	if !ok {
		return nil, nil, fmt.Errorf("stuntgres: missing virtual source descriptor")
	}
	definition, ok := logical.ColumnMeta.Generations[e.Origin.Address.SubID]
	if !ok || definition.Kind != catalog.GenerationVirtual || definition.Planned == nil {
		return nil, nil, fmt.Errorf("stuntgres: missing captured virtual generation")
	}
	SelectVirtualGenerationRecipe(c, logical, definition)
	if current != nil {
		if len(current) != len(logical.Columns) {
			return nil, nil, fmt.Errorf("stuntgres: invalid virtual recheck tuple")
		}
		return definition.Planned, current, nil
	}
	return definition.Planned, source.Row(index), nil
}

func (c *Context) EvalGrouping(e plan.GroupingExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
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

func (row QueryPhysicalRow) Deletion() visibility.DeleteCandidate {
	return visibility.DeleteCandidate{Ref: row.Leaf.Ref, Storage: row.Leaf.Storage, Row: row.Version.Row, Version: row.Version.Version, Base: row.Version.Base, Ordinal: row.Hint, Own: row.Version.Born == 0}
}

func (c *Context) EvalMergeAction(e plan.MergeActionExpr) (sqltype.Value, error) {
	owner, err := returningRowContext(c, e.Scope, e.Frame)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !owner.MergeReturning {
		return sqltype.Value{}, errors.New("stuntgres: MERGE output action is missing")
	}
	var text string
	switch owner.MergeAction {
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
	MergeDefaultSide plan.MergeReturningSide = iota
	MergeOldSide
	MergeNewSide
)

func mergeReturningAbsent(c *Context, side plan.MergeReturningSide) bool {
	return side == MergeOldSide && c.MergeAction == parser.MergeInsertAction || side == MergeNewSide && c.MergeAction == parser.MergeDeleteAction
}

func (c *Context) EvalCachedOrder(e plan.CachedOrderExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.Slot < 0 || e.Slot >= len(c.OrderValues) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing ordering output")
	}
	return c.OrderValues[e.Slot], nil
}

func returningRowContext(c *Context, scope, frame uint32) (*Context, error) {
	if err := EvaluationContext(c); err != nil {
		return nil, err
	}
	for parent := c; parent != nil; parent = parent.Outer {
		if parent.Scope == scope && parent.Frame == frame {
			return parent, nil
		}
	}
	return nil, fmt.Errorf("stuntgres: RETURNING row frame is missing")
}

func (c *Context) EvalReturningColumn(e plan.ReturningColumnExpr) (sqltype.Value, error) {
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

func (c *Context) EvalReturningWholeRow(e plan.ReturningWholeRowExpr) (sqltype.Value, error) {
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

func (c *Context) subqueryJSONArray(e plan.SubqueryExpr, state *State, stage *Stage) (sqltype.Value, error) {
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
		inner := state.Evaluation(c.Context)
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

func SourceReturnsSet(e expr.Expr) bool { return expr.IsSetSource(e) }

func SourceAdmitted(e expr.Expr) bool { return expr.AdmitsSource(e) }

// Entries are immutable once published by Execute under Engine.mu. A candidate
// borrows the old group and copies only when a successful fold changes it.
type QuoteOrigin struct {
	Owner      ids.ObjectRef
	Attribute  int32
	Definition ids.OID
	Revision   ids.DefinitionRevision
}

type QuoteConstant struct {
	Offset     int
	Origin     QuoteOrigin
	Frame      uint32
	Name       string
	Anyelement bool
	Source     sqltype.Usage
	Result     sqltype.Usage
	Value      sqltype.Value
}

type QuoteCaptureCandidate struct {
	Entries []QuoteConstant
	Private bool
	Sealed  bool
}

func (c *Context) EvalUpdateOutput(e plan.UpdateOutputExpr) (sqltype.Value, error) {
	if e.Index >= len(c.UpdateOutputs) || !c.UpdateOutputsReady {
		return sqltype.Value{}, fmt.Errorf("stuntgres: UPDATE output outside its assignment group")
	}
	return c.UpdateOutputs[e.Index], nil
}

func (c *Context) EvalWindow(e *plan.WindowExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if e.Slot < 0 || e.Slot >= len(c.Row) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing window output")
	}
	return c.Row[e.Slot], nil
}
