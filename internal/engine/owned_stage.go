package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
)

var _ role.OwnedCleanupStage = (*CatalogStage)(nil)

func (s *CatalogStage) BaseCatalog() catalog.Read { return s.Base }

func (s *CatalogStage) CascadeRevision(revision ids.DefinitionRevision) (ids.DefinitionRevision, error) {
	return s.cascadeMetadataRevision(revision)
}

func (s *CatalogStage) BaseRef(address ids.ObjectAddress) ids.ObjectRef {
	return s.owner.catalog.Domain.Ref(s.Base.Epoch(), address)
}

func (s *CatalogStage) RoleCandidate() *access.RoleRecord { return s.roleCandidate }

func (s *CatalogStage) SetRoleCandidate(candidate, publication *access.RoleRecord) authorization.Read {
	s.roleCandidate = candidate
	return s.roleRead(publication, s.owner.stageRead(s))
}
