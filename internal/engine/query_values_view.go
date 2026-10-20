package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// VALUES ordinals follow structural source preorder, independent of physical
// table occurrences and source offsets. Row/key indices are local to a source.
func (b *expressionBinder) captureValuesViewLiteral(raw parser.Expr, bound expr.Expr, clause viewLiteralClause, source, row, column int) error {
	path := viewLiteralPath{Query: b.frameID(), Clause: clause, Source: source, Row: row, Target: column}
	if clause == analyze.ViewValuesOrderLiteral {
		if integer, ok := raw.(parser.LiteralExpr); ok && integer.Value.Kind == parser.IntegerValue {
			return nil
		}
	}
	ordinal := 0
	return b.captureViewLiteralTree(raw, bound, path, &ordinal)
}

func viewValuesAt(node plan.Node, ordinal int) *plan.ValuesNode {
	var visit func(plan.Node) *plan.ValuesNode
	visit = func(node plan.Node) *plan.ValuesNode {
		switch n := node.(type) {
		case *plan.ValuesNode:
			if ordinal == n.ViewOrdinal {
				return n
			}
		case *plan.SetNode:
			if found := visit(n.Left); found != nil {
				return found
			}
			return visit(n.Right)
		case *plan.JoinNode:
			if found := visit(n.Left); found != nil {
				return found
			}
			return visit(n.Right)
		case *plan.ProjectNode:
			return visit(n.Child)
		case *plan.SortNode:
			return visit(n.Child)
		case *plan.LimitNode:
			return visit(n.Child)
		case *plan.EvaluationNode:
			return visit(n.Child)
		case *plan.FilterNode:
			return visit(n.Child)
		case *plan.AggregateNode:
			return visit(n.Child)
		case *plan.WindowNode:
			return visit(n.Child)
		case *plan.DistinctNode:
			return visit(n.Child)
		case *plan.DistinctOnNode:
			return visit(n.Child)
		case *plan.RowLockNode:
			return visit(n.Child)
		}
		return nil
	}
	return visit(node)
}

func (v *viewRenderer) valuesSQL(ctx context.Context, catalogView catalog.Read, source *parser.ValuesQuery, occurrence int) (string, error) {
	root := viewValuesAt(v.query.Root, occurrence)
	if root == nil {
		return "", fmt.Errorf("stuntgres: missing view VALUES occurrence")
	}
	slots := make(map[int]string, len(root.Schema()))
	for i, column := range root.Schema() {
		slots[i] = catalog.QuoteIdentifier(column.Name)
	}
	v.slots[root] = slots
	rows := make([]string, len(source.Rows))
	for i, row := range source.Rows {
		cells := make([]string, 0, len(row))
		for j, raw := range row {
			text, err := v.expressionSQL(ctx, catalogView, raw, viewLiteralPath{Clause: analyze.ViewValuesCellLiteral, Source: occurrence, Row: i, Target: j})
			if err != nil {
				return "", err
			}
			if text != "" {
				cells = append(cells, text)
			}
		}
		if len(cells) == 0 {
			cells = append(cells, "(ROW()).*")
		}
		rows[i] = "(" + strings.Join(cells, ", ") + ")"
	}
	text := "VALUES " + strings.Join(rows, ", ")
	if source.Order != nil {
		keys := make([]string, len(source.Order.Keys))
		for i, key := range source.Order.Keys {
			part, err := v.expressionSQL(ctx, catalogView, key.Expr, viewLiteralPath{Clause: analyze.ViewValuesOrderLiteral, Source: occurrence, Target: i})
			if err != nil {
				return "", err
			}
			if key.Desc {
				part += " DESC"
			}
			switch key.Nulls {
			case parser.NullsFirst:
				part += " NULLS FIRST"
			case parser.NullsLast:
				part += " NULLS LAST"
			}
			keys[i] = part
		}
		text += " ORDER BY " + strings.Join(keys, ", ")
	}
	if source.Bounds != nil {
		for _, bound := range []struct {
			raw    parser.Expr
			clause viewLiteralClause
			name   string
		}{
			{source.Bounds.Offset, analyze.ViewValuesOffsetLiteral, "OFFSET"},
			{source.Bounds.Count, analyze.ViewValuesLimitLiteral, "LIMIT"},
		} {
			if bound.raw == nil {
				continue
			}
			part, err := v.expressionSQL(ctx, catalogView, bound.raw, viewLiteralPath{Clause: bound.clause, Source: occurrence})
			if err != nil {
				return "", err
			}
			if source.Bounds.WithTies && bound.name == "LIMIT" {
				text += " FETCH FIRST (" + part + ") ROWS WITH TIES"
			} else {
				text += " " + bound.name + " " + part
			}
		}
	}
	return text, nil
}

func (v *viewDefinition) validateValuesLiterals(catalog catalog.Read, source *parser.ValuesQuery, frame uint32, occurrence int, count, columns, composites, intaggCount *int) error {
	validate := func(raw parser.Expr, clause viewLiteralClause, row, column int) error {
		ordinal := 0
		return v.validateLiteralTree(catalog, raw, viewLiteralPath{Query: frame, Clause: clause, Source: occurrence, Row: row, Target: column}, &ordinal, count, columns, composites, intaggCount)
	}
	for i, row := range source.Rows {
		for j, raw := range row {
			if err := validate(raw, analyze.ViewValuesCellLiteral, i, j); err != nil {
				return err
			}
		}
	}
	if source.Order != nil {
		for i, key := range source.Order.Keys {
			if err := validate(key.Expr, analyze.ViewValuesOrderLiteral, 0, i); err != nil {
				return err
			}
		}
	}
	if source.Bounds != nil {
		if err := validate(source.Bounds.Offset, analyze.ViewValuesOffsetLiteral, 0, 0); err != nil {
			return err
		}
		if err := validate(source.Bounds.Count, analyze.ViewValuesLimitLiteral, 0, 0); err != nil {
			return err
		}
	}
	return nil
}
