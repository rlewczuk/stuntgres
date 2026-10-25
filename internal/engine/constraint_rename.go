package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// The owner-authorized AX grant pins the private parent incarnation. Rename is
// metadata-only: the existing overlay and Finish own publication of the group.
// Engine.mu is held on entry and released on every return.
func RenameConstraintLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, raw *parser.AlterTableRenameConstraint, count int) (core.Result, error) {
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, true)
	if raw.IfExists && table.RenameMissing(err) {
		return RenameRelationLocked(e, scope, op, auth, raw.Name, "", raw.NewName.Text, true, false, false, false, count)
	}
	if err != nil {
		return fail(err)
	}
	if ref != scope.LockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	parent := scope.LockedDescriptor.(*catalog.Relation)
	kind, backing, err := table.RenameConstraintKind(parent, raw.Constraint.Text)
	if err != nil {
		return fail(err)
	}
	if (kind == 'n' || kind == 'c') && catalog.InheritanceParticipant(auth.Catalog, parent) {
		return renameInheritanceConstraintLocked(e, scope, op, auth, raw, parent, kind)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	err = table.StageRenameConstraint(stage, auth.Catalog, parent, raw, kind, backing)
	if err != nil {
		e.DiscardCatalogStage(stage)
		return fail(err)
	}
	changes := scope.Owner.Catalog.Clone()
	grantCatalog := e.Catalog.State
	scope.CatalogAuthority = auth.CatalogAuthorityLocked(ref, grantCatalog.Get(ref.Address))
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if ref.Epoch != e.Catalog.State.Epoch() {
		return core.Result{}, ids.ErrCatalogReplaced
	}
	// Check all originals before transferring any member. Earlier overlay originals
	// remain intact across repeated renames and savepoint rollback.
	for a := range stage.Descriptors() {
		if e.Catalog.State.Get(a) != grantCatalog.Get(a) {
			return core.Result{}, catalog.Conflict{}
		}
	}
	for k := range stage.Touched() {
		if e.Catalog.State.NameAddress(k) != grantCatalog.NameAddress(k) {
			return core.Result{}, catalog.Conflict{}
		}
	}
	for a, d := range stage.Descriptors() {
		e.CatalogChangeLocked(changes, a, d)
	}
	scope.Catalog = changes
	return core.Result{}, nil
}
