package core

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
	return s.Owner.Catalog.Domain.Ref(s.Base.Epoch(), address)
}

func (s *CatalogStage) RoleCandidate() *access.RoleRecord { return s.RoleCandidateRecord }

func (s *CatalogStage) SetRoleCandidate(candidate, publication *access.RoleRecord) authorization.Read {
	s.RoleCandidateRecord = candidate
	return s.roleRead(publication, s.Owner.StageRead(s))
}

func (s *CatalogStage) ReadRoleCandidate(candidate, publication *access.RoleRecord) authorization.Read {
	if candidate != nil {
		return s.Owner.RoleStore.Read(&s.Owner.Catalog.Owner, s.StageRead(), candidate)
	}
	return s.Owner.RoleStore.Read(&s.Owner.Catalog.Owner, s.StageRead(), publication)
}
