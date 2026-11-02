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
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

// Capture identities only. index_open's kind check and index_checkable's
// method/TEMP/validity checks belong after their respective relation locks.
func amcheckTargetIdentity(view catalog.Read, oid uint32) (*catalog.Relation, *catalog.Relation, error) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
	d, ok := view.Relation(address)
	if !ok {
		return nil, nil, sqlError("XX000", fmt.Sprintf("could not open relation with OID %d", oid))
	}
	if d.RelationKind != catalog.IndexRelation || d.SQLIndex == nil {
		return d, nil, nil
	}
	parent, ok := view.Relation(d.SQLIndex.Parent)
	if !ok {
		return nil, nil, ids.ErrCatalogReplaced
	}
	return d, parent, nil
}

func amcheckTargetCheckable(view catalog.Read, d, parent *catalog.Relation, token ids.SessionToken, kind functions.AmcheckKind) error {
	// A non-index has no heap identity: PostgreSQL locks the target itself
	// before index_open rejects its kind.
	if d.RelationKind != catalog.IndexRelation || d.SQLIndex == nil {
		return sqlError("42809", fmt.Sprintf("\"%s\" is not an index", d.Name()))
	}
	expected := catalog.BtreeMethod
	expectedName := "btree"
	if kind == functions.AmcheckGIN {
		expected = catalog.GinMethod
		expectedName = "gin"
	}
	if d.SQLIndex.MethodIdentity() != expected.OID() || parent.Partition != nil {
		am, ok := view.AccessMethod(d.SQLIndex.MethodIdentity())
		if !ok {
			return ids.ErrCatalogReplaced
		}
		method := am.Name()
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf("expected \"%s\" index as targets for verification", expectedName), Detail: fmt.Sprintf("Relation \"%s\" is a %s index.", d.Name(), method)}
	}
	if ns, ok := view.Namespace(parent.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) && ns.Temporary != token {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot access temporary tables of other sessions", Detail: fmt.Sprintf("Index \"%s\" is associated with temporary relation.", d.Name())}
	}
	if !d.SQLIndex.Valid {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf("cannot check index \"%s\"", d.Name()), Detail: "Index is not valid."}
	}
	return nil
}

