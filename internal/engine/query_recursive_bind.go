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
	old := len(b.cteBindings)
	b.cteBindings = append(b.cteBindings, cteBinding{d.name, d})
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
	project.child = &recursiveUnionNode{setNode: *set, definition: d}
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
