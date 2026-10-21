package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

// The optional candidate is membership deletion only. It shares immutable role
// descriptors/name maps and preserves the order and flags of surviving rows.
func (s *CatalogStage) validateRoleCandidate(current *access.RoleRecord) error {
	if s.roleCandidate == nil {
		return nil
	}
	if current.Revision != s.roleRevision {
		return fmt.Errorf("stuntgres: authorization changed before publication")
	}
	if current.Revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	next := s.roleCandidate
	if next.Revision != current.Revision+1 {
		return fmt.Errorf("stuntgres: invalid ownership role candidate")
	}
	return current.Root.ValidateDeletionCandidate(&next.Root)
}
func (s *CatalogStage) roleRead(current *access.RoleRecord, catalog catalog.Read) authorization.Read {
	if s.roleCandidate != nil {
		return s.owner.roles.read(s.owner.catalog.owner(), catalog, s.roleCandidate)
	}
	return s.owner.roles.read(s.owner.catalog.owner(), catalog, current)
}
