package engine

import (
	"fmt"
)

// Persistent definition holds use the incumbent ticket and replacement fence.
// Admission occurs outside Engine.mu; after a wait identities are opened anew,
// never retargeted by name or evaluated to establish a dependency.
func (s *StatementScope) acquireDefinitionProviders(stage *CatalogStage) error {
	if err := s.acquireForeignKeyDefinitionProviders(stage); err != nil {
		return err
	}
	requests, retained, err := stage.tableSlot.DefinitionProviders(stage.Base, stage, stage.Descriptors(), stage.owner.catalog.Domain)
	if err != nil {
		return err
	}
	e := s.engine()
	for _, request := range requests {
		ref := request.Ref
		if err := s.acquireSequence(ref, request.Mode, true); err != nil {
			return err
		}
		e.mu.Lock()
		auth, err := e.authorizationLocked(s.op)
		if err == nil {
			provider, ok := auth.Catalog.Relation(ref.Address)
			original, existed := stage.Base.Relation(ref.Address)
			if !ok && existed {
				// The provider existed when the definition was analyzed and was
				// dropped while this statement waited for it (PostgreSQL 18.6).
				err = sqlError("42704", "referenced relation was concurrently dropped")
			} else if !ok || !existed || ref.Domain() != e.catalog.Domain || ref.Epoch != auth.Catalog.Epoch() || provider.RelationKind != original.RelationKind || retained[ref.Address] != nil && provider != retained[ref.Address] {
				err = fmt.Errorf("stuntgres: definition provider disappeared before publication")
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		return err
	}
	return stage.validateLikeSources(auth.Catalog)
}
