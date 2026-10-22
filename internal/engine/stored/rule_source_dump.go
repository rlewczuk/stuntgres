package stored

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

func ruleSourceColumnSQL(view catalog.Read, root plan.Node, slot int, action RuleAction, expression func(plan.Node, expr.Expr) (string, error)) (string, error) {
	if root == nil || slot < 0 || slot >= len(root.Schema()) {
		return "", fmt.Errorf("stuntgres: invalid rule source slot")
	}
	switch node := root.(type) {
	case *plan.JoinNode:
		if slot >= node.LeftWidth+node.RightWidth {
			merged := slot - node.LeftWidth - node.RightWidth
			if merged >= len(node.Merges) {
				return "", ids.ErrCatalogReplaced
			}
			return expression(node, node.Merges[merged])
		}
		if slot < len(node.Left.Schema()) {
			return ruleSourceColumnSQL(view, node.Left, slot, action, expression)
		}
		return ruleSourceColumnSQL(view, node.Right, slot-len(node.Left.Schema()), action, expression)
	case *plan.SourceNode:
		d, ok := view.Relation(node.Ref.Address)
		if !ok {
			return "", ids.ErrCatalogReplaced
		}
		leaf := action.binding.QuerySources[node.Occurrence]
		attributes := leaf.FrozenAttributes
		if attributes == nil {
			attributes = leaf.Relation.Attributes
		}
		if slot >= len(attributes) {
			return "", ids.ErrCatalogReplaced
		}
		column, err := DeparseAttribute(d, attributes[slot])
		if err != nil {
			return "", err
		}
		return catalog.QuoteIdentifier("_rule_source_"+strconv.Itoa(node.Occurrence)) + "." + column, nil
	case *plan.FunctionFromNode:
		return catalog.QuoteIdentifier("_rule_function_"+strconv.Itoa(node.ViewOrdinal)) + "." + catalog.QuoteIdentifier(node.Schema()[slot].Name), nil
	case *plan.DerivedNode:
		return catalog.QuoteIdentifier("_rule_source_"+strconv.Itoa(node.Ordinal)) + "." + catalog.QuoteIdentifier(node.Schema()[slot].Name), nil
	case *plan.ProjectNode, *plan.SortNode, *plan.LimitNode, *plan.EvaluationNode:
		values, ok := analyze.ViewFromRoot(node).(*plan.ValuesNode)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing rule VALUES namespace")
		}
		return catalog.QuoteIdentifier("_rule_values_"+strconv.Itoa(values.ViewOrdinal)) + "." + catalog.QuoteIdentifier(node.Schema()[slot].Name), nil
	case *plan.ValuesNode:
		return catalog.QuoteIdentifier("_rule_values") + "." + catalog.QuoteIdentifier(node.Schema()[slot].Name), nil
	}
	return "", fmt.Errorf("stuntgres: missing rule source namespace for %T", root)
}

// VALUES FROM binding retains projection/order/bounds wrappers. Render its
// checked presentation instead of flattening those semantically real clauses.
func ruleValuesFromSQL(ctx context.Context, view catalog.Read, root plan.Node, action RuleAction) (string, error) {
	values, ok := analyze.ViewFromRoot(root).(*plan.ValuesNode)
	if !ok || action.presentation == nil {
		return "", fmt.Errorf("stuntgres: missing checked rule VALUES presentation")
	}
	presentation := action.presentation
	query := presentation.syntax
	if query == nil {
		return "", fmt.Errorf("stuntgres: missing rule source syntax")
	}
	project := presentation.queries[query]
	if project == nil {
		return "", fmt.Errorf("stuntgres: missing rule source frame")
	}
	var find func(parser.FromItem, plan.Node) *parser.ValuesQuery
	find = func(raw parser.FromItem, bound plan.Node) *parser.ValuesQuery {
		switch syntax := raw.(type) {
		case *parser.AliasReference:
			return find(syntax.Input, bound)
		case *parser.JoinReference:
			join, ok := analyze.ViewFromRoot(bound).(*plan.JoinNode)
			if !ok {
				return nil
			}
			if source := find(syntax.Left, join.Left); source != nil {
				return source
			}
			return find(syntax.Right, join.Right)
		case *parser.ValuesQuery:
			captured, ok := analyze.ViewFromRoot(bound).(*plan.ValuesNode)
			if ok && captured.ViewOrdinal == values.ViewOrdinal {
				return syntax
			}
		}
		return nil
	}
	source, err := presentation.fromRoot(project)
	if err != nil {
		return "", err
	}
	syntax := find(query.From, source)
	if syntax == nil {
		return "", fmt.Errorf("stuntgres: missing captured rule VALUES syntax")
	}
	renderer := NewQueryRenderer(presentation)
	renderer.Frame = project.Frame
	text, err := renderer.valuesSQL(ctx, view, syntax, values.ViewOrdinal)
	if err != nil {
		return "", err
	}
	names := make([]string, len(root.Schema()))
	for i, column := range root.Schema() {
		names[i] = catalog.QuoteIdentifier(column.Name)
	}
	return "(" + text + ") AS " + catalog.QuoteIdentifier("_rule_values_"+strconv.Itoa(values.ViewOrdinal)) + "(" + strings.Join(names, ", ") + ")", nil
}
