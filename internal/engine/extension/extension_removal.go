package extension

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// RemovalRoots preserves root order and authentic manifest identities.
// The caller owns roots; published extension manifests are never modified.
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
			for _, member := range extension.Members {
				if !pending[member] {
					result = append(result, member)
					pending[member] = true
				}
			}
		}
	}
	return result
}
