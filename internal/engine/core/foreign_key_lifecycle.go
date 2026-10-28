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
	e := stage.Owner
	for _, ref := range refs {
		mode := lock.ShareRowExclusive
		// ALTER admission already folds ADD and stored-toggle requirements.
		// Reuse its AX grant instead of adding an independent SRE bit during
		// definition-provider readmission; still perform ordinary revalidation.
		if e.Locks.Holds(s.LockTicket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive) {
			mode = lock.AccessExclusive
		}
		if err := s.ForeignKeyGrant(ref, 0, mode, nil); err != nil {
			return err
		}
	}
	auth, err := OperationAuthorization(s.Op)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	auth.Catalog = e.PrivateCatalogLocked(s.PendingCatalog())
	auth.Recapture(auth.Catalog)
	e.Mu.Unlock()
	return table.CheckForeignKeyDefinitionProviders(auth.View, auth, stage.Base, stage.Descriptors())
}
