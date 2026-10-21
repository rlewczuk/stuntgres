package engine

import (
	"stuntgres/internal/engine/plan"
)

func QuerySetNode(node plan.Node) *plan.SetNode {
	switch n := node.(type) {
	case *plan.SetNode:
		return n
	case *plan.RecursiveUnionNode:
		return &n.SetNode
	}
	return nil
}
