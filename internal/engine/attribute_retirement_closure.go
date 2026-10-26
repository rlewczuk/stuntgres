package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Only ordinary inheritance contributes generated retirement descendants.
// Partition layouts finish their local history before attachment.
func attributeRetirementClosure(ctx context.Context, view catalog.Read, root ids.ObjectAddress) ([]ids.ObjectAddress, error) {
	queue := []ids.ObjectAddress{root}
	seen := make(map[ids.ObjectAddress]bool)
	var result []ids.ObjectAddress
	for len(queue) != 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		address := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[address] {
			continue
		}
		seen[address] = true
		if _, ok := view.Relation(address); !ok {
			continue
		}
		result = append(result, address)
		for _, child := range catalog.InheritanceChildren(view, address) {
			queue = append(queue, child.Address())
		}
	}
	return result, nil
}
