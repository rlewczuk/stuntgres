package engine

import (
	"fmt"
	"stuntgres/internal/parser"
)

// Reuse the existing rename publication boundary. The table's owner-authorized
// AX grant protects its attachment; neither tuple providers nor storage run here.
func (e *Engine) renameExclusionLocked(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTableRenameConstraint, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, true)
	if raw.IfExists && renameMissing(err) {
		return e.renameRelationLocked(scope, op, auth, raw.Name, "", raw.NewName.Text, true, false, count)
	}
	if err != nil {
		return fail(err)
	}
	if ref != scope.lockedRef {
		return fail(ErrCatalogReplaced)
	}
	if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	parent := scope.lockedDescriptor.(*relationDescriptor)
	if err := tableKind(parent, "rename"); err != nil {
		return fail(err)
	}
	var definition *exclusionConstraint
	for i := range parent.exclusionConstraints {
		if parent.exclusionConstraints[i].Name == raw.Constraint.Text {
			definition = &parent.exclusionConstraints[i]
			break
		}
	}
	if definition == nil {
		for _, c := range parent.uniqueConstraints {
			if c.Name == raw.Constraint.Text {
				return fail(expressionError("0A000", "RENAME CONSTRAINT is unsupported", raw.ConstraintPos))
			}
		}
		for _, c := range parent.notNullConstraints {
			if c.Name == raw.Constraint.Text {
				return fail(expressionError("0A000", "RENAME CONSTRAINT is unsupported", raw.ConstraintPos))
			}
		}
		for _, c := range parent.checkConstraints {
			if c.Name == raw.Constraint.Text {
				return fail(expressionError("0A000", "RENAME CONSTRAINT is unsupported", raw.ConstraintPos))
			}
		}
		return fail(sqlError("42704", fmt.Sprintf(`constraint "%s" for table "%s" does not exist`, raw.Constraint.Text, parent.name)))
	}
	index, ok := auth.catalog.state.descriptors[definition.Index].(*relationDescriptor)
	if !ok {
		return fail(ErrCatalogReplaced)
	}
	if _, exists := auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: parent.namespace, Name: raw.NewName.Text}]; exists {
		return fail(sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, raw.NewName.Text)))
	}
	if err := auth.CheckCreate(ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: parent.namespace}); err != nil {
		return fail(err)
	}
	if index.revision == DefinitionRevision(^uint64(0)) {
		return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
	}
	next := *index
	next.name = raw.NewName.Text
	next.revision++
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
	err = stageBackingIndexRename(stage, &next)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		return Result{}, err
	}
	if err := stage.Replace(&next); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	if _, err = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
		return Result{}, err
	}
	scope.completed = true
	return Result{}, nil
}
