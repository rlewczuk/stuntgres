package engine

import (
	"iter"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
)

// ownerStage is the catalog stage that ALTER ... OWNER and REASSIGN OWNED
// stage new owners into. *CatalogStage implements it.
type ownerStage interface {
	ddl.Stage
	// BaseCatalog is the catalog the stage was begun from.
	BaseCatalog() catalog.Read
	// CascadeRevision is the next definition revision of a replaced
	// descriptor; terminal cleanup may saturate it.
	CascadeRevision(ids.DefinitionRevision) (ids.DefinitionRevision, error)
}

// ownedCleanupStage is the catalog stage DROP OWNED cleans ACLs, policies,
// default privileges and memberships in. *CatalogStage implements it.
type ownedCleanupStage interface {
	ownerStage
	// BaseRef names address in the catalog domain at the base epoch.
	BaseRef(ids.ObjectAddress) ids.ObjectRef
	Remove(ids.ObjectRef) error
	Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	Removal(ids.ObjectAddress) (catalog.Removal, bool)
	Removals() iter.Seq2[ids.ObjectAddress, catalog.Removal]
	DefaultACLChange(catalog.DefaultACLKey) (*catalog.DefaultACLRecord, bool)
	ObserveDefault(catalog.DefaultACLKey)
	SetDefaultACL(catalog.DefaultACLKey, *catalog.DefaultACLRecord)
	// RoleCandidate is the membership deletion that joins this stage's
	// publication, or nil.
	RoleCandidate() *access.RoleRecord
	// SetRoleCandidate joins candidate to this stage's publication and returns
	// the roles as the staged catalog reads them: candidate's, or
	// publication's when candidate is nil.
	SetRoleCandidate(candidate, publication *access.RoleRecord) authorization.Read
}
