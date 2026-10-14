package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/parser"
)

func viewCTEScan(root LogicalNode, match func(*derivedNode) bool) LogicalNode {
	switch n := viewFromRoot(root).(type) {
	case *derivedNode:
		if n.cte != nil && match(n) {
			return n
		}
	case *worktableScanNode:
		if match(&n.derivedNode) {
			return n
		}
	case *joinNode:
		if found := viewCTEScan(n.left, match); found != nil {
			return found
		}
		return viewCTEScan(n.right, match)
	}
	return nil
}
func viewCTETable(root LogicalNode, table *parser.TableReference) LogicalNode {
	return viewCTEScan(root, func(n *derivedNode) bool { return n.table == table })
}
func viewCTEOrdinal(root LogicalNode, ordinal int) LogicalNode {
	return viewCTEScan(root, func(n *derivedNode) bool { return n.ordinal == ordinal })
}

func (r *viewRenderer) selectQueryColumnsSQL(ctx context.Context, catalog CatalogRead, s *parser.Select, project *projectNode, columns []ColumnDescriptor, setOperand bool) (string, error) {
	prefix, err := r.withClauseSQL(ctx, catalog, s.With, s.WithRecursive)
	if err != nil {
		return "", err
	}
	text, err := r.selectQueryBodyColumnsSQL(ctx, catalog, s, project, columns, setOperand)
	if err != nil {
		return "", err
	}
	return prefix + text, nil
}

func (r *viewRenderer) withClauseSQL(ctx context.Context, catalog CatalogRead, raw []parser.CommonTableExpression, recursive bool, bodyRenderer ...func(parser.Statement) (string, error)) (string, error) {
	var definitions []string
	for _, definition := range raw {
		var body string
		var err error
		if len(bodyRenderer) != 0 {
			body, err = bodyRenderer[0](definition.Query)
		} else {
			selection, ok := definition.Query.(*parser.Select)
			if !ok {
				return "", fmt.Errorf("stuntgres: modifying WITH cannot be stored")
			}
			child := r.queries[selection]
			if child == nil {
				return "", fmt.Errorf("stuntgres: missing retained WITH body")
			}
			body, err = r.selectQueryColumnsSQL(ctx, catalog, selection, child, child.Schema(), false)
		}
		if err != nil {
			return "", err
		}
		name := quoteIdentifier(definition.Name.Text)
		if len(definition.Columns) != 0 {
			labels := make([]string, len(definition.Columns))
			for i, column := range definition.Columns {
				labels[i] = quoteIdentifier(column.Text)
			}
			name += "(" + strings.Join(labels, ", ") + ")"
		}
		hint := ""
		switch definition.Materialization {
		case parser.CTEMaterialized:
			hint = "MATERIALIZED "
		case parser.CTENotMaterialized:
			hint = "NOT MATERIALIZED "
		}
		definitions = append(definitions, name+" AS "+hint+"("+body+")")
	}
	if len(definitions) == 0 {
		return "", nil
	}
	modifier := "WITH "
	if recursive {
		modifier = "WITH RECURSIVE "
	}
	return modifier + strings.Join(definitions, ", ") + " ", nil
}
