package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// catalogStateFixture builds an unpublished state at epoch that holds
// descriptors and names, through the state's write API.
func catalogStateFixture(epoch ids.CatalogEpoch, descriptors map[ids.ObjectAddress]catalog.Descriptor, names map[ids.NameKey]ids.ObjectAddress) *catalog.State {
	s := catalog.NewState(epoch)
	for a, d := range descriptors {
		s.SetDescriptor(a, d)
	}
	for k, a := range names {
		s.BindName(k, a)
	}
	return s
}

// ValidateObjectRef checks, under Engine.mu, that view and ref belong to e.
func (e *Engine) ValidateObjectRef(view catalog.Read, ref ids.ObjectRef) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.catalog.validateObjectRefLocked(view, ref)
}

// PublishCatalog publishes p outside any operation or statement.
func (e *Engine) PublishCatalog(p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	return e.publishCatalog(nil, nil, p, token)
}
