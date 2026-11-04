package contrib

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func pgvisibilityAuthority[A Authority](auth A, n plan.InstalledPgvisibilityExpr) error {
	view := auth.Access()
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != view.Catalog.Owner().Domain || n.Ref.Epoch != view.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := view.Catalog.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return ids.ErrCatalogReplaced
	}
	if err := extension.ValidatePgvisibilityIdentity(view.Catalog, current, n.Ref); err != nil {
		return err
	}
	boundary := n.Pgvisibility
	boundary.RoutineReference = current
	if err := extension.ValidatePgvisibilityCall(view.Catalog, current, boundary); err != nil {
		return err
	}
	if err := auth.Check(access.Requirement{Object: view.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil {
		return err
	}
	// Execute MAC belongs to the original expression initializer, not each
	// entered VPC body. Retain current ordinary SQL ACL admission here.
	return auth.Check(access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege, RoutineACLOnly: true})
}

// pgvisibilityTargetCheckable reads descriptor facts only. Target kind precedes
// block validation; TEMP buffer policy belongs to the captured physical layout.
func pgvisibilityTargetCheckable(v catalog.Read, target *catalog.Relation) error {
	label := ""
	if target.RelationKind == catalog.CatalogRelation && target.CatalogView {
		label = "views"
	} else {
		switch target.RelationKind {
		case catalog.TableRelation, catalog.MaterializedViewRelation, catalog.CatalogRelation:
		case catalog.PartitionedRelation:
			label = "partitioned tables"
		case catalog.ViewRelation:
			label = "views"
		case catalog.ForeignRelation:
			label = "foreign tables"
		case catalog.SequenceRelation:
			label = "sequences"
		case catalog.IndexRelation:
			label = "indexes"
			if target.SQLIndex != nil {
				if parent, ok := v.Relation(target.SQLIndex.Parent); ok && parent.Partition != nil {
					label = "partitioned indexes"
				}
			}
		default:
			label = "composite types"
		}
	}
	if label == "" {
		return nil
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf("relation %q is of wrong relation kind", target.Name()), Detail: "This operation is not supported for " + label + "."}
}

func Pgvisibility[A Authority](h Host[A], n plan.InstalledPgvisibilityExpr, oid uint32, block int64) (storage.VisibilityMap, error) {
	if n.Kind.Check() || n.Kind == functions.VisibilityTruncateMap {
		return storage.VisibilityMap{}, fmt.Errorf("stuntgres: invalid pg_visibility READ operation")
	}
	result, _, err := capturePgvisibility(h, n, oid, block)
	return result, err
}

func PgvisibilityCheck[A Authority](h Host[A], n plan.InstalledPgvisibilityExpr, oid uint32) ([]ids.VersionID, error) {
	if !n.Kind.Check() {
		return nil, fmt.Errorf("stuntgres: invalid pg_visibility check operation")
	}
	_, findings, err := capturePgvisibility(h, n, oid, 0)
	return findings, err
}

