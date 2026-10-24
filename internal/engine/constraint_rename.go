package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// The owner-authorized AX grant pins the private parent incarnation. Rename is
// metadata-only: the existing overlay and Finish own publication of the group.
// Engine.mu is held on entry and released on every return.
func (e *Engine) renameConstraintLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, raw *parser.AlterTableRenameConstraint, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, true)
	if raw.IfExists && table.RenameMissing(err) {
		return e.renameRelationLocked(scope, op, auth, raw.Name, "", raw.NewName.Text, true, false, false, false, count)
	}
	if err != nil {
		return fail(err)
	}
	if ref != scope.lockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	parent := scope.lockedDescriptor.(*catalog.Relation)
	kind, backing, err := table.RenameConstraintKind(parent, raw.Constraint.Text)
	if err != nil {
		return fail(err)
	}
	if (kind == 'n' || kind == 'c') && catalog.InheritanceParticipant(auth.Catalog, parent) {
		return e.renameInheritanceConstraintLocked(scope, op, auth, raw, parent, kind)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	err = table.StageRenameConstraint(stage, auth.Catalog, parent, raw, kind, backing)
	if err != nil {
		e.DiscardCatalogStage(stage)
		return fail(err)
	}
	changes := scope.owner.Catalog.Clone()
	grantCatalog := e.catalog.State
	scope.catalogAuthority = auth.catalogAuthorityLocked(ref, grantCatalog.Get(ref.Address))
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Epoch != e.catalog.State.Epoch() {
		return Result{}, ids.ErrCatalogReplaced
	}
	// Check all originals before transferring any member. Earlier overlay originals
	// remain intact across repeated renames and savepoint rollback.
	for a := range stage.Descriptors() {
		if e.catalog.State.Get(a) != grantCatalog.Get(a) {
			return Result{}, catalog.Conflict{}
		}
	}
	for k := range stage.Touched() {
		if e.catalog.State.NameAddress(k) != grantCatalog.NameAddress(k) {
			return Result{}, catalog.Conflict{}
		}
	}
	for a, d := range stage.Descriptors() {
		e.catalogChangeLocked(changes, a, d)
	}
	scope.catalog = changes
	return Result{}, nil
}
