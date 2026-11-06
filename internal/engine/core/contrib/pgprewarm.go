package contrib

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
)

func pgPrewarmAuthority[A Authority](auth A, n plan.InstalledPgPrewarmExpr) error {
	view := auth.Access()
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != view.Catalog.Owner().Domain || n.Ref.Epoch != view.Catalog.Epoch() { return ids.ErrCatalogReplaced }
	current, ok := view.Catalog.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) { return ids.ErrCatalogReplaced }
	if err := extension.ValidatePgPrewarmIdentity(view.Catalog, current, n.Ref); err != nil { return err }
	call := n.PgPrewarm
	call.RoutineReference = current
	if err := extension.ValidatePgPrewarmCall(view.Catalog, current, call); err != nil { return err }
	return auth.Check(access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege, RoutineACLOnly: true})
}

func pgPrewarmSelect[A Authority](auth A, target, parent *catalog.Relation) error {
	selected := target
	if parent != nil { selected = parent }
	err := auth.Check(access.Requirement{Object: auth.Access().Ref(selected.Address()), Privileges: authorization.SelectPrivilege})
	if parent != nil && err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" && diagnostic.Message == "permission denied for table "+parent.Name() {
			copy := *diagnostic
			copy.Message = "permission denied for index "+target.Name()
			return &copy
		}
	}
	return err
}

