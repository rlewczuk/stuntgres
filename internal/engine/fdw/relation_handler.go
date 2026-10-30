package fdw

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
)

// RelationHandler admits an actual foreign occurrence without entering a provider.
// Capability inquiries and executable ACL fences share HandlerFor's policy.
func RelationHandler(read catalog.Read, relation *catalog.Relation, restricted settings.RestrictedRelationKinds) (*catalog.ForeignProcedure, error) {
	if relation.Foreign == nil {
		return nil, ids.ErrCatalogReplaced
	}
	server, ok := read.Foreign(relation.Foreign.Server)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	wrapper, ok := read.Foreign(server.Wrapper)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	return HandlerFor(read, wrapper, restricted)
}
