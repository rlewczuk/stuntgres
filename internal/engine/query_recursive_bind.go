package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindRecursiveSet(s *parser.Select, d *plan.CTEDefinition) (*plan.Query, error) {
	leftQuery, err := b.bindSelect(s.Set.Left, false)
	if err != nil {
		return nil, err
	}
	left := leftQuery.Root.(*plan.ProjectNode)
	if len(d.Columns) > len(left.Schema()) {
		return nil, expr.ErrorAt("42P10", fmt.Sprintf(`WITH query "%s" has %d columns available but %d columns specified`, d.Name, len(left.Schema()), len(d.Columns)), d.Position)
	}
	d.Schema = make(plan.QuerySchema, len(left.Schema()))
	for i, column := range left.Schema() {
		d.Schema[i] = catalog.Column{Name: column.Name, Usage: column.Usage}
		if i < len(d.Columns) {
			d.Schema[i].Name = d.Columns[i].Text
		}
	}
	leftCopy := *left
	leftCopy.QuerySchema = d.Schema
	leftQuery.Root = &leftCopy
	d.UserWidth = len(d.Schema)
	if d.Search != nil {
		if err := b.bindSearchShape(d); err != nil {
			return nil, err
		}
	}
	if d.Cycle != nil {
		if err := b.bindCycleShape(d); err != nil {
			return nil, err
		}
	}
	old := len(b.cte.bindings)
	b.cte.bindings = append(b.cte.bindings, cteBinding{name: d.Name, definition: d, contained: true})
	defer func() { b.cte.bindings = b.cte.bindings[:old] }()
	rightQuery, err := b.bindSelect(s.Set.Right, true)
	if err != nil {
		return nil, err
	}
	query, err := b.bindSetArms(s, leftQuery, rightQuery)
	if err != nil {
		return nil, err
	}
	project := query.Root.(*plan.ProjectNode)
	set, ok := project.Child.(*plan.SetNode)
	if !ok {
		return nil, fmt.Errorf("stuntgres: recursive body lost UNION root")
	}
	for i, column := range set.Schema() {
		anchor := d.Schema[i].Usage
		overall := column.Usage
		position := left.Expressions[i].SourcePosition()
		if anchor.Type != overall.Type || anchor.Typmod != overall.Typmod {
			failure := expr.ErrorAt("42804", fmt.Sprintf(`recursive query "%s" column %d has type %s in non-recursive term but type %s overall`, d.Name, i+1, b.recursiveUsageName(anchor), b.recursiveUsageName(overall)), position)
			failure.(*sqlstate.Error).Hint = "Cast the output of the non-recursive term to the correct type."
			return nil, failure
		}
		anchorConflict := left.Expressions[i].CollationIndeterminate()
		overallConflict := project.Expressions[i].CollationIndeterminate()
		if anchor.Collation.ID != overall.Collation.ID || anchorConflict != overallConflict {
			name := func(id uint32, indeterminate bool) string {
				if indeterminate || id == 0 {
					return "(null)"
				}
				return b.collationName(id)
			}
			failure := expr.ErrorAt("42P21", fmt.Sprintf(`recursive query "%s" column %d has collation "%s" in non-recursive term but collation "%s" overall`, d.Name, i+1, name(anchor.Collation.ID, anchorConflict), name(overall.Collation.ID, overallConflict)), position)
			failure.(*sqlstate.Error).Hint = "Use the COLLATE clause to set the collation of the non-recursive term."
			return nil, failure
		}
	}
	if len(set.Schema()) == 0 {
		set.Grouping = nil
	}
	// SEARCH expandability is checked only after complete body analysis,
	// including recursive width, type and collation reconciliation.
	if d.Search != nil {
		if err := parser.CheckRecursiveCTESelectArms(s); err != nil {
			return nil, err
		}
	}
	if d.Search != nil {
		if err := analyze.SearchNameError(d); err != nil {
			return nil, err
		}
	}
	if d.Cycle != nil {
		if d.Cycle.ShapeError != nil {
			return nil, d.Cycle.ShapeError
		}
		if err := cycleNameError(d); err != nil {
			return nil, err
		}
		if d.Search != nil {
			name := d.Search.Syntax.Sequence.Text
			if name == d.Cycle.Syntax.Mark.Text {
				return nil, expr.ErrorAt("42601", "search sequence column name and cycle mark column name are the same", d.Search.Syntax.Pos)
			}
			if name == d.Cycle.Syntax.Path.Text {
				return nil, expr.ErrorAt("42601", "search sequence column name and cycle path column name are the same", d.Search.Syntax.Pos)
			}
		}
		if !set.All {
			markGrouping, err := b.bindDistinctGrouping(d.Cycle.Mark)
			if err != nil {
				return nil, err
			}
			set.Grouping = append(set.Grouping, markGrouping)
		}
	}
	if d.Search != nil {
		if err := parser.CheckRecursiveCTETopLevel(s, parser.Identifier{Text: d.Name, Pos: d.Position}); err != nil {
			return nil, err
		}
		if !set.All {
			if err := b.bindSearchIdentity(d); err != nil {
				return nil, err
			}
		}
	}
	if d.Search != nil || d.Cycle != nil {
		right := set.Right
		scan, offset := analyze.RecursiveParentSlot(right.Child, d, 0)
		if scan == nil {
			return nil, fmt.Errorf("stuntgres: missing recursive parent projection")
		}
		for slot := d.UserWidth; slot < len(d.Schema); slot++ {
			scan.Demand[slot] = true
			column := d.Schema[slot]
			info := b.info(column.Usage, d.Position, expr.Properties{RowDependent: true})
			right.Expressions = append(right.Expressions, plan.ColumnExpr{Info: info, Scope: 1, Frame: right.Frame, Slot: offset + slot})
			right.QuerySchema = append(right.QuerySchema, column)
			set.RightExpressions = append(set.RightExpressions, right.Expressions[len(right.Expressions)-1])
			project.Expressions = append(project.Expressions, plan.ColumnExpr{Info: info, Scope: 1, Frame: project.Frame, Slot: slot})
		}
		set.QuerySchema = d.Schema
		project.QuerySchema = d.Schema
	}
	project.Child = &plan.RecursiveUnionNode{SetNode: *set, Definition: d, Search: d.Search, Cycle: d.Cycle}
	return query, nil
}

func (b *expressionBinder) recursiveUsageName(usage sqltype.Usage) string {
	if !usage.Type.IsUserDefined() && !usage.Type.IsDomain() {
		if name, err := pgType(usage); err == nil {
			return name
		}
	}
	return b.diagnosticTypeName(usage.Type)
}
