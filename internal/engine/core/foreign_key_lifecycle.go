package core

import (
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
)

// Nested CREATE SCHEMA definitions share the normal candidate. Newly created
// providers need no live grant; external parents retain the canonical DDL mode.
func (s *StatementScope) acquireForeignKeyDefinitionProviders(stage *CatalogStage) error {
	refs := table.ForeignKeyDefinitionProviders(stage.Base, stage.Owner.Catalog.Domain, stage.Descriptors())
	if len(refs) == 0 {
		return nil
	}
	for _, ref := range refs {
		if err := s.ForeignKeyGrant(ref, 0, lock.ShareRowExclusive, nil); err != nil {
			return err
		}
	}
	auth, err := OperationAuthorization(s.Op)
	if err != nil {
		return err
	}
	e := stage.Owner
	e.Mu.Lock()
	auth.Catalog = e.PrivateCatalogLocked(s.PendingCatalog())
	auth.Recapture(auth.Catalog)
	e.Mu.Unlock()
	return table.CheckForeignKeyDefinitionProviders(auth.View, auth, stage.Base, stage.Descriptors())
}
