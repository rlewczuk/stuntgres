package index

import (
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// OverlayCapture is one ready index a transaction overlay must rebuild: the
// index, its parent and a detached copy of the parent's private store.
type OverlayCapture struct {
	Index, Parent *catalog.Relation
	Store         storage.Relation
}

// SelectOverlayCapturesLocked selects the indexes of view that a staged
// catalog change makes the overlay rebuild, in view order: those whose own or
// parent descriptor changed, whose rebuild is staged or whose parent heap c
// replaced. Unready indexes are forgotten in c, virtual (partitioned) ones
// are skipped, and staged or transported candidates that still match their
// store are kept. Each capture adds a committed storage read fence to c.
// owner is the transaction's journal before this statement. The caller holds
// Engine.mu; this takes no lock and returns catalog.Conflict for a missing
// parent or store, or a candidate that no longer matches.
func SelectOverlayCapturesLocked(published *storage.Store, view catalog.Read, staged *catalog.Candidate, stage Stage, owner, c *session.Journal) ([]OverlayCapture, error) {
	var captures []OverlayCapture
	for a, idx := range view.Relations() {
		if idx.SQLIndex == nil {
			continue
		}
		parent, ok := view.Relation(idx.SQLIndex.Parent)
		if !ok {
			return nil, catalog.Conflict{}
		}
		old, existed := staged.Base.Relation(parent.Address())
		shape, replaced := c.Storage[parent.Address()]
		storageChanged := replaced && shape.Replacement != session.PrivateStorage(published, owner, parent.Address())
		if !storageChanged && staged.Get(a) == nil && stage.Rebuilds[a] == nil && (!existed || slices.Equal(old.Attributes, parent.Attributes)) {
			continue
		}
		if !idx.SQLIndex.Ready {
			JournalOf(c.Indexes).Forget(a)
			continue
		}
		// Virtual indexes own ancestry/validity metadata, never heap captures or
		// installation roots. Their exact descriptor fences were captured above.
		if parent.Partition != nil {
			continue
		}
		// Unchanged key semantics permit root reuse only on the same storage
		// incarnation; a replacement heap needs its own physical baseline.
		if previous, ok := staged.Base.Relation(a); !storageChanged && ok && previous.SQLIndex != nil && previous.SQLIndex.Ready && stage.Rebuilds[a] == nil && existed && slices.Equal(old.Attributes, parent.Attributes) && SameIndexKeySemantics(previous, idx) {
			continue
		}
		store := session.PrivateStorage(published, c, parent.Address())
		if store == nil {
			return nil, catalog.Conflict{}
		}
		if _, produced := stage.Candidates[a]; produced {
			candidate, exists := JournalIndexesOrZero(c).Candidates[a]
			if !exists || !candidate.MatchesIndex(idx, store) {
				return nil, catalog.Conflict{}
			}
			continue
		}
		if _, transported := stage.Layouts[a]; transported {
			if candidate, exists := JournalIndexesOrZero(c).Candidates[a]; exists && candidate.MatchesIndex(idx, store) && stage.Rebuilds[a] == nil {
				continue
			}
		}
		snapshot := *store
		snapshot.IndexSource = store.IndexIdentity()
		captures = append(captures, OverlayCapture{Index: idx, Parent: parent, Store: snapshot})
		c.AddFence(session.NewStorageReadFence(parent.Address(), published.Relations[parent.Address()]))
	}
	return captures, nil
}
