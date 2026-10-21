package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Retire named constraint identities and original backing generations after
// logical DROP, before ADD naming.
// Remove retains the original reference/name observations and locks the engine;
// neither retirement nor dependency traversal publishes the private candidate.
// Each action classifies its own dependencies before any later backing removal.
func (e *Engine) stageDroppedConstraintIndexes(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, old, next *catalog.Relation, actions []parser.AlterTableAction, changes *session.Journal) (*catalog.Relation, *CatalogStage, error) {
	if !table.HasDroppedConstraintIndexes(old, actions) {
		return next, stage, nil
	}
	if stage == nil {
		stage = e.BeginCatalogStage(auth.Catalog)
		stage.authSession, stage.authRevision, stage.roleRevision = scope.op.Session, auth.Revision, auth.RoleRecord.Revision
	}
	next, err := dropConstraintIndexes(stageDropProvider(scope.op.Context(), stage, scope, changes, nil), old, next, actions)
	return next, stage, err
}

// The same ordered provider retires named constraint identities, including
// constraints without backing indexes, during staging and pure preparation.
func dropConstraintIndexes(provider *dropProvider, old, next *catalog.Relation, actions []parser.AlterTableAction) (*catalog.Relation, error) {
	for _, action := range actions {
		constraint, address := table.DroppedConstraint(old, action)
		if constraint == (ids.ObjectAddress{}) {
			continue
		}
		if address != (ids.ObjectAddress{}) && provider.Removed(address) {
			continue // An earlier real column drop can retire the same backing.
		}
		if err := provider.Replace(next); err != nil {
			return next, err
		}
		retired := [2]ids.ObjectAddress{constraint, address}
		roots := retired[:1]
		if address != (ids.ObjectAddress{}) {
			if err := provider.remove(provider.owner.catalog.Domain.Ref(provider.base.Epoch(), address)); err != nil {
				return next, err
			}
			roots = retired[:]
		}
		if err := provider.expressionDependencies(roots, action.Mode, constraint); err != nil {
			return next, err
		}
		// CASCADE can remove a self-reference from this same parent.
		next = provider.Descriptor(old.Address()).(*catalog.Relation)
	}
	return next, nil
}

func (scope *StatementScope) prepareConstraintIndexDrop(auth AuthorizationView, old, next *catalog.Relation, actions []parser.AlterTableAction, prior *preparedDrop) (*preparedDrop, error) {
	if !table.HasDroppedConstraintIndexes(old, actions) {
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
