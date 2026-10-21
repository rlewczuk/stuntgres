package stored

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
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
	case *plan.ValuesNode:
		return catalog.QuoteIdentifier("_rule_values") + "." + catalog.QuoteIdentifier(node.Schema()[slot].Name), nil
	}
	return "", fmt.Errorf("stuntgres: missing rule source namespace for %T", root)
}
