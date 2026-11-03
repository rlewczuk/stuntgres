package contrib

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
)

// PgvisibilityTruncate is a physical, non-journaled VM publication. It neither
// changes tuple headers/MVCC nor retains its AccessExclusive contribution until
// transaction end. Current routine/schema/EXECUTE and target lifetime are checked
// again after the real lock wait; every failure precedes the sole publication.
func PgvisibilityTruncate[A Authority](h Host[A], n plan.InstalledPgvisibilityExpr, oid uint32) error {
	if n.Kind != functions.VisibilityTruncateMap {
		return fmt.Errorf("stuntgres: invalid pg_visibility truncate operation")
	}
	if err := h.Active(); err != nil {
		return err
	}
	h.ReacquireEngine()
	auth, err := h.RoutineAuthorizationLocked()
	if err == nil {
		err = pgvisibilityAuthority(auth, n)
	}
	var target *catalog.Relation
	var store *storage.Relation
	if err == nil {
		target, _, err = amcheckTargetIdentity(auth.Access().Catalog, oid)
	}
	if err == nil {
		store = h.PrivateStorageLocked(target.Address())
	}
	epoch := h.CatalogEpochLocked()
	h.ReleaseEngineLocked()
	if err != nil {
		return err
	}
	ref := h.CatalogDomain().Ref(epoch, target.Address())
	var contribution lock.RelationContribution
	defer h.LockManager().ReleaseRelation(&contribution)
	if err := h.AcquireRelationContribution(ref, lock.AccessExclusive, &contribution); err != nil {
		return err
	}
	h.ReacquireEngine()
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil {
		err = pgvisibilityAuthority(auth, n)
	}
	if err == nil && (h.CatalogEpochLocked() != epoch || auth.Access().Catalog.Get(target.Address()) != target || h.PrivateStorageLocked(target.Address()) != store) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		h.MarkTemporaryTargetLocked(ref)
		err = pgvisibilityTargetCheckable(auth.Access().Catalog, target)
	}
	if err == nil && store == nil && target.RelationKind != catalog.CatalogRelation {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = h.Operation().Context().Err()
	}
	if err == nil {
		current := h.PgvisibilityMapLocked(target, store)
		if current == nil || !current.Invalidated {
			h.SetPgvisibilityMapLocked(target, store, storage.EmptyVisibilityRoot())
		}
	}
	h.ReleaseEngineLocked()
	return err
}
