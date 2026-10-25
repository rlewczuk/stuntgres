package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
)

func (scope *StatementScope) inheritancePreparedNode() *table.InheritancePreparedNode {
	if scope.Inheritance.Alter == nil {
		return nil
	}
	return scope.Inheritance.Alter.Prepared[scope.LockedRef.Address]
}

func (scope *StatementScope) consumeInheritancePreparedAction(stage *CatalogStage, prepared *table.InheritancePreparedNode, idx int, old *catalog.Relation, ref ids.ObjectRef, changes *session.Journal) (*catalog.Relation, table.AddedColumn, bool, error) {
	if prepared == nil {
		return nil, table.AddedColumn{}, false, nil
	}
	step, exists := prepared.Steps[idx]
	if !exists {
		return nil, table.AddedColumn{}, false, nil
	}
	if step.Old != old {
		return nil, table.AddedColumn{}, true, ids.ErrCatalogReplaced
	}
	if len(step.Notices) != 0 {
		scope.Op.Notices = append(scope.Op.Notices, step.Notices...)
	}
	if step.Err != nil {
		return nil, table.AddedColumn{}, true, step.Err
	}
	for _, descriptor := range step.Types {
		if err := stage.ReplaceDescriptor(descriptor, false, false); err != nil {
			return nil, table.AddedColumn{}, true, err
		}
	}
	if step.Drop != nil {
		next, err := scope.consumePreparedDrop(stage, preparedDropOf(step.Drop), old, ref, changes)
		return next, step.Added, true, err
	}
	if step.Identity != nil {
		next, err := scope.consumeIdentityDropDefinition(stage, preparedIdentityDropOf(step.Identity), old, ref)
		return next, step.Added, true, err
	}
	if step.Using != nil {
		next, err := index.StageUsingIndex(TableHost{DDLHost{E: scope.Engine(), Op: scope.Op, CatalogStage: stage}}, old, step.Using)
		return next, step.Added, true, err
	}
	return step.Next, step.Added, true, nil
}
