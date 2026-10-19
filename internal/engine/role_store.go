package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

// roleStore owns the engine's role state: the principal domain, the ACL
// authority, the committed role publication, the principal allocator and the
// registry of role references owned by other internal features. It has no
// mutex. Engine.mu guards every field, and mutating methods carry the Locked
// suffix.
//
// Engine holds the store by value and never replaces it whole:
// catalog.Owner.Principals, catalog.Owner.ACLAuthority and
// Root.Reprovenance keep pointers to its domain and ACL authority.
// Transaction overlays of the role publication live in catalogChanges, not
// here; published is only the committed record.
type roleStore struct {
	domain        authorization.Domain
	aclAuthority  sqltype.ACLAuthority
	published     *roleCatalog
	nextPrincipal uint64
	references    map[string][]authorization.PrincipalRef
}
