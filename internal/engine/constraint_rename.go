package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
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
	if raw.IfExists && RenameMissing(err) {
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
	if parent.RelationKind == catalog.SequenceRelation {
		return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot rename columns of relation "%s"`, parent.Name()), Detail: "This operation is not supported for sequences."})
	}
	// PostgreSQL accepts view/index targets here, then performs constraint lookup.
	var backing ids.ObjectAddress
	kind := byte(0)
	for _, c := range parent.Constraints.NotNull {
		if c.Name == raw.Constraint.Text {
			kind = 'n'
			break
		}
	}
	for _, c := range parent.Constraints.Check {
		if c.Name == raw.Constraint.Text {
			kind = 'c'
			break
		}
	}
	for _, c := range parent.Constraints.Unique {
		if c.Name == raw.Constraint.Text {
			kind = 'u'
			backing = c.Index
			break
		}
	}
	for _, c := range parent.Constraints.Exclusion {
		if c.Name == raw.Constraint.Text {
			kind = 'x'
			backing = c.Index
			break
		}
	}
	for _, c := range parent.Constraints.ForeignKeys {
		if c.Name == raw.Constraint.Text {
			kind = 'f'
			break
		}
	}
	if kind == 0 {
		return fail(sqlError("42704", fmt.Sprintf(`constraint "%s" for table "%s" does not exist`, raw.Constraint.Text, parent.Name())))
	}
	if (kind == 'n' || kind == 'c') && catalog.InheritanceParticipant(auth.Catalog, parent) {
		return e.renameInheritanceConstraintLocked(scope, op, auth, raw, parent, kind)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	if backing != (ids.ObjectAddress{}) {
		idx, ok := auth.Catalog.Relation(backing)
		if !ok {
			e.DiscardCatalogStage(stage)
			return fail(ids.ErrCatalogReplaced)
		}
		if _, exists := auth.Catalog.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: raw.NewName.Text}); exists {
			e.DiscardCatalogStage(stage)
			return fail(sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, raw.NewName.Text)))
		}
		if idx.Revision() == ids.DefinitionRevision(^uint64(0)) {
			e.DiscardCatalogStage(stage)
			return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
		}
		next := *idx
		next.ObjectName = raw.NewName.Text
		next.ObjectRevision++
		err = index.StageBackingIndexRename(stage, &next)
		if err == nil {
			err = stage.Replace(&next)
		}
	} else {
		var next *catalog.Relation
		next, err = RenameLocalConstraint(parent, raw.Constraint.Text, raw.NewName.Text, kind)
		if err == nil {
			err = stage.Replace(next)
		}
	}
	if err != nil {
		e.DiscardCatalogStage(stage)
		return fail(err)
	}
	changes := scope.owner.Catalog.Clone()
	grantCatalog := e.catalog.state
	scope.catalogAuthority = auth.catalogAuthorityLocked(ref, grantCatalog.Get(ref.Address))
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Epoch != e.catalog.state.Epoch() {
		return Result{}, ids.ErrCatalogReplaced
	}
	// Check all originals before transferring any member. Earlier overlay originals
	// remain intact across repeated renames and savepoint rollback.
	for a := range stage.Descriptors() {
		if e.catalog.state.Get(a) != grantCatalog.Get(a) {
			return Result{}, catalog.Conflict{}
		}
	}
	for k := range stage.Touched() {
		if e.catalog.state.NameAddress(k) != grantCatalog.NameAddress(k) {
			return Result{}, catalog.Conflict{}
		}
	}
	for a, d := range stage.Descriptors() {
		e.catalogChangeLocked(changes, a, d)
	}
	scope.catalog = changes
	return Result{}, nil
}
