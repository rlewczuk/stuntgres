package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/parser"
)

// VALUES ordinals follow structural source preorder, independent of physical
// table occurrences and source offsets. Row/key indices are local to a source.
func (b *expressionBinder) captureValuesViewLiteral(raw parser.Expr, bound BoundExpr, clause viewLiteralClause, source, row, column int) error {
	path := viewLiteralPath{query: b.frameID(), clause: clause, source: source, row: row, target: column}
	if clause == viewValuesOrderLiteral {
		if integer, ok := raw.(parser.LiteralExpr); ok && integer.Value.Kind == parser.IntegerValue {
			return nil
		}
	}
	ordinal := 0
	return b.captureViewLiteralTree(raw, bound, path, &ordinal)
}

func viewValuesAt(node LogicalNode, ordinal int) *valuesNode {
	var visit func(LogicalNode) *valuesNode
	visit = func(node LogicalNode) *valuesNode {
		switch n := node.(type) {
		case *valuesNode:
			if ordinal == n.viewOrdinal {
				return n
			}
		case *setNode:
			if found := visit(n.left); found != nil {
				return found
			}
			return visit(n.right)
		case *joinNode:
			if found := visit(n.left); found != nil {
				return found
			}
			return visit(n.right)
		case *projectNode:
			return visit(n.child)
		case *sortNode:
			return visit(n.child)
		case *limitNode:
			return visit(n.child)
		case *evaluationNode:
			return visit(n.child)
		case *filterNode:
			return visit(n.child)
		case *aggregateNode:
			return visit(n.child)
		case *windowNode:
			return visit(n.child)
		case *distinctNode:
			return visit(n.child)
		case *rowLockNode:
			return visit(n.child)
		}
		return nil
	}
	return visit(node)
}

func (v *viewRenderer) valuesSQL(ctx context.Context, catalog CatalogRead, source *parser.ValuesQuery, occurrence int) (string, error) {
	root := viewValuesAt(v.query.Root, occurrence)
	if root == nil {
		return "", fmt.Errorf("stuntgres: missing view VALUES occurrence")
	}
	slots := make(map[int]string, len(root.Schema()))
	for i, column := range root.Schema() {
		slots[i] = quoteIdentifier(column.Name)
	}
	v.slots[root] = slots
	rows := make([]string, len(source.Rows))
	for i, row := range source.Rows {
		cells := make([]string, len(row))
		for j, raw := range row {
			text, err := v.expressionSQL(ctx, catalog, raw, viewLiteralPath{clause: viewValuesCellLiteral, source: occurrence, row: i, target: j})
			if err != nil {
				return "", err
			}
			cells[j] = text
		}
		rows[i] = "(" + strings.Join(cells, ", ") + ")"
	}
	text := "VALUES " + strings.Join(rows, ", ")
	if source.Order != nil {
		keys := make([]string, len(source.Order.Keys))
		for i, key := range source.Order.Keys {
			part, err := v.expressionSQL(ctx, catalog, key.Expr, viewLiteralPath{clause: viewValuesOrderLiteral, source: occurrence, target: i})
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
			{source.Bounds.Offset, viewValuesOffsetLiteral, "OFFSET"},
			{source.Bounds.Count, viewValuesLimitLiteral, "LIMIT"},
		} {
			if bound.raw == nil {
				continue
			}
			part, err := v.expressionSQL(ctx, catalog, bound.raw, viewLiteralPath{clause: bound.clause, source: occurrence})
			if err != nil {
				return "", err
			}
			text += " " + bound.name + " " + part
		}
	}
	return text, nil
}

func (v *viewDefinition) validateValuesLiterals(catalog CatalogRead, source *parser.ValuesQuery, frame uint32, occurrence int, count, columns *int) error {
	validate := func(raw parser.Expr, clause viewLiteralClause, row, column int) error {
		ordinal := 0
		return v.validateLiteralTree(catalog, raw, viewLiteralPath{query: frame, clause: clause, source: occurrence, row: row, target: column}, &ordinal, count, columns)
	}
	for i, row := range source.Rows {
		for j, raw := range row {
			if err := validate(raw, viewValuesCellLiteral, i, j); err != nil {
				return err
			}
		}
	}
	if source.Order != nil {
		for i, key := range source.Order.Keys {
			if err := validate(key.Expr, viewValuesOrderLiteral, 0, i); err != nil {
				return err
			}
		}
	}
	if source.Bounds != nil {
		if err := validate(source.Bounds.Offset, viewValuesOffsetLiteral, 0, 0); err != nil {
			return err
		}
		if err := validate(source.Bounds.Count, viewValuesLimitLiteral, 0, 0); err != nil {
			return err
		}
	}
	return nil
}
