package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func droppedConstraint(parent *relationDescriptor, action parser.AlterTableAction) (ids.ObjectAddress, ids.ObjectAddress) {
	if action.Action == parser.DropConstraint {
		for _, c := range parent.UniqueConstraints {
			if c.Name == action.Constraint.Text {
				return ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, c.Index
			}
		}
		for _, c := range parent.ExclusionConstraints {
			if c.Name == action.Constraint.Text {
				return ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, c.Index
			}
		}
		for _, c := range parent.CheckConstraints {
			if c.Name == action.Constraint.Text {
				return ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, ids.ObjectAddress{}
			}
		}
		for _, c := range parent.NotNullConstraints {
			if c.Name == action.Constraint.Text {
				return ids.ObjectAddress{ClassID: 2606, ObjectID: c.OID}, ids.ObjectAddress{}
			}
		}
	}
	return ids.ObjectAddress{}, ids.ObjectAddress{}
}

// Retire named constraint identities and original backing generations after
// logical DROP, before ADD naming.
// Remove retains the original reference/name observations and locks the engine;
// neither retirement nor dependency traversal publishes the private candidate.
// Each action classifies its own dependencies before any later backing removal.
func (e *Engine) stageDroppedConstraintIndexes(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, old, next *relationDescriptor, actions []parser.AlterTableAction, changes *catalogChanges) (*relationDescriptor, *CatalogStage, error) {
	if !hasDroppedConstraintIndexes(old, actions) {
		return next, stage, nil
	}
	if stage == nil {
		stage = e.BeginCatalogStage(auth.catalog)
		stage.authSession, stage.authRevision, stage.roleRevision = scope.op.session, auth.revision, auth.roleRecord.revision
	}
	next, err := dropConstraintIndexes(stageDropProvider(scope.op.Context(), stage, scope, changes, nil), old, next, actions)
	return next, stage, err
}

func hasDroppedConstraintIndexes(old *relationDescriptor, actions []parser.AlterTableAction) bool {
	for _, action := range actions {
		if constraint, _ := droppedConstraint(old, action); constraint != (ids.ObjectAddress{}) {
			return true
		}
	}
	return false
}

// The same ordered provider retires named constraint identities, including
// constraints without backing indexes, during staging and pure preparation.
func dropConstraintIndexes(provider *dropProvider, old, next *relationDescriptor, actions []parser.AlterTableAction) (*relationDescriptor, error) {
	for _, action := range actions {
		constraint, address := droppedConstraint(old, action)
		if constraint == (ids.ObjectAddress{}) {
			continue
		}
		if address != (ids.ObjectAddress{}) && provider.removed(address) {
			continue // An earlier real column drop can retire the same backing.
		}
		if err := provider.replace(next); err != nil {
			return next, err
		}
		retired := [2]ids.ObjectAddress{constraint, address}
		roots := retired[:1]
		if address != (ids.ObjectAddress{}) {
			if err := provider.remove(provider.owner.catalog.domain.Ref(provider.base.Epoch(), address)); err != nil {
				return next, err
			}
			roots = retired[:]
		}
		if err := provider.expressionDependencies(roots, action.Mode, constraint); err != nil {
			return next, err
		}
		// CASCADE can remove a self-reference from this same parent.
		next = provider.descriptor(old.address).(*relationDescriptor)
	}
	return next, nil
}

func (scope *StatementScope) prepareConstraintIndexDrop(auth AuthorizationView, old, next *relationDescriptor, actions []parser.AlterTableAction, prior *preparedDrop) (*preparedDrop, error) {
	if !hasDroppedConstraintIndexes(old, actions) {
		return nil, nil
	}
	provider, err := scope.prepareDropDefinitionProvider(auth, next, scope.lockedRef, prior)
	if err != nil {
		return nil, err
	}
	next, err = dropConstraintIndexes(provider, old, next, actions)
	if err != nil {
		return nil, err
	}
	return scope.finishDropDefinition(provider, next)
}