func capturePgvisibility[A Authority](h Host[A], n plan.InstalledPgvisibilityExpr, oid uint32, block int64) (storage.VisibilityMap, []ids.VersionID, error) {
	if err := h.Active(); err != nil {
		return storage.VisibilityMap{}, nil, err
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
		return storage.VisibilityMap{}, nil, err
	}
	ref := h.CatalogDomain().Ref(epoch, target.Address())
	var contribution lock.RelationContribution
	defer h.LockManager().ReleaseRelation(&contribution)
	if err := h.AcquireRelationContribution(ref, lock.AccessShare, &contribution); err != nil {
		return storage.VisibilityMap{}, nil, err
	}
	writes, deletes := h.ColumnProjectionRoots(target.Address())
	owners, err := h.LockHeapStatisticsCohort()
	if err != nil {
		return storage.VisibilityMap{}, nil, err
	}
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil && auth.Access().Catalog.Epoch() != epoch {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = pgvisibilityAuthority(auth, n)
	}
	if err == nil && (auth.Access().Catalog.Get(target.Address()) != target || h.PrivateStorageLocked(target.Address()) != store) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		h.MarkTemporaryTargetLocked(ref)
		err = pgvisibilityTargetCheckable(auth.Access().Catalog, target)
	}
	if err == nil && n.Kind.Block() && (block < 0 || block > 4294967294) {
		err = sqlError("22023", "invalid block number")
	}
	var facts []storage.HeapTupleFact
	var horizon storage.VisibilityHorizon
	var checkHorizon storage.VisibilityCheckHorizon
	var snapshot storage.Relation
	var root *storage.VisibilityRoot
	foreign := false
	if err == nil {
		root = h.PgvisibilityMapLocked(target, store)
		if namespace, ok := auth.Access().Catalog.Namespace(target.Namespace()); ok {
			foreign = namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != h.Operation().Session.SessionToken
		}
		if store != nil {
			snapshot = *store
			facts = h.HeapStatisticsFactsLocked(owners, store, target.Address(), writes, deletes)
			horizon = h.PgvisibilityHorizonLocked(owners)
			checkHorizon = h.PgvisibilityCheckHorizonLocked()
		} else if target.RelationKind != catalog.CatalogRelation {
			err = ids.ErrCatalogReplaced
		}
	}
	h.UnlockHeapStatisticsCohortLocked(owners)
	if err != nil {
		return storage.VisibilityMap{}, nil, err
	}
	if foreign && n.Kind.Check() {
		return storage.VisibilityMap{}, nil, sqlError("0A000", "cannot access temporary tables of other sessions")
	}
	var heap storage.VisibilityMap
	var prepared storage.PreparedVisibilityFacts
	if store == nil {
		bytes, failure := h.PgvisibilityCatalogBytes(ref, target)
		err = failure
		if err == nil {
			heap, err = storage.CatalogVisibility(bytes)
		}
	} else {
		prepared = storage.PrepareVisibilityFacts(facts, snapshot.Headers)
		facts = prepared
		count := 0
		for _, fact := range facts {
			if !fact.LinePointerOnly {
				count++
			}
		}
		maintenance := make([]lock.TupleMaintenance, count)
		at := 0
		for i := range facts {
			if facts[i].LinePointerOnly {
				continue
			}
			resource, failure := exec.TupleResource(ref, facts[i].Version, snapshot.Headers)
			if failure != nil {
				return storage.VisibilityMap{}, nil, failure
			}
			maintenance[at].Tuple = lock.MultiTuple{Resource: resource, Version: facts[i].Version.Version}
			at++
		}
		// Manager sampling is outside every owner/Engine mutex. Raw xmax
		// maintenance outlives its owner until successful full-XID retirement.
		if len(maintenance) != 0 {
			err = h.LockManager().CaptureTupleMaintenance(h.Operation().Context(), h.Ticket(), maintenance)
		}
		if err == nil {
			at = 0
			for i := range facts {
				if facts[i].LinePointerOnly {
					continue
				}
				raw := maintenance[at]
				at++
				facts[i].LockMaintenance = raw.Present && (raw.Active || raw.NewestFullXID == 0 || raw.NewestFullXID >= snapshot.Maintenance.FrozenXID)
			}
			heap, err = prepared.Capture(h.Operation().Context(), horizon, false)
		}
	}
	if err != nil {
		return storage.VisibilityMap{}, nil, err
	}
	result := heap
	var publish *storage.VisibilityRoot
	if root != nil && root.Invalidated {
		result = storage.SuppressedVisibility(heap)
	} else if store != nil {
		if root != nil && root.Basis.Same(&snapshot, facts) {
			// A READ sufficiency frontier cannot erase a physical marked claim
			// before a consistency check independently tests its tuple truth.
			if n.Kind.Check() || root.Proof == horizon {
				result = root.Map
				result.PageRuns = heap.Runs
			}
		} else {
			publish = &storage.VisibilityRoot{Map: heap, Basis: prepared.Basis(&snapshot), Proof: horizon}
		}
	}
	var findings []ids.VersionID
	if n.Kind.Check() {
		findings, err = prepared.Check(h.Operation().Context(), snapshot.Headers, result, checkHorizon, n.Kind == functions.VisibilityCheckFrozen)
		if err != nil {
			return storage.VisibilityMap{}, nil, err
		}
	}
	if foreign {
		buffer := n.Kind == functions.VisibilityPageRelation
		if n.Kind.Block() {
			buffer = block < result.MappedBlocks || n.Kind.Page() && block < result.Blocks
		} else {
			buffer = buffer || result.Blocks != 0 && result.MappedBlocks != 0
		}
		if buffer {
			return storage.VisibilityMap{}, nil, sqlError("0A000", "cannot access temporary tables of other sessions")
		}
	}
	// Final readmission uses the current exact owner cohort. Neither payload
	// allocation/codec work nor Manager calls occur inside this window.
	// Identity, authority, physical roots and tuple facts fence the result.
	// The READ horizon was exact when the facts were sampled; peer statements
	// advertising or retiring snapshots since then only withhold publication.
	owners, err = h.LockHeapStatisticsCohort()
	if err != nil {
		return storage.VisibilityMap{}, nil, err
	}
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil {
		err = pgvisibilityAuthority(auth, n)
	}
	valid := err == nil && h.CatalogEpochLocked() == epoch && auth.Access().Catalog.Get(target.Address()) == target && h.PrivateStorageLocked(target.Address()) == store
	if valid && store != nil {
		valid = storage.SameVersionRoot(snapshot.Versions, store.Versions) && snapshot.Ends == store.Ends && snapshot.Retained == store.Retained && snapshot.Discarded == store.Discarded && snapshot.Headers == store.Headers && snapshot.Generation == store.Generation && snapshot.Maintenance.FrozenXID == store.Maintenance.FrozenXID
		if valid {
			latest := h.HeapStatisticsFactsLocked(owners, store, target.Address(), writes, deletes)
			latest = storage.PrepareVisibilityFacts(latest, store.Headers)
			valid = storage.SameVisibilityFacts(facts, latest) && (!n.Kind.Check() || h.PgvisibilityCheckHorizonLocked() == checkHorizon)
			if h.PgvisibilityHorizonLocked(owners) != horizon {
				publish = nil
			}
		}
	}
	if valid {
		if current := h.PgvisibilityMapLocked(target, store); current != root {
			// Truncation or removal of the modeled map fences the result. A
			// sound root installed meanwhile by a peer capture only supersedes
			// this capture's publication; its facts are unchanged.
			valid = current != nil && !current.Invalidated
			publish = nil
		}
	}
	if err == nil {
		err = h.Operation().Context().Err()
	}
	if err == nil && !valid {
		err = ids.ErrCatalogReplaced
	}
	if err == nil && publish != nil {
		h.SetPgvisibilityMapLocked(target, store, publish)
	}
	h.UnlockHeapStatisticsCohortLocked(owners)
	if err != nil {
		return storage.VisibilityMap{}, nil, err
	}
	return result, findings, nil
}
