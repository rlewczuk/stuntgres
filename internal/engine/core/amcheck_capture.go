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
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

// Capture identities only. index_open's kind check and index_checkable's
// method/TEMP/validity checks belong after their respective relation locks.
func amcheckTargetIdentity(auth AuthorizationView, oid uint32) (*catalog.Relation, *catalog.Relation, error) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
	d, ok := auth.Catalog.Relation(address)
	if !ok {
		return nil, nil, SQLError("XX000", fmt.Sprintf("could not open relation with OID %d", oid))
	}
	if d.RelationKind != catalog.IndexRelation || d.SQLIndex == nil {
		return d, nil, nil
	}
	parent, ok := auth.Catalog.Relation(d.SQLIndex.Parent)
	if !ok {
		return nil, nil, ids.ErrCatalogReplaced
	}
	return d, parent, nil
}

func amcheckTargetCheckable(auth AuthorizationView, d, parent *catalog.Relation, token ids.SessionToken) error {
	// A non-index has no heap identity: PostgreSQL locks the target itself
	// before index_open rejects its kind.
	if d.RelationKind != catalog.IndexRelation || d.SQLIndex == nil {
		return SQLError("42809", fmt.Sprintf("\"%s\" is not an index", d.Name()))
	}
	if d.SQLIndex.MethodIdentity() != catalog.BtreeMethod.OID() || parent.Partition != nil {
		am, ok := auth.Catalog.AccessMethod(d.SQLIndex.MethodIdentity())
		if !ok {
			return ids.ErrCatalogReplaced
		}
		method := am.Name()
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "expected \"btree\" index as targets for verification", Detail: fmt.Sprintf("Relation \"%s\" is a %s index.", d.Name(), method)}
	}
	if ns, ok := auth.Catalog.Namespace(parent.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != token {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot access temporary tables of other sessions", Detail: fmt.Sprintf("Index \"%s\" is associated with temporary relation.", d.Name())}
	}
	if !d.SQLIndex.Valid {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf("cannot check index \"%s\"", d.Name()), Detail: "Index is not valid."}
	}
	return nil
}

