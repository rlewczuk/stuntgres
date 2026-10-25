package engine

import (
	"cmp"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// Routine DROP has only captured identity roots at this point. Hold those
// objects before discovering dependents, then use grant-time metadata without
// resolving the original names again or repeating conditional notices.
func acquireRoutineRetirementRoots(s *StatementScope, stage *CatalogStage) error {
	var scratch [8]ids.ObjectAddress
	addresses := append(scratch[:0], stage.DomainTargets...)
	slices.SortFunc(addresses, func(a, b ids.ObjectAddress) int { return cmp.Compare(a.ObjectID, b.ObjectID) })
	for _, address := range addresses {
		if err := s.AcquireCatalogResource(lock.Resource{ObjectRef: stage.Base.Domain().Ref(stage.Base.Epoch(), address)}, lock.AccessExclusive); err != nil {
			return err
		}
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := schemaAuthorizationLocked(e, s.Op)
	if err != nil {
		return err
	}
	if auth.Catalog.Epoch() != stage.Base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if auth.Revision != stage.AuthRevision || auth.RoleRecord.Revision != stage.RoleRevision {
		return catalog.Conflict{}
	}
	for _, address := range addresses {
		if auth.Catalog.Get(address) != stage.Base.Get(address) {
			return catalog.Conflict{}
		}
	}
	stage.Candidate = catalog.NewCandidate(auth.Catalog)
	stage.AuthorizeLocked(auth, s.Op.Session)
	return nil
}
