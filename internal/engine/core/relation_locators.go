package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// prepareReplacementLocatorsLocked updates only real same-object heap
// replacements (TRUNCATE, REFRESH and row-shape rewrites), plus their rebuilt
// indexes. Metadata-only projections may replace a Go store without replacing
// its physical incarnation; rewritten supplies the admitted publication intent.
// Caller holds Engine.mu; every allocation precedes publication mutations.
func (e *Engine) prepareReplacementLocatorsLocked(next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, rewritten func(ids.ObjectAddress) bool) (*catalog.State, error) {
	if next == nil {
		return nil, nil
	}
	var replaced map[ids.ObjectAddress]bool
	for address, store := range relations {
		old := e.Storage.Relations[address]
		if old == nil || old == store || !rewritten(address) {
			continue
		}
		if replaced == nil {
			replaced = make(map[ids.ObjectAddress]bool)
		}
		replaced[address] = true
	}
	if len(replaced) == 0 {
		return next, nil
	}
	// The private candidate may itself be retained by an older prepared/query
	// owner. Locator publication owns another immutable catalog incarnation.
	next = catalog.CloneState(next)
	view := catalog.NewRead(&e.Catalog.Owner, next)
	for address, descriptor := range next.Relations() {
		if !replaced[address] && (descriptor.SQLIndex == nil || !replaced[descriptor.SQLIndex.Parent]) {
			continue
		}
		if descriptor.EffectiveStorageLocator(view) == 0 {
			continue
		}
		locator, err := e.Catalog.AllocateOIDLocked()
		if err != nil {
			return nil, err
		}
		clone := *descriptor
		clone.StorageLocator = locator
		next.SetDescriptor(address, &clone)
	}
	return next, nil
}
