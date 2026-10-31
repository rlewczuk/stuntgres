package core

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Only the supplied index is locked. Parent storage is borrowed coherently
// under Owner.Mu then Engine.Mu, without owner switching or recipe callbacks.
func (h *statementHost) Pgstatindex(n plan.InstalledPgstatindexExpr, input sqltype.Value) (integers [8]int64, floats [2]float64, err error) {
	s, e := h.scope(), h.scope().Engine()
	if err = s.Active(); err != nil {
		return
	}
	e.Mu.Lock()
	auth, failure := s.RoutineAuthorizationLocked()
	err = failure
	if err == nil {
		err = pgstatindexAuthority(auth, n)
	}
	var path []ids.ObjectAddress
	if err == nil && n.Input == sqltype.Text {
		path, err = ReferencePath(auth)
	}
	e.Mu.Unlock()
	if err != nil {
		return
	}
	var oid uint32
	if n.Input == sqltype.Text {
		var text string
		text, err = input.Text()
		if err == nil {
			oid, err = regobj.NewClassInput(inputCapture(auth, path)).ResolveRelationName(s.Op.Context(), text)
		}
	} else {
		oid, err = input.ClassOID()
	}
	if err != nil {
		return
	}
	resolvedEpoch := auth.Catalog.Epoch()
	e.Mu.Lock()
	auth, err = s.RoutineAuthorizationLocked()
	if err == nil && auth.Catalog.Epoch() != resolvedEpoch {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = pgstatindexAuthority(auth, n)
	}
	var target, parent *catalog.Relation
	var store *storage.Relation
	if err == nil {
		target, parent, err = amcheckTargetIdentity(auth, oid)
	}
	if err == nil && parent != nil {
		store = e.PrivateStorageLocked(s.PendingCatalog(), parent.Address())
	}
	e.Mu.Unlock()
	if err != nil {
		return
	}
	ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), target.Address())
	var contribution lock.RelationContribution
	defer e.Locks.ReleaseRelation(&contribution)
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	request, failure := e.Locks.StartAcquireRelation(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: ref}, lock.AccessShare, policy, &contribution)
	err = failure
	if request != nil {
		s.admissionWaited = true
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return
	}
	s.Owner.Mu.Lock()
	implicit := s.Owner.State == session.TxImplicit
	s.Owner.Mu.Unlock()
	if implicit && !s.ReplacementRead {
		e.ReplacementMu.RLock()
		s.ReplacementRead = true
	}
	s.Owner.Mu.Lock()
	e.Mu.Lock()
	auth, err = s.RoutineAuthorizationLocked()
	if err == nil && auth.Catalog.Epoch() != resolvedEpoch {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = pgstatindexAuthority(auth, n)
	}
	if err == nil && (ref.Domain() != e.Catalog.Domain || ref.Epoch != auth.Catalog.Epoch() || auth.Catalog.Get(target.Address()) != target) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil && parent != nil && (auth.Catalog.Get(parent.Address()) != parent || e.PrivateStorageLocked(s.PendingCatalog(), parent.Address()) != store) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		e.markTemporaryAccessLocked(s, ref, temporaryTargetAttempt)
		err = pgstatindexTargetCheckable(auth.Catalog, target, s.Op.Session.SessionToken)
	}
	var capture index.PgstatindexCapture
	if err == nil && store == nil {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		root := index.EffectiveRoot(e.Storage.IndexRoots, s.PendingCatalog(), target.Address())
		if s.PendingCatalog() != nil {
			if candidate, ok := index.JournalIndexesOrZero(s.PendingCatalog()).Candidates[target.Address()]; ok && (!candidate.MatchesIndex(target, store) || candidate.Root != root) {
				err = ids.ErrCatalogReplaced
			}
		}
		writes, deletes := s.ColumnProjectionRoots(parent.Address())
		capture = index.PgstatindexCapture{Index: target, Parent: parent, Root: root, Store: *store, Writes: writes, Deletes: deletes, Frontier: e.Visibility.CommitSeq()}
		capture.Store.IndexSource = store.IndexIdentity()
	}
	e.Mu.Unlock()
	s.Owner.Mu.Unlock()
	if err != nil {
		return
	}
	var stats index.PgstatindexStats
	stats, err = index.CapturePgstatindex(s.Op.Context(), capture)
	if err != nil {
		return
	}
	e.Mu.Lock()
	valid := e.Catalog.State.Epoch() == ref.Epoch && e.PrivateCatalogLocked(s.PendingCatalog()).Get(target.Address()) == target && e.PrivateCatalogLocked(s.PendingCatalog()).Get(parent.Address()) == parent && e.PrivateStorageLocked(s.PendingCatalog(), parent.Address()) == store
	e.Mu.Unlock()
	if !valid {
		err = ids.ErrCatalogReplaced
		return
	}
	if err = s.Op.Context().Err(); err != nil {
		return
	}
	integers = [8]int64{stats.Version, stats.Level, stats.Size, stats.Root, stats.Internal, stats.Leaves, stats.Empty, stats.Deleted}
	floats = [2]float64{stats.Density, stats.Fragmentation}
	return
}
func pgstatindexTargetCheckable(v catalog.Read, target *catalog.Relation, token ids.SessionToken) error {
	parentPartitioned := false
	if target.SQLIndex != nil {
		if parent, ok := v.Relation(target.SQLIndex.Parent); ok {
			parentPartitioned = parent.Partition != nil
		}
	}
	if target.RelationKind != catalog.IndexRelation || target.SQLIndex == nil || target.SQLIndex.MethodIdentity() != catalog.BtreeMethod.OID() || parentPartitioned {
		return SQLError("42809", fmt.Sprintf("relation \"%s\" is not a btree index", target.Name()))
	}
	if ns, ok := v.Namespace(target.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != token {
		return SQLError("0A000", "cannot access temporary tables of other sessions")
	}
	if !target.SQLIndex.Valid {
		return SQLError("55000", fmt.Sprintf("index \"%s\" is not valid", target.Name()))
	}
	return nil
}
func pgstatindexAuthority(auth AuthorizationView, n plan.InstalledPgstatindexExpr) error {
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != auth.Catalog.Owner().Domain || n.Ref.Epoch != auth.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := auth.Catalog.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return ids.ErrCatalogReplaced
	}
	if err := extension.ValidatePgstatindexIdentity(auth.Catalog, current, n.Ref); err != nil {
		return err
	}
	boundary := n.Pgstatindex
	boundary.RoutineReference = current
	if err := extension.ValidatePgstatindexCall(auth.Catalog, current, boundary); err != nil {
		return err
	}
	if err := auth.Check(access.Requirement{Object: auth.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil {
		return err
	}
	return auth.Check(access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege})
}
