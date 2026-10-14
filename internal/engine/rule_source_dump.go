package engine

import (
	"fmt"
	"strconv"
)

func ruleSourceColumnSQL(view CatalogRead, root LogicalNode, slot int, action ruleAction, expression func(LogicalNode, BoundExpr) (string, error)) (string, error) {
	if root == nil || slot < 0 || slot >= len(root.Schema()) {
		return "", fmt.Errorf("stuntgres: invalid rule source slot")
	}
	switch node := root.(type) {
	case *joinNode:
		if slot >= node.leftWidth+node.rightWidth {
			merged := slot - node.leftWidth - node.rightWidth
			if merged >= len(node.merges) {
				return "", ErrCatalogReplaced
			}
			return expression(node, node.merges[merged])
		}
		if slot < len(node.left.Schema()) {
			return ruleSourceColumnSQL(view, node.left, slot, action, expression)
		}
		return ruleSourceColumnSQL(view, node.right, slot-len(node.left.Schema()), action, expression)
	case *sourceNode:
		d, ok := view.state.descriptors[node.ref.Address].(*relationDescriptor)
		if !ok {
			return "", ErrCatalogReplaced
		}
		leaf := action.binding.querySources[node.occurrence]
		attributes := leaf.frozenAttributes
		if attributes == nil {
			attributes = leaf.relation.attributes
		}
		if slot >= len(attributes) {
			return "", ErrCatalogReplaced
		}
		column, err := deparseAttribute(d, attributes[slot])
		if err != nil {
			return "", err
		}
		return quoteIdentifier("_rule_source_"+strconv.Itoa(node.occurrence)) + "." + column, nil
	case *functionFromNode:
		return quoteIdentifier("_rule_function_"+strconv.Itoa(node.viewOrdinal)) + "." + quoteIdentifier(node.Schema()[slot].Name), nil
	case *derivedNode:
		return quoteIdentifier("_rule_source_"+strconv.Itoa(node.ordinal)) + "." + quoteIdentifier(node.Schema()[slot].Name), nil
	case *valuesNode:
		return quoteIdentifier("_rule_values") + "." + quoteIdentifier(node.Schema()[slot].Name), nil
	}
	return "", fmt.Errorf("stuntgres: missing rule source namespace for %T", root)
}
