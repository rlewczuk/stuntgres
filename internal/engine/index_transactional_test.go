package engine

import (
	"slices"

	"stuntgres/internal/engine/storage"
)

// Preserve empty-vs-nil representation and detach keys as well as entry headers,
// so later mutation of either membership or typed key values is observable.
func transactionalIndexSnapshot(root *storage.IndexRoot) []storage.IndexEntry {
	entries := slices.Clone(root.Entries)
	for i := range entries {
		entries[i].Keys = slices.Clone(entries[i].Keys)
	}
	return entries
}
