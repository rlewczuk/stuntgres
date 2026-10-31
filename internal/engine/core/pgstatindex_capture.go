package core

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// PgstattupleStats is pgstatindex and pgstathashindex. Only the supplied
// index is locked. Parent storage is borrowed coherently under Owner.Mu then
// Engine.Mu, without owner switching or recipe callbacks.
func PgstattupleStats[A Authority](h Host[A], n plan.InstalledPgstattupleStatsExpr, input sqltype.Value) (integers [8]int64, floats [2]float64, err error) {
	if err = h.Active(); err != nil {
		return
	}
	h.ReacquireEngine()
	auth, failure := h.RoutineAuthorizationLocked()
	err = failure
	if err == nil {
		err = pgstattupleStatsAuthority(auth, n)
	}
	var path []ids.ObjectAddress
	if err == nil && n.Input == sqltype.Text {
		path, err = auth.ReferencePath()
	}
	h.ReleaseEngineLocked()
	if err != nil {
		return
	}
	var oid uint32
	if n.Input == sqltype.Text {
		var text string
		text, err = input.Text()
		if err == nil {
			oid, err = h.ClassInput(auth, path).ResolveRelationName(h.Operation().Context(), text)
		}
	} else {
		oid, err = input.ClassOID()
	}
	if err != nil {
		return
	}
	resolvedEpoch := auth.Access().Catalog.Epoch()
	h.ReacquireEngine()
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil && auth.Access().Catalog.Epoch() != resolvedEpoch {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = pgstattupleStatsAuthority(auth, n)
	}
	var target, parent *catalog.Relation
	var store *storage.Relation
	if err == nil {
		target, parent, err = amcheckTargetIdentity(auth.Access().Catalog, oid)
	}
	if err == nil && parent != nil {
		store = h.PrivateStorageLocked(parent.Address())
	}
	h.ReleaseEngineLocked()
	if err != nil {
		return
	}
	ref := h.CatalogDomain().Ref(auth.Access().Catalog.Epoch(), target.Address())
	var contribution lock.RelationContribution
	defer h.LockManager().ReleaseRelation(&contribution)
	if err = h.AcquireRelationContribution(ref, lock.AccessShare, &contribution); err != nil {
		return
	}
	h.ReacquireOwnerEngine()
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil && auth.Access().Catalog.Epoch() != resolvedEpoch {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = pgstattupleStatsAuthority(auth, n)
	}
	if err == nil && (ref.Domain() != h.CatalogDomain() || ref.Epoch != auth.Access().Catalog.Epoch() || auth.Access().Catalog.Get(target.Address()) != target) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil && parent != nil && (auth.Access().Catalog.Get(parent.Address()) != parent || h.PrivateStorageLocked(parent.Address()) != store) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		h.MarkTemporaryTargetLocked(ref)
		err = pgstattupleStatsTargetCheckable(auth.Access().Catalog, target, h.Operation().Session.SessionToken, n.Operation)
	}
	var capture index.PgstattupleStatsCapture
	if err == nil && store == nil {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		root := index.EffectiveRoot(h.EngineStorageLocked().IndexRoots, h.PendingCatalog(), target.Address())
		if h.PendingCatalog() != nil {
			if candidate, ok := index.JournalIndexesOrZero(h.PendingCatalog()).Candidates[target.Address()]; ok && (!candidate.MatchesIndex(target, store) || candidate.Root != root) {
				err = ids.ErrCatalogReplaced
			}
		}
		capture = index.PgstattupleStatsCapture{Index: target, Parent: parent, Root: root}
		if n.Operation != functions.HashStats {
			writes, deletes := h.ColumnProjectionRoots(parent.Address())
			capture.Store, capture.Writes, capture.Deletes, capture.Frontier = *store, writes, deletes, h.CommitSequenceLocked()
			capture.Store.IndexSource = store.IndexIdentity()
		}
	}
	h.ReleaseOwnerEngineLocked()
	if err != nil {
		return
	}
	var stats index.PgstattupleStatsResult
	stats, err = index.CapturePgstattupleStats(h.Operation().Context(), capture)
	if err != nil {
		return
	}
	h.ReacquireEngine()
	valid := h.CatalogEpochLocked() == ref.Epoch && h.PrivateCatalogLocked(h.PendingCatalog()).Get(target.Address()) == target && h.PrivateCatalogLocked(h.PendingCatalog()).Get(parent.Address()) == parent && h.PrivateStorageLocked(parent.Address()) == store
	h.ReleaseEngineLocked()
	if !valid {
		err = ids.ErrCatalogReplaced
		return
	}
	if err = h.Operation().Context().Err(); err != nil {
		return
	}
	if n.Operation == functions.HashStats {
		integers = [8]int64{stats.Version, stats.Buckets, stats.Overflow, stats.Bitmap, stats.Unused, stats.LiveItems, stats.DeadItems}
		floats[0] = stats.FreePercent
	} else {
		integers = [8]int64{stats.Version, stats.Level, stats.Size, stats.Root, stats.Internal, stats.Leaves, stats.Empty, stats.Deleted}
		floats = [2]float64{stats.Density, stats.Fragmentation}
	}
	return
}
func pgstattupleStatsTargetCheckable(v catalog.Read, target *catalog.Relation, token ids.SessionToken, operation functions.PgstattupleOperation) error {
	parentPartitioned := false
	if target.SQLIndex != nil {
		if parent, ok := v.Relation(target.SQLIndex.Parent); ok {
			parentPartitioned = parent.Partition != nil
		}
	}
	method, kind, temporary := catalog.BtreeMethod.OID(), "btree", "tables"
	if operation == functions.HashStats {
		method, kind, temporary = catalog.HashMethod.OID(), "hash", "indexes"
		parentPartitioned = false
	}
	if target.RelationKind != catalog.IndexRelation || target.SQLIndex == nil || target.SQLIndex.MethodIdentity() != method || parentPartitioned {
		return sqlError("42809", fmt.Sprintf("relation \"%s\" is not a %s index", target.Name(), kind))
	}
	if ns, ok := v.Namespace(target.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != token {
		return sqlError("0A000", "cannot access temporary "+temporary+" of other sessions")
	}
	if !target.SQLIndex.Valid {
		return sqlError("55000", fmt.Sprintf("index \"%s\" is not valid", target.Name()))
	}
	return nil
}
func pgstattupleStatsAuthority[A Authority](auth A, n plan.InstalledPgstattupleStatsExpr) error {
	view := auth.Access()
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != view.Catalog.Owner().Domain || n.Ref.Epoch != view.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := view.Catalog.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return ids.ErrCatalogReplaced
	}
	if err := extension.ValidatePgstattupleIdentity(view.Catalog, current, n.Ref); err != nil {
		return err
	}
	boundary := n.PgstattupleStats
	boundary.RoutineReference = current
	if err := extension.ValidatePgstattupleStatsCall(view.Catalog, current, boundary); err != nil {
		return err
	}
	if err := auth.Check(access.Requirement{Object: view.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil {
		return err
	}
	return auth.Check(access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege})
}
