package engine

import "stuntgres/internal/engine/ids"

// catalogStore owns the engine's catalog state: the provenance domain, the
// published catalog snapshot, the provider registry and the identity
// allocators. It has no mutex. Engine.mu guards every field, and mutating
// methods carry the Locked suffix.
//
// Engine holds the store by value and never replaces it whole. The domain is
// allocated once per engine and kept across Reset and Restore; it is the
// engine's single provenance domain, so session tokens, transaction
// references and the lock manager use it as well as catalog references.
type catalogStore struct {
	domain         *ids.Domain
	state          *catalogState
	providers      map[ids.OID]CatalogProvider
	nextOID        ids.OID
	nextRevision   ids.DefinitionRevision
	nextEnumMember int64
}
