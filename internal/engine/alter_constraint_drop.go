package engine

import "stuntgres/internal/parser"

func droppedConstraintIndex(parent *relationDescriptor, action parser.AlterTableAction) ObjectAddress {
	if action.Action == parser.DropConstraint {
		for _, c := range parent.uniqueConstraints {
			if c.Name == action.Constraint.Text {
				return c.Index
			}
		}
		for _, c := range parent.exclusionConstraints {
			if c.Name == action.Constraint.Text {
				return c.Index
			}
		}
	}
	return ObjectAddress{}
}

// Retire original backing generations after logical DROP, before ADD naming.
// Remove retains the original reference/name observations and locks the engine;
// neither retirement nor dependency traversal publishes the private candidate.
// Each action classifies its own dependencies before any later backing removal.
func (e *Engine) stageDroppedConstraintIndexes(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, old, next *relationDescriptor, actions []parser.AlterTableAction, changes *catalogChanges) (*relationDescriptor, *CatalogStage, error) {
	for _, action := range actions {
		address := droppedConstraintIndex(old, action)
		if address == (ObjectAddress{}) {
			continue
		}
		if stage == nil {
			stage = e.BeginCatalogStage(auth.catalog)
			stage.authSession, stage.authRevision, stage.roleRevision = scope.op.session, auth.revision, auth.roles.state.revision
		}
		if err := stage.Replace(next); err != nil {
			return next, stage, err
		}
		if err := stage.Remove(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: address}); err != nil {
			return next, stage, err
		}
		if err := stageExpressionDependencies(scope.op.Context(), stage, []ObjectAddress{address}, action.Mode, scope, changes, nil, address); err != nil {
			return next, stage, err
		}
		// CASCADE can remove a self-reference from this same parent. Preserve
		// that dependency candidate through the later ALTER passes.
		next = stage.descriptors[old.address].(*relationDescriptor)
	}
	return next, stage, nil
}
