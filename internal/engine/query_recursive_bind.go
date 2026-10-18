package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindRecursiveSet(s *parser.Select, d *cteDefinition) (*BoundQuery, error) {
	leftQuery, err := b.bindSelect(s.Set.Left, false)
	if err != nil {
		return nil, err
	}
	left := leftQuery.Root.(*projectNode)
	if len(d.columns) > len(left.Schema()) {
		return nil, expressionError("42P10", fmt.Sprintf(`WITH query "%s" has %d columns available but %d columns specified`, d.name, len(left.Schema()), len(d.columns)), d.position)
	}
	d.schema = make(querySchema, len(left.Schema()))
	for i, column := range left.Schema() {
		d.schema[i] = ColumnDescriptor{Name: column.Name, Usage: column.Usage}
		if i < len(d.columns) {
			d.schema[i].Name = d.columns[i].Text
		}
	}
	leftCopy := *left
	leftCopy.querySchema = d.schema
	leftQuery.Root = &leftCopy
	d.userWidth = len(d.schema)
	if d.search != nil {
		if err := b.bindSearchShape(d); err != nil {
			return nil, err
		}
	}
	if d.cycle != nil {
		if err := b.bindCycleShape(d); err != nil {
			return nil, err
		}
	}
	old := len(b.cteBindings)
	b.cteBindings = append(b.cteBindings, cteBinding{name: d.name, definition: d, contained: true})
	defer func() { b.cteBindings = b.cteBindings[:old] }()
	rightQuery, err := b.bindSelect(s.Set.Right, true)
	if err != nil {
		return nil, err
	}
	query, err := b.bindSetArms(s, leftQuery, rightQuery)
	if err != nil {
		return nil, err
	}
	project := query.Root.(*projectNode)
	set, ok := project.child.(*setNode)
	if !ok {
		return nil, fmt.Errorf("stuntgres: recursive body lost UNION root")
	}
	for i, column := range set.Schema() {
		anchor := d.schema[i].Usage
		overall := column.Usage
		position := left.expressions[i].SourcePosition()
		if anchor.Type != overall.Type || anchor.Typmod != overall.Typmod {
			failure := expressionError("42804", fmt.Sprintf(`recursive query "%s" column %d has type %s in non-recursive term but type %s overall`, d.name, i+1, b.recursiveUsageName(anchor), b.recursiveUsageName(overall)), position)
			failure.(*sqlstate.Error).Hint = "Cast the output of the non-recursive term to the correct type."
			return nil, failure
		}
		anchorConflict := left.expressions[i].CollationIndeterminate()
		overallConflict := project.expressions[i].CollationIndeterminate()
		if anchor.Collation.ID != overall.Collation.ID || anchorConflict != overallConflict {
			name := func(id uint32, indeterminate bool) string {
				if indeterminate || id == 0 {
					return "(null)"
				}
				return b.collationName(id)
			}
			failure := expressionError("42P21", fmt.Sprintf(`recursive query "%s" column %d has collation "%s" in non-recursive term but collation "%s" overall`, d.name, i+1, name(anchor.Collation.ID, anchorConflict), name(overall.Collation.ID, overallConflict)), position)
			failure.(*sqlstate.Error).Hint = "Use the COLLATE clause to set the collation of the non-recursive term."
			return nil, failure
		}
	}
	if len(set.Schema()) == 0 {
		set.grouping = nil
	}
	// SEARCH expandability is checked only after complete body analysis,
	// including recursive width, type and collation reconciliation.
	if d.search != nil {
		if err := parser.CheckRecursiveCTESelectArms(s); err != nil {
			return nil, err
		}
	}
	if d.search != nil {
		if err := searchNameError(d); err != nil {
			return nil, err
		}
	}
	if d.cycle != nil {
		if d.cycle.shapeError != nil {
			return nil, d.cycle.shapeError
		}
		if err := cycleNameError(d); err != nil {
			return nil, err
		}
		if d.search != nil {
			name := d.search.syntax.Sequence.Text
			if name == d.cycle.syntax.Mark.Text {
				return nil, expressionError("42601", "search sequence column name and cycle mark column name are the same", d.search.syntax.Pos)
			}
			if name == d.cycle.syntax.Path.Text {
				return nil, expressionError("42601", "search sequence column name and cycle path column name are the same", d.search.syntax.Pos)
			}
		}
		if !set.all {
			markGrouping, err := b.bindDistinctGrouping(d.cycle.mark)
			if err != nil {
				return nil, err
			}
			set.grouping = append(set.grouping, markGrouping)
		}
	}
	if d.search != nil {
		if err := parser.CheckRecursiveCTETopLevel(s, parser.Identifier{Text: d.name, Pos: d.position}); err != nil {
			return nil, err
		}
		if !set.all {
			if err := b.bindSearchIdentity(d); err != nil {
				return nil, err
			}
		}
	}
	if d.search != nil || d.cycle != nil {
		right := set.right
		scan, offset := recursiveParentSlot(right.child, d, 0)
		if scan == nil {
			return nil, fmt.Errorf("stuntgres: missing recursive parent projection")
		}
		for slot := d.userWidth; slot < len(d.schema); slot++ {
			scan.demand[slot] = true
			column := d.schema[slot]
			info := b.info(column.Usage, d.position, ExprProperties{RowDependent: true})
			right.expressions = append(right.expressions, columnExpression{Info: info, scope: 1, frame: right.frame, slot: offset + slot})
			right.querySchema = append(right.querySchema, column)
			set.rightExpressions = append(set.rightExpressions, right.expressions[len(right.expressions)-1])
			project.expressions = append(project.expressions, columnExpression{Info: info, scope: 1, frame: project.frame, slot: slot})
		}
		set.querySchema = d.schema
		project.querySchema = d.schema
	}
	project.child = &recursiveUnionNode{setNode: *set, definition: d, search: d.search, cycle: d.cycle}
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
