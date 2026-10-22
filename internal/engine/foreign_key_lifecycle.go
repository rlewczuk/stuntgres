package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
)

func (s *StatementScope) acquireForeignKeyTypeProviders(view catalog.Read, address ids.ObjectAddress) error {
	for _, ref := range table.ForeignKeyTypeProviders(view, s.engine().catalog.Domain, address) {
		if err := s.foreignKeyGrant(ref, 0, lock.RowShare, nil); err != nil {
			return err
		}
	}
	return nil
}

// Nested CREATE SCHEMA definitions share the normal candidate. Newly created
// providers need no live grant; external parents retain the canonical DDL mode.
func (s *StatementScope) acquireForeignKeyDefinitionProviders(stage *CatalogStage) error {
	refs := table.ForeignKeyDefinitionProviders(stage.Base, stage.owner.catalog.Domain, stage.Descriptors())
	if len(refs) == 0 {
		return nil
	}
	for _, ref := range refs {
		if err := s.foreignKeyGrant(ref, 0, lock.ShareRowExclusive, nil); err != nil {
			return err
		}
	}
	auth, err := operationAuthorization(s.op)
	if err != nil {
		return err
	}
	e := stage.owner
	e.mu.Lock()
	auth.Catalog = e.privateCatalogLocked(s.owner.Catalog)
	auth.Recapture(auth.Catalog)
	e.mu.Unlock()
	return table.CheckForeignKeyDefinitionProviders(auth.View, auth, stage.Base, stage.Descriptors())
}
