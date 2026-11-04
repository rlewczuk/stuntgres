package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// admitDropRootInputs holds the existing genuine root definition resources
// before discovering incoming edges. It may acquire/wait, but never retargets a
// captured root to a replacement. Fresh incoming members are then discovered
// once from this actual session's current committed/private journal view.
func (p *DropProvider) admitDropRootInputs(roots []ids.ObjectAddress) error {
	scope := p.Scope
	if scope == nil || scope.LockTicket == nil {
		return nil
	}
	original := p.Base
	if err := scope.acquireRetiredDefinitionObjects(original, roots); err != nil {
		return err
	}
	e := p.Owner
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return err
	}
	if auth.Catalog.Owner() != original.Owner() || auth.Catalog.Epoch() != original.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for _, root := range roots {
		root.SubID = 0
		if auth.Catalog.Get(root) != original.Get(root) {
			return catalog.Conflict{}
		}
	}
	p.Base = auth.Catalog
	if p.Stage != nil {
		p.Stage.Base = auth.Catalog
	}
	if p.Prepared != nil {
		p.Prepared.Discovery = auth.Catalog
	}
	return nil
}
