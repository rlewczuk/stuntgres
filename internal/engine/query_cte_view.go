package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func viewCTEScan(root plan.Node, match func(*plan.DerivedNode) bool) plan.Node {
	switch n := viewFromRoot(root).(type) {
	case *plan.DerivedNode:
		if n.CTE != nil && match(n) {
			return n
		}
	case *plan.WorktableScanNode:
		if match(&n.DerivedNode) {
			return n
		}
	case *plan.JoinNode:
		if found := viewCTEScan(n.Left, match); found != nil {
			return found
		}
		return viewCTEScan(n.Right, match)
	}
	return nil
}
func viewCTETable(root plan.Node, table *parser.TableReference) plan.Node {
	return viewCTEScan(root, func(n *plan.DerivedNode) bool { return n.Table == table })
}
func viewCTEOrdinal(root plan.Node, ordinal int) plan.Node {
	return viewCTEScan(root, func(n *plan.DerivedNode) bool { return n.Ordinal == ordinal })
}

func (r *viewRenderer) selectQueryColumnsSQL(ctx context.Context, catalog catalog.Read, s *parser.Select, project *plan.ProjectNode, columns []catalog.Column, setOperand bool) (string, error) {
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

func (r *viewRenderer) withClauseSQL(ctx context.Context, catalogView catalog.Read, raw []parser.CommonTableExpression, recursive bool, bodyRenderer ...func(parser.Statement) (string, error)) (string, error) {
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
			body, err = r.selectQueryColumnsSQL(ctx, catalogView, selection, child, child.Schema(), false)
		}
		if err != nil {
			return "", err
		}
		name := catalog.QuoteIdentifier(definition.Name.Text)
		if len(definition.Columns) != 0 {
			labels := make([]string, len(definition.Columns))
			for i, column := range definition.Columns {
				labels[i] = catalog.QuoteIdentifier(column.Text)
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
		suffix := ""
		if search := definition.Search; search != nil {
			keys := make([]string, len(search.Keys))
			for i, key := range search.Keys {
				keys[i] = catalog.QuoteIdentifier(key.Text)
			}
			mode := "DEPTH"
			if search.Breadth {
				mode = "BREADTH"
			}
			suffix = " SEARCH " + mode + " FIRST BY " + strings.Join(keys, ", ") + " SET " + catalog.QuoteIdentifier(search.Sequence.Text)
		}
		if cycle := definition.Cycle; cycle != nil {
			keys := make([]string, len(cycle.Keys))
			for i, key := range cycle.Keys {
				keys[i] = catalog.QuoteIdentifier(key.Text)
			}
			suffix += " CYCLE " + strings.Join(keys, ", ") + " SET " + catalog.QuoteIdentifier(cycle.Mark.Text)
			if cycle.Value != nil {
				project := r.queries[definition.Query.(*parser.Select)]
				union, ok := viewFromRoot(project).(*plan.RecursiveUnionNode)
				if !ok || union.Cycle == nil {
					return "", fmt.Errorf("stuntgres: missing stored cycle recipe")
				}
				value, err := cycleConstantSQL(catalogView, cycle.Value, union.Cycle.TypedConstants[0])
				if err != nil {
					return "", err
				}
				fallback, err := cycleConstantSQL(catalogView, cycle.Default, union.Cycle.TypedConstants[1])
				if err != nil {
					return "", err
				}
				suffix += " TO " + value + " DEFAULT " + fallback
			}
			suffix += " USING " + catalog.QuoteIdentifier(cycle.Path.Text)
		}
		definitions = append(definitions, name+" AS "+hint+"("+body+")"+suffix)
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

func cycleConstantSQL(catalog catalog.Read, raw parser.Expr, source sqltype.Usage) (string, error) {
	switch constant := raw.(type) {
	case parser.TypedLiteralExpr:
		// The introducer must use the typed-literal binder's source modifier,
		// never the common output Usage applied after that input conversion.
		typ, err := (pgRenderer{view: catalog}).typeSQL(source)
		if err != nil {
			return "", err
		}
		return typ + " " + commentLiteral(constant.Text), nil
	case parser.LiteralExpr:
		switch value := constant.Value; value.Kind {
		case parser.NullValue:
			return "NULL", nil
		case parser.BooleanValue:
			if value.Boolean {
				return "TRUE", nil
			}
			return "FALSE", nil
		case parser.IntegerValue:
			return strconv.FormatInt(value.Integer, 10), nil
		case parser.NumericValue:
			return value.Text, nil
		case parser.StringValue:
			return commentLiteral(value.Text), nil
		case parser.BitStringValue:
			return value.Text[:1] + "'" + value.Text[1:] + "'", nil
		}
	}
	return "", fmt.Errorf("stuntgres: invalid stored cycle constant")
}