// PgPrewarm captures scalar storage authority under owner -> Engine locks,
// computes logical geometry unlocked, then readmits each real block. No range
// sized allocation, retained cache lease, alternate store or arithmetic-only
// success path is used; ineligible private/TEMP reads still process their range.
func PgPrewarm[A Authority](h Host[A], n plan.InstalledPgPrewarmExpr, oid uint32, request storage.PrewarmRequest) (int64, error) {
	if err := h.Active(); err != nil { return 0, err }
	h.ReacquireEngine()
	auth, err := h.RoutineAuthorizationLocked()
	if err == nil { err = pgPrewarmAuthority(auth, n) }
	var target, parent *catalog.Relation
	if err == nil { target, parent, err = amcheckTargetIdentity(auth.Access().Catalog, oid) }
	epoch := h.CatalogEpochLocked()
	h.ReleaseEngineLocked()
	if err != nil { return 0, err }
	ref := h.CatalogDomain().Ref(epoch, target.Address())
	var tableLock, targetLock lock.RelationContribution
	defer h.LockManager().ReleaseRelation(&tableLock)
	defer h.LockManager().ReleaseRelation(&targetLock)
	if parent != nil {
		if err := h.AcquireRelationContribution(h.CatalogDomain().Ref(epoch, parent.Address()), lock.AccessShare, &tableLock); err != nil { return 0, err }
	}
	if err := h.AcquireRelationContribution(ref, lock.AccessShare, &targetLock); err != nil { return 0, err }
	h.ReacquireOwnerEngine()
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil { err = pgPrewarmAuthority(auth, n) }
	if err == nil && (h.CatalogEpochLocked() != epoch || auth.Access().Catalog.Get(target.Address()) != target || parent != nil && auth.Access().Catalog.Get(parent.Address()) != parent) { err = ids.ErrCatalogReplaced }
	if err == nil { err = pgPrewarmSelect(auth, target, parent) }
	if err == nil { h.MarkTemporaryTargetLocked(ref); err = PgFreespaceTargetCheckable(auth.Access().Catalog, target) }
	address := target.Address()
	if parent != nil { address = parent.Address() }
	store := h.PrivateStorageLocked(address)
	var generation uint64
	var roots []storage.RowVersion
	var ends *storage.DeletionRoot
	var headers *storage.HeapHeaderRoot
	var auxiliary storage.AuxiliaryForks
	if store != nil { generation, roots, ends, headers, auxiliary = store.Generation, store.Versions, store.Ends, store.Headers, store.Auxiliary }
	var generator *storage.SequenceGenerator
	var allocation storage.Allocation
	var view *visibility.View
	local := false
	if err == nil {
		switch {
		case target.Sequence != nil:
			generator = h.SequenceGeneratorLocked(target.Address())
			if generator == nil { err = ids.ErrCatalogReplaced }
		case target.SQLIndex != nil:
			allocation = h.RelationAllocationLocked(auth.Access().Catalog, target)
			if !allocation.Allocated || allocation.Index == nil || allocation.Parent != parent || store == nil { err = ids.ErrCatalogReplaced }
		case target.RelationKind == catalog.CatalogRelation:
		default:
			if store == nil { err = ids.ErrCatalogReplaced } else if request.Fork == 0 { view, local, err = h.RelationDataViewLocked(ref) }
		}
	}
	h.ReleaseOwnerEngineLocked()
	if local && view != nil { defer view.Release() }
	if err != nil { return 0, err }
	ctx := h.Operation().Context()
	if err := ctx.Err(); err != nil { return 0, err }
	var main int64
	if request.Fork == 0 {
		switch {
		case target.Sequence != nil:
			main = 1
		case target.SQLIndex != nil:
			forks, failure := allocation.Forks(ctx)
			err = failure
			if err == nil { main = forks.Main/8192; if forks.Main%8192 != 0 { main++ } }
		case target.RelationKind == catalog.CatalogRelation:
			var payload int64
			payload, err = h.PgvisibilityCatalogBytes(ref, target)
			if err == nil { var layout storage.FreespaceLayout; layout, err = storage.FreespacePayload(ctx, payload); main = layout.Blocks }
		default:
			source, failure := exec.CaptureSource(ctx, view, ref)
			err = failure
			if err == nil {
				var payload int64
				for i := range source.Count() {
					if err = ctx.Err(); err != nil { break }
					var weight int64
					weight, err = storage.RowAllocation(ctx, source.Row(i))
					if err != nil { break }
					weight, err = storage.AddSize(weight, 4)
					if err != nil { break }
					payload, err = storage.AddSize(payload, weight)
					if err != nil { break }
				}
				source.Close()
				if err == nil { var layout storage.FreespaceLayout; layout, err = storage.FreespacePayload(ctx, payload); main = layout.Blocks }
			}
		}
	}
	if err != nil { return 0, err }
	blocks, present := main, true
	switch request.Fork {
	case 0:
	case 1, 2:
		if target.SQLIndex != nil || target.Sequence != nil || target.RelationKind == catalog.CatalogRelation { present = false } else { blocks, present = auxiliary.Fork(request.Fork) }
	case 3:
		persistence := target.Persistence
		if target.Sequence != nil { persistence = target.Sequence.Persistence }
		if parent != nil { persistence = parent.Persistence }
		present, blocks = persistence == "unlogged", 0
		if target.SQLIndex != nil || target.Sequence != nil { blocks = 1 }
	default:
		return 0, fmt.Errorf("stuntgres: invalid typed prewarm fork")
	}
	if !present { return 0, sqlError("22023", fmt.Sprintf("fork %q does not exist for this relation", [...]string{"main", "fsm", "vm", "init"}[request.Fork])) }
	first, last, err := request.Bounds(blocks)
	if err != nil { return 0, err }
	var count int64
	// Also readmit empty/reversed calls. Stale capture or cancellation must not
	// become a fabricated successful zero merely because no block was admitted.
	for block := first; ; block++ {
		if err := ctx.Err(); err != nil { return 0, err }
		h.ReacquireOwnerEngine()
		auth, err = h.RoutineAuthorizationLocked()
		if err == nil { err = pgPrewarmAuthority(auth, n) }
		valid := err == nil && h.CatalogEpochLocked() == epoch && auth.Access().Catalog.Get(target.Address()) == target && h.PrivateStorageLocked(address) == store && (parent == nil || auth.Access().Catalog.Get(parent.Address()) == parent)
		if valid && store != nil { valid = store.Generation == generation && storage.SameVersionRoot(store.Versions, roots) && store.Ends == ends && store.Headers == headers && store.Auxiliary == auxiliary }
		if valid && generator != nil { valid = h.SequenceGeneratorLocked(target.Address()) == generator }
		if valid && target.SQLIndex != nil { current := h.RelationAllocationLocked(auth.Access().Catalog, target); valid = current.Index == allocation.Index }
		if err == nil && !valid { err = ids.ErrCatalogReplaced }
		if err == nil { err = pgPrewarmSelect(auth, target, parent) }
		if err == nil && block <= last { err = h.PrewarmBlockLocked(ref, request.Fork, block) }
		h.ReleaseOwnerEngineLocked()
		if err != nil { return 0, err }
		if block > last { return count, ctx.Err() }
		count++
		if block == last { return count, ctx.Err() }
	}
}
