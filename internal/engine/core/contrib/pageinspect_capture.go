package contrib

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Pageinspect captures only immutable catalog descriptors. No physical state or
// table rows are consulted. Engine.Mu protects admission, never byte decoding.
func Pageinspect[A Authority](h Host[A], n plan.InstalledPageinspectExpr, oid uint32) ([]sqltype.BrinStoredColumn, error) {
	return capturePageinspect(h, n, oid, nil)
}

// PageinspectHeap captures the physical heap attribute facts of relation oid.
func PageinspectHeap[A Authority](h Host[A], n plan.InstalledPageinspectExpr, oid uint32) ([]sqltype.HeapStoredColumn, error) {
	var columns []sqltype.HeapStoredColumn
	_, err := capturePageinspect(h, n, oid, &columns)
	return columns, err
}

func capturePageinspect[A Authority](h Host[A], n plan.InstalledPageinspectExpr, oid uint32, heap *[]sqltype.HeapStoredColumn) ([]sqltype.BrinStoredColumn, error) {
	if err := h.Active(); err != nil {
		return nil, err
	}
	h.ReacquireEngine()
	auth, err := h.RoutineAuthorizationLocked()
	if err == nil {
		err = pageinspectAuthority(auth.Access(), n)
	}
	var target *catalog.Relation
	var ref ids.ObjectRef
	if err == nil && (n.Member == "brin_page_items" || heap != nil) {
		ref = h.CatalogDomain().Ref(auth.Access().Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)})
		target, _ = auth.Access().Catalog.Relation(ref.Address)
		if target == nil {
			err = sqlError("XX000", fmt.Sprintf("could not open relation with OID %d", oid))
		}
	}
	h.ReleaseEngineLocked()
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, nil
	}
	var contribution lock.RelationContribution
	defer h.LockManager().ReleaseRelation(&contribution)
	if err := h.AcquireRelationContribution(ref, lock.AccessShare, &contribution); err != nil {
		return nil, err
	}
	h.ReacquireOwnerEngine()
	defer h.ReleaseOwnerEngineLocked()
	auth, err = h.RoutineAuthorizationLocked()
	if err != nil {
		return nil, err
	}
	if err = pageinspectAuthority(auth.Access(), n); err != nil {
		return nil, err
	}
	view := auth.Access().Catalog
	if ref.Epoch != view.Epoch() || view.Get(ref.Address) != target {
		return nil, ids.ErrCatalogReplaced
	}
	h.MarkTemporaryTargetLocked(ref)
	if heap != nil {
		*heap, err = pageinspectHeapColumns(target)
		return nil, err
	}
	if target.RelationKind != catalog.IndexRelation || target.SQLIndex == nil {
		return nil, sqlError("42809", fmt.Sprintf("\"%s\" is not an index", target.Name()))
	}
	if target.SQLIndex.Method != catalog.BrinMethod {
		return nil, sqlError("42809", fmt.Sprintf("\"%s\" is not a BRIN index", target.Name()))
	}
	parent, ok := view.Relation(target.SQLIndex.Parent)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	bindings, err := index.BindBrinIndex(view, parent, target.SQLIndex)
	if err != nil {
		return nil, err
	}
	columns := make([]sqltype.BrinStoredColumn, len(bindings))
	for i, b := range bindings {
		storage, ok := view.LookupType(sqltype.ID(b.Class.StorageOID()))
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing BRIN stored type")
		}
		columns[i], err = sqltype.BindBrinStoredColumn(b.Usage, b.Class, storage)
		if err != nil {
			return nil, err
		}
	}
	return columns, nil
}
func pageinspectAuthority(auth access.View, n plan.InstalledPageinspectExpr) error {
	current, ok := auth.Catalog.Procedure(n.Ref.Address)
	if !ok || n.RoutineReference != n.Procedure || !exec.SameRoutineDeclaration(current, n.Procedure) {
		return ids.ErrCatalogReplaced
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(auth.Catalog, n.Procedure); err != nil {
		return err
	}
	if err := extension.ValidatePageinspectIdentity(auth.Catalog, current, n.Ref); err != nil {
		return err
	}
	symbol, err := extension.ProcedureSymbol(auth.Catalog, current)
	if err != nil {
		return err
	}
	if symbol != n.Member {
		return fmt.Errorf("stuntgres: invalid pageinspect call member")
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return sqlError("42501", "must be superuser to use raw page functions")
	}
	return nil
}
