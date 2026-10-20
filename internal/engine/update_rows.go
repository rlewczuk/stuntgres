package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The first recipe owns the source query through subqueryExpression. Remaining
// outputs are lexical leaves, so all existing query graph transformations visit
// a group source exactly once, without adding a parallel graph ownership path.
type updateAssignmentGroup struct {
	start, width int
}

type updateOutputExpression struct {
	expr.Info
	index, group int
}

func (e updateOutputExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if e.index >= len(c.updateOutputs) || !c.updateOutputsReady {
		return sqltype.Value{}, fmt.Errorf("stuntgres: UPDATE output outside its assignment group")
	}
	return c.updateOutputs[e.index], nil
}

// Analyze sources in original SET order, before the shared destination pass.
// Constructors are expanded once, then their members use destination contexts;
// SELECT outputs retain their independently analyzed types and origins.
func (e *Engine) prepareUpdateAssignments(b *binding, binder *expressionBinder, raw []parser.UpdateAssignment, u *boundUpdate) ([]parser.UpdateAssignment, error) {
	width := 0
	grouped := false
	for _, a := range raw {
		if len(a.Targets) == 0 {
			width++
		} else {
			width += len(a.Targets)
			grouped = true
		}
	}
	assignments := raw
	if !grouped && u.assignments == nil {
		u.assignments = make([]expr.Expr, len(raw))
	}
	if grouped {
		assignments = make([]parser.UpdateAssignment, 0, width)
		u.assignments = make([]expr.Expr, 0, width)
		u.origins = make([]parser.Position, 0, width)
	}
	appendMember := func(a parser.UpdateAssignment, node expr.Expr, origin parser.Position) error {
		bound, err := e.bindUpdateAssignmentSource(b, binder, a, node, u, len(assignments), width)
		if err != nil {
			return err
		}
		if grouped {
			assignments = append(assignments, a)
			u.assignments = append(u.assignments, bound)
			u.origins = append(u.origins, origin)
		}
		return nil
	}
	for i, a := range raw {
		if len(a.Targets) == 0 {
			if grouped {
				if err := appendMember(a, nil, parser.ExpressionStart(a.Value)); err != nil {
					return nil, err
				}
			} else {
				bound, err := e.bindUpdateAssignmentSource(b, binder, a, nil, u, i, width)
				if err != nil {
					return nil, err
				}
				u.assignments[i] = bound
			}
			continue
		}
		start := len(assignments)
		switch source := a.Value.(type) {
		case parser.RowExpr:
			list, err := binder.expandRawList(source.Fields)
			if err != nil {
				return nil, err
			}
			if len(list.raw) != len(a.Targets) {
				return nil, updateRowWidthError(source.Pos)
			}
			for j, target := range a.Targets {
				member := parser.UpdateAssignment{Target: parser.Identifier{Text: target.Name, Pos: target.Pos}, Steps: target.Steps, Value: list.raw[j]}
				var bound expr.Expr
				if list.nodes != nil {
					bound = list.nodes[j]
				}
				if err := appendMember(member, bound, parser.ExpressionStart(member.Value)); err != nil {
					return nil, err
				}
			}
		case parser.SubqueryExpr:
			if source.Kind != parser.SubqueryScalar {
				return nil, expr.ErrorAt("0A000", "source for a multiple-column UPDATE item must be a sub-SELECT or ROW() expression", source.Pos)
			}
			binder.query.subqueryDemand = true
			query, err := binder.bindSelect(source.Query, false)
			if err != nil {
				return nil, err
			}
			project := query.Root.(*projectNode)
			if len(project.Schema()) != len(a.Targets) {
				return nil, updateRowWidthError(source.Pos)
			}
			properties := queryDemandProperties(project, false)
			properties.StageDependent = true
			for j, target := range a.Targets {
				origin := project.expressions[j].SourcePosition()
				info := binder.info(project.Schema()[j].Usage, origin, properties)
				var node expr.Expr = updateOutputExpression{Info: info, index: j, group: start}
				if j == 0 {
					node = subqueryExpression{Info: info, kind: parser.SubqueryScalar, queryPlan: query, updateWidth: len(a.Targets)}
				}
				member := parser.UpdateAssignment{Target: parser.Identifier{Text: target.Name, Pos: target.Pos}, Steps: target.Steps, Value: a.Value}
				if err := appendMember(member, node, origin); err != nil {
					return nil, err
				}
			}
			u.groups = append(u.groups, updateAssignmentGroup{start: start, width: len(a.Targets)})
			u.outputWidth = max(u.outputWidth, len(a.Targets))
		default:
			return nil, expr.ErrorAt("0A000", "source for a multiple-column UPDATE item must be a sub-SELECT or ROW() expression", parser.ExpressionStart(a.Value))
		}
	}
	return assignments, nil
}

func updateRowWidthError(pos parser.Position) error {
	return expr.ErrorAt("42601", "number of columns does not match number of values", pos)
}

func (e *Engine) bindUpdateAssignmentSource(b *binding, binder *expressionBinder, a parser.UpdateAssignment, bound expr.Expr, u *boundUpdate, index, width int) (expr.Expr, error) {
	if len(a.Steps) != 0 {
		if _, requested := a.Value.(parser.DefaultExpr); requested {
			return nil, expr.ErrorAt("0A000", "cannot set an array element to DEFAULT", a.Target.Pos)
		}
	}
	if slot, exists := b.relation.Index[a.Target.Text]; exists {
		if _, generated := b.relation.ColumnMeta.Generations[b.relation.Attributes[slot]]; generated {
			if _, requested := a.Value.(parser.DefaultExpr); !requested {
				return nil, generationWriteError(a.Target.Text, true)
			}
			return nil, nil
		}
	}
	if _, requested := a.Value.(parser.DefaultExpr); requested {
		if slot, ok := b.relation.Index[a.Target.Text]; ok && b.relation.Columns[slot].Usage.Type.IsDomain() {
			node, origin := columnDefault(binder.bindingCatalog(), b.relation, slot)
			if node == nil {
				node = domainNullInputExpression(b.relation.Columns[slot].Usage)
			}
			if u.defaultOrigins == nil {
				u.defaultOrigins = make([]defaultOrigin, width)
			}
			origin.requested = true
			u.defaultOrigins[index] = origin
			return node, nil
		}
		if b.relation.RelationKind == catalog.ViewRelation {
			return nil, nil
		}
		return nil, expr.ErrorAt("0A000", "UPDATE DEFAULT is unsupported", a.Value.SourcePosition())
	}
	if bound != nil {
		return bound, nil
	}
	expected := sqltype.Usage{}
	if slot, ok := b.relation.Index[a.Target.Text]; ok {
		expected = b.relation.Columns[slot].Usage
		if len(a.Steps) != 0 {
			expected = targetUsage(expected, a.Steps)
		}
	}
	return binder.bind(a.Value, expressionPolicy{context: projectionContext, expected: expected, mode: sqltype.Assignment})
}
