package stored

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func viewCTEScan(root plan.Node, match func(*plan.DerivedNode) bool) plan.Node {
	switch n := analyze.ViewFromRoot(root).(type) {
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
func ViewCTETable(root plan.Node, table *parser.TableReference) plan.Node {
	return viewCTEScan(root, func(n *plan.DerivedNode) bool { return n.Table == table })
}
func ViewCTEOrdinal(root plan.Node, ordinal int) plan.Node {
	return viewCTEScan(root, func(n *plan.DerivedNode) bool { return n.Ordinal == ordinal })
}

func (r *QueryRenderer) selectQueryColumnsSQL(ctx context.Context, catalog catalog.Read, s *parser.Select, project *plan.ProjectNode, columns []catalog.Column, setOperand bool) (string, error) {
	if r.logicalSourceNames() && project != nil {
		r.ruleScopes = append(r.ruleScopes, project.Frame)
		defer func() { r.ruleScopes = r.ruleScopes[:len(r.ruleScopes)-1] }()
		if err := r.prepareRuleRangeTable(ctx, catalog, project); err != nil {
			return "", err
		}
	}
	prefix, err := r.WithClauseSQL(ctx, catalog, s.With, s.WithRecursive)
	if err != nil {
		return "", err
	}
	text, err := r.selectQueryBodyColumnsSQL(ctx, catalog, s, project, columns, setOperand)
	if err != nil {
		return "", err
	}
	return prefix + text, nil
}

func (r *QueryRenderer) cteBodySQL(ctx context.Context, read catalog.Read, statement parser.Statement, parent *ruleRangeTable, bodyRenderer []func(parser.Statement) (string, error)) (string, error) {
	policy := r.Presentation
	r.Presentation = policy.nestedQueryPresentation(true)
	if r.Presentation.Rule {
		// CTE execution schemas may contain the declaration's column names;
		// the body still prints its original target-entry names.
		r.Presentation.AnchorLabels = true
	}
	entryFrame, entryParent := r.ruleEntryFrame, r.ruleEntryParent
	defer func() {
		r.Presentation = policy
		r.ruleEntryFrame, r.ruleEntryParent = entryFrame, entryParent
	}()
	if selection, ok := statement.(*parser.Select); r.Presentation.Rule && ok {
		if project := r.queries[selection]; project != nil {
			r.ruleEntryFrame, r.ruleEntryParent = project.Frame, parent
		}
	}
	if len(bodyRenderer) != 0 {
		return bodyRenderer[0](statement)
	}
	selection, ok := statement.(*parser.Select)
	if !ok {
		if r.ruleAction != nil {
			return r.ruleModifierSQL(ctx, read, statement, parent)
		}
		return "", fmt.Errorf("stuntgres: modifying WITH cannot be stored")
	}
	child := r.queries[selection]
	if child == nil {
		return "", fmt.Errorf("stuntgres: missing retained WITH body")
	}
	return r.selectQueryColumnsSQL(ctx, read, selection, child, child.Schema(), false)
}

func (r *QueryRenderer) WithClauseSQL(ctx context.Context, catalogView catalog.Read, raw []parser.CommonTableExpression, recursive bool, bodyRenderer ...func(parser.Statement) (string, error)) (string, error) {
	policy := r.Presentation
	identifier := catalog.QuoteIdentifier
	if policy.Rule {
		identifier = policy.identifier
	}
	var definitions []string
	parent := r.ruleNameFacts(len(bodyRenderer) != 0)
	for _, definition := range raw {
		body, err := r.cteBodySQL(ctx, catalogView, definition.Query, parent, bodyRenderer)
		if err != nil {
			return "", err
		}
		name := catalog.QuoteIdentifier(definition.Name.Text)
		if policy.Rule {
			name = policy.identifier(definition.Name.Text)
		}
		if len(definition.Columns) != 0 {
			labels := make([]string, len(definition.Columns))
			for i, column := range definition.Columns {
				labels[i] = catalog.QuoteIdentifier(column.Text)
				if policy.Rule {
					labels[i] = policy.identifier(column.Text)
				}
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
				keys[i] = identifier(key.Text)
			}
			mode := "DEPTH"
			if search.Breadth {
				mode = "BREADTH"
			}
			suffix = " SEARCH " + mode + " FIRST BY " + strings.Join(keys, ", ") + " SET " + identifier(search.Sequence.Text)
		}
		if cycle := definition.Cycle; cycle != nil {
			keys := make([]string, len(cycle.Keys))
			for i, key := range cycle.Keys {
				keys[i] = identifier(key.Text)
			}
			suffix += " CYCLE " + strings.Join(keys, ", ") + " SET " + identifier(cycle.Mark.Text)
			if cycle.Value != nil {
				project := r.queries[definition.Query.(*parser.Select)]
				union, ok := analyze.ViewFromRoot(project).(*plan.RecursiveUnionNode)
				if !ok || union.Cycle == nil {
					return "", fmt.Errorf("stuntgres: missing stored cycle recipe")
				}
				var value string
				var err error
				deparser := Deparser{View: catalogView, Path: policy.Path, Output: policy.Output, NonstandardStrings: policy.NonstandardStrings}
				if policy.Rule {
					value, err = deparser.SQL(ctx, union.Cycle.Mark, DeparseParent{})
				} else {
					value, err = cycleConstantSQL(catalogView, cycle.Value, union.Cycle.TypedConstants[0])
				}
				if err != nil {
					return "", err
				}
				var fallback string
				if policy.Rule {
					fallback, err = deparser.SQL(ctx, union.Cycle.Fallback, DeparseParent{})
				} else {
					fallback, err = cycleConstantSQL(catalogView, cycle.Default, union.Cycle.TypedConstants[1])
				}
				if err != nil {
					return "", err
				}
				suffix += " TO " + value + " DEFAULT " + fallback
			}
			suffix += " USING " + identifier(cycle.Path.Text)
		}
		if policy.Rule {
			definitions = append(definitions, name+" AS "+hint+"("+policy.line(8)+body+policy.line(8)+")"+suffix)
		} else {
			definitions = append(definitions, name+" AS "+hint+"("+body+")"+suffix)
		}
	}
	if len(definitions) == 0 {
		return "", nil
	}
	modifier := "WITH "
	if recursive {
		modifier = "WITH RECURSIVE "
	}
	if policy.Rule {
		return " " + modifier + strings.Join(definitions, ","+policy.line(0)) + policy.line(0), nil
	}
	return modifier + strings.Join(definitions, ", ") + " ", nil
}

func cycleConstantSQL(catalog catalog.Read, raw parser.Expr, source sqltype.Usage) (string, error) {
	switch constant := raw.(type) {
	case parser.TypedLiteralExpr:
		// The introducer must use the typed-literal binder's source modifier,
		// never the common output Usage applied after that input conversion.
		typ, err := (Deparser{View: catalog}).typeSQL(source)
		if err != nil {
			return "", err
		}
		return typ + " " + dump.CommentLiteral(constant.Text), nil
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
			return dump.CommentLiteral(value.Text), nil
		case parser.BitStringValue:
			return value.Text[:1] + "'" + value.Text[1:] + "'", nil
		}
	}
	return "", fmt.Errorf("stuntgres: invalid stored cycle constant")
}