func (h *statementHost) Amcheck(n plan.InstalledAmcheckExpr, oid uint32, flags [3]bool) (err error) {
	s, e := h.scope(), h.scope().Engine()
	if err := s.Active(); err != nil {
		return err
	}
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	if err == nil {
		err = amcheckProcedure(auth, n)
	}
	var target, parent *catalog.Relation
	var stored *storage.Relation
	if err == nil {
		target, parent, err = amcheckTargetIdentity(auth, oid)
	}
	if err == nil && parent != nil {
		stored = e.PrivateStorageLocked(s.PendingCatalog(), parent.Address())
	}
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	if parent != nil && stored == nil {
		return ids.ErrCatalogReplaced
	}
	indexRef := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), target.Address())
	var heapRef ids.ObjectRef
	if parent != nil {
		heapRef = e.Catalog.Domain.Ref(auth.Catalog.Epoch(), parent.Address())
	}
	mode := lock.AccessShare
	if n.Parent {
		mode = lock.Share
	}
	acquire := func(ref ids.ObjectRef, contribution *lock.RelationContribution) error {
		resource := lock.Resource{ObjectRef: ref}
		policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
		request, err := e.Locks.StartAcquireRelation(s.Op.Context(), s.LockTicket, resource, mode, policy, contribution)
		if request != nil {
			s.admissionWaited = true
			if s.ReplacementRead {
				e.ReplacementMu.RUnlock()
				s.ReplacementRead = false
			}
			err = request.Wait()
		}
		if err != nil {
			return err
		}
		s.Owner.Mu.Lock()
		implicit := s.Owner.State == session.TxImplicit
		s.Owner.Mu.Unlock()
		if implicit && !s.ReplacementRead {
			e.ReplacementMu.RLock()
			s.ReplacementRead = true
		}
		// Opening either relation follows lock admission and exact current
		// authority, but precedes kind/method/TEMP checkability. Record the
		// sticky transaction footprint here, not physical buffer initialization:
		// a later target wait or rejection cannot undo an opened TEMP heap.
		s.Owner.Mu.Lock()
		e.Mu.Lock()
		err = s.amcheckReadmitLocked(e, n, indexRef, target, parent, stored, false)
		if err == nil {
			err = s.Op.Context().Err()
		}
		if err == nil {
			e.markTemporaryAccessLocked(s, ref, temporaryTargetAttempt)
		}
		e.Mu.Unlock()
		s.Owner.Mu.Unlock()
		return err
	}
	var heapContribution, indexContribution lock.RelationContribution
	defer e.Locks.ReleaseRelation(&heapContribution)
	if parent != nil {
		if err := acquire(heapRef, &heapContribution); err != nil {
			return err
		}
	}
	defer e.Locks.ReleaseRelation(&indexContribution)
	if err := acquire(indexRef, &indexContribution); err != nil {
		return err
	}
	s.Owner.Mu.Lock()
	e.Mu.Lock()
	err = s.amcheckReadmitLocked(e, n, indexRef, target, parent, stored, true)
	var capture index.AmcheckCapture
	if err == nil {
		root := index.EffectiveRoot(e.Storage.IndexRoots, s.PendingCatalog(), target.Address())
		if s.PendingCatalog() != nil {
			if candidate, ok := index.JournalIndexesOrZero(s.PendingCatalog()).Candidates[target.Address()]; ok && (!candidate.MatchesIndex(target, stored) || candidate.Root != root) {
				err = ids.ErrCatalogReplaced
			}
		}
		writes, deletes := s.ColumnProjectionRoots(parent.Address())
		capture = index.AmcheckCapture{Index: target, Parent: parent, IndexRef: indexRef, ParentRef: heapRef, Root: root, Store: *stored, Frontier: e.Visibility.CommitSeq(), Writes: writes, Deletes: deletes}
		capture.Store.IndexSource = stored.IndexIdentity()
	}
	e.Mu.Unlock()
	s.Owner.Mu.Unlock()
	if err != nil {
		return err
	}
	options := index.AmcheckOptions{Parent: n.Parent, HeapAllIndexed: flags[0]}
	if n.Parent {
		options.RootDescend, options.CheckUnique = flags[1], flags[2]
	} else {
		options.CheckUnique = flags[1]
	}
	if err := s.WithAmcheckOwner(parent, func(evaluation *exec.Context) error { return index.CheckBtree(evaluation, capture, options) }); err != nil {
		return err
	}
	e.Mu.Lock()
	valid := e.Catalog.State.Epoch() == indexRef.Epoch && e.PrivateCatalogLocked(s.PendingCatalog()).Get(target.Address()) == target && e.PrivateCatalogLocked(s.PendingCatalog()).Get(parent.Address()) == parent && e.PrivateStorageLocked(s.PendingCatalog(), parent.Address()) == stored
	e.Mu.Unlock()
	if !valid {
		return ids.ErrCatalogReplaced
	}
	return s.Op.Context().Err()
}

// Engine.Mu held, with no manager calls or recipe execution in this window.
func (s *StatementScope) amcheckReadmitLocked(e *Engine, n plan.InstalledAmcheckExpr, ref ids.ObjectRef, target, parent *catalog.Relation, store *storage.Relation, checkable bool) error {
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != auth.Catalog.Epoch() || auth.Catalog.Get(target.Address()) != target {
		return ids.ErrCatalogReplaced
	}
	if parent != nil && (auth.Catalog.Get(parent.Address()) != parent || target.SQLIndex.Parent != parent.Address() || e.PrivateStorageLocked(s.PendingCatalog(), parent.Address()) != store) {
		return ids.ErrCatalogReplaced
	}
	if err := amcheckProcedure(auth, n); err != nil {
		return err
	}
	if checkable {
		return amcheckTargetCheckable(auth, target, parent, s.Op.Session.SessionToken)
	}
	return nil
}

func amcheckProcedure(auth AuthorizationView, n plan.InstalledAmcheckExpr) error {
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != auth.Catalog.Owner().Domain || n.Ref.Epoch != auth.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := auth.Catalog.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return ids.ErrCatalogReplaced
	}
	if err := extension.ValidateAmcheckIdentity(auth.Catalog, current, n.Ref); err != nil {
		return err
	}
	boundary := n.Amcheck
	boundary.RoutineReference = current
	if err := extension.ValidateAmcheckCall(auth.Catalog, current, boundary); err != nil {
		return err
	}
	return auth.Check(access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege})
}