// Amcheck runs the selected installed index checker on the index oid.
func Amcheck[A Authority](h Host[A], n plan.InstalledAmcheckExpr, oid uint32, flags [3]bool) (err error) {
	if err := h.Active(); err != nil {
		return err
	}
	h.ReacquireEngine()
	auth, err := h.RoutineAuthorizationLocked()
	if err == nil {
		err = amcheckProcedure(auth, n)
	}
	var target, parent *catalog.Relation
	var stored *storage.Relation
	if err == nil {
		target, parent, err = amcheckTargetIdentity(auth.Access().Catalog, oid)
	}
	if err == nil && parent != nil {
		stored = h.PrivateStorageLocked(parent.Address())
	}
	h.ReleaseEngineLocked()
	if err != nil {
		return err
	}
	if parent != nil && stored == nil {
		return ids.ErrCatalogReplaced
	}
	indexRef := h.CatalogDomain().Ref(auth.Access().Catalog.Epoch(), target.Address())
	var heapRef ids.ObjectRef
	if parent != nil {
		heapRef = h.CatalogDomain().Ref(auth.Access().Catalog.Epoch(), parent.Address())
	}
	mode := lock.AccessShare
	if n.Kind == functions.AmcheckBtreeParent {
		mode = lock.Share
	}
	acquire := func(ref ids.ObjectRef, contribution *lock.RelationContribution) error {
		if err := h.AcquireRelationContribution(ref, mode, contribution); err != nil {
			return err
		}
		// Opening either relation follows lock admission and exact current
		// authority, but precedes kind/method/TEMP checkability. Record the
		// sticky transaction footprint here, not physical buffer initialization:
		// a later target wait or rejection cannot undo an opened TEMP heap.
		h.ReacquireOwnerEngine()
		err := amcheckReadmitLocked(h, n, indexRef, target, parent, stored, false)
		if err == nil {
			err = h.Operation().Context().Err()
		}
		if err == nil {
			h.MarkTemporaryTargetLocked(ref)
		}
		h.ReleaseOwnerEngineLocked()
		return err
	}
	var heapContribution, indexContribution lock.RelationContribution
	defer h.LockManager().ReleaseRelation(&heapContribution)
	if parent != nil {
		if err := acquire(heapRef, &heapContribution); err != nil {
			return err
		}
	}
	defer h.LockManager().ReleaseRelation(&indexContribution)
	if err := acquire(indexRef, &indexContribution); err != nil {
		return err
	}
	h.ReacquireOwnerEngine()
	err = amcheckReadmitLocked(h, n, indexRef, target, parent, stored, true)
	var capture index.AmcheckCapture
	if err == nil {
		root := index.EffectiveRoot(h.EngineStorageLocked().IndexRoots, h.PendingCatalog(), target.Address())
		if h.PendingCatalog() != nil {
			if candidate, ok := index.JournalIndexesOrZero(h.PendingCatalog()).Candidates[target.Address()]; ok && (!candidate.MatchesIndex(target, stored) || candidate.Root != root) {
				err = ids.ErrCatalogReplaced
			}
		}
		writes, deletes := h.ColumnProjectionRoots(parent.Address())
		capture = index.AmcheckCapture{Index: target, Parent: parent, IndexRef: indexRef, ParentRef: heapRef, Root: root, Store: *stored, Frontier: h.CommitSequenceLocked(), Writes: writes, Deletes: deletes}
		capture.Store.IndexSource = stored.IndexIdentity()
	}
	h.ReleaseOwnerEngineLocked()
	if err != nil {
		return err
	}
	if n.Kind == functions.AmcheckGIN {
		err = h.CheckGinAsOwner(parent, capture)
	} else {
		options := index.AmcheckOptions{Parent: n.Kind == functions.AmcheckBtreeParent, HeapAllIndexed: flags[0]}
		if options.Parent {
			options.RootDescend, options.CheckUnique = flags[1], flags[2]
		} else {
			options.CheckUnique = flags[1]
		}
		err = h.CheckBtreeAsOwner(parent, capture, options)
	}
	if err != nil {
		return err
	}
	h.ReacquireEngine()
	valid := h.CatalogEpochLocked() == indexRef.Epoch && h.PrivateCatalogLocked(h.PendingCatalog()).Get(target.Address()) == target && h.PrivateCatalogLocked(h.PendingCatalog()).Get(parent.Address()) == parent && h.PrivateStorageLocked(parent.Address()) == stored
	h.ReleaseEngineLocked()
	if !valid {
		return ids.ErrCatalogReplaced
	}
	return h.Operation().Context().Err()
}

// Engine.Mu held, with no manager calls or recipe execution in this window.
func amcheckReadmitLocked[A Authority](h Host[A], n plan.InstalledAmcheckExpr, ref ids.ObjectRef, target, parent *catalog.Relation, store *storage.Relation, checkable bool) error {
	auth, err := h.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	view := auth.Access().Catalog
	if ref.Domain() != h.CatalogDomain() || ref.Epoch != view.Epoch() || view.Get(target.Address()) != target {
		return ids.ErrCatalogReplaced
	}
	if parent != nil && (view.Get(parent.Address()) != parent || target.SQLIndex.Parent != parent.Address() || h.PrivateStorageLocked(parent.Address()) != store) {
		return ids.ErrCatalogReplaced
	}
	if err := amcheckProcedure(auth, n); err != nil {
		return err
	}
	if checkable {
		return amcheckTargetCheckable(view, target, parent, h.Operation().Session.SessionToken, n.Kind)
	}
	return nil
}

func amcheckProcedure[A Authority](auth A, n plan.InstalledAmcheckExpr) error {
	view := auth.Access().Catalog
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != view.Owner().Domain || n.Ref.Epoch != view.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := view.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return ids.ErrCatalogReplaced
	}
	if err := extension.ValidateAmcheckIdentity(view, current, n.Ref); err != nil {
		return err
	}
	boundary := n.Amcheck
	boundary.RoutineReference = current
	if err := extension.ValidateAmcheckCall(view, current, boundary); err != nil {
		return err
	}
	return auth.Check(access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege})
}
