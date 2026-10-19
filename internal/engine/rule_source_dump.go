package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

func ruleSourceColumnSQL(view catalog.Read, root LogicalNode, slot int, action ruleAction, expression func(LogicalNode, expr.Expr) (string, error)) (string, error) {
	if root == nil || slot < 0 || slot >= len(root.Schema()) {
		return "", fmt.Errorf("stuntgres: invalid rule source slot")
	}
	switch node := root.(type) {
	case *joinNode:
		if slot >= node.leftWidth+node.rightWidth {
			merged := slot - node.leftWidth - node.rightWidth
			if merged >= len(node.merges) {
				return "", ids.ErrCatalogReplaced
			}
			return expression(node, node.merges[merged])
		}
		if slot < len(node.left.Schema()) {
			return ruleSourceColumnSQL(view, node.left, slot, action, expression)
		}
		return ruleSourceColumnSQL(view, node.right, slot-len(node.left.Schema()), action, expression)
	case *sourceNode:
		d, ok := view.Relation(node.ref.Address)
		if !ok {
			return "", ids.ErrCatalogReplaced
		}
		leaf := action.binding.querySources[node.occurrence]
		attributes := leaf.frozenAttributes
		if attributes == nil {
			attributes = leaf.relation.Attributes
		}
		if slot >= len(attributes) {
			return "", ids.ErrCatalogReplaced
		}
		column, err := deparseAttribute(d, attributes[slot])
		if err != nil {
			return "", err
		}
		return catalog.QuoteIdentifier("_rule_source_"+strconv.Itoa(node.occurrence)) + "." + column, nil
	case *functionFromNode:
		return catalog.QuoteIdentifier("_rule_function_"+strconv.Itoa(node.viewOrdinal)) + "." + catalog.QuoteIdentifier(node.Schema()[slot].Name), nil
	case *derivedNode:
		return catalog.QuoteIdentifier("_rule_source_"+strconv.Itoa(node.ordinal)) + "." + catalog.QuoteIdentifier(node.Schema()[slot].Name), nil
	case *valuesNode:
		return catalog.QuoteIdentifier("_rule_values") + "." + catalog.QuoteIdentifier(node.Schema()[slot].Name), nil
	}
	return "", fmt.Errorf("stuntgres: missing rule source namespace for %T", root)
}
