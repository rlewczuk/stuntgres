package core

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

// The optional candidate is membership deletion only. It shares immutable role
// descriptors/name maps and preserves the order and flags of surviving rows.
func (s *CatalogStage) validateRoleCandidate(current *access.RoleRecord) error {
	if s.RoleCandidateRecord == nil {
		return nil
	}
	if current.Revision != s.RoleRevision {
		return fmt.Errorf("stuntgres: authorization changed before publication")
	}
	if current.Revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	next := s.RoleCandidateRecord
	if next.Revision != current.Revision+1 {
		return fmt.Errorf("stuntgres: invalid ownership role candidate")
	}
	return current.Root.ValidateDeletionCandidate(&next.Root)
}
func (s *CatalogStage) roleRead(current *access.RoleRecord, catalog catalog.Read) authorization.Read {
	if s.RoleCandidateRecord != nil {
		return s.Owner.RoleStore.Read(&s.Owner.Catalog.Owner, catalog, s.RoleCandidateRecord)
	}
	return s.Owner.RoleStore.Read(&s.Owner.Catalog.Owner, catalog, current)
}
