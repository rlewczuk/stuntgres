package extension

import (
	"cmp"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// RemovalRoots preserves requested root order and authentic manifest identities.
// Newly appended members follow PostgreSQL's dependency visitation order rather
// than recipe slot order. The caller owns roots; published manifests stay intact.
func RemovalRoots(view catalog.Read, roots []ids.ObjectAddress) []ids.ObjectAddress {
	result := make([]ids.ObjectAddress, 0, len(roots))
	pending := make(map[ids.ObjectAddress]bool, len(roots))
	for _, root := range roots {
		if !pending[root] {
			result = append(result, root)
			pending[root] = true
		}
	}
	end := len(result)
	for _, root := range result[:end] {
		if extension, ok := view.Extension(root); ok {
			start := len(result)
			for _, member := range extension.Members {
				if !pending[member] {
					result = append(result, member)
					pending[member] = true
				}
			}
			// REL_18_6 dependency.c's object_address_comparator visits newer
			// identities first. A shared consumer retains its first reached
			// parent, so this order also determines its genuine DROP diagnostic.
			slices.SortFunc(result[start:], func(a, b ids.ObjectAddress) int {
				if order := cmp.Compare(b.ObjectID, a.ObjectID); order != 0 {
					return order
				}
				if order := cmp.Compare(a.ClassID, b.ClassID); order != 0 {
					return order
				}
				return cmp.Compare(uint32(a.SubID), uint32(b.SubID))
			})
		}
	}
	return result
}
