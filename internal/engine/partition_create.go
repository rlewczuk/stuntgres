package engine

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
)

// A pending definition is private to one catalog stage. Raw bound datums are
// transformed in demand order; every Eval occurs outside Engine.mu.
type pendingPartitionDefinition struct {
	partition.Definition
	stage *CatalogStage
	auth  AuthorizationView
	view  *visibility.View
}

func (e *Engine) schemaPartitionTableLocked(scope *StatementScope, auth AuthorizationView, stage *CatalogStage, syntax *parser.CreateTable, namespace ids.ObjectAddress) (*pendingPartitionDefinition, error) {
	op := scope.Op
	if err := partition.RejectForeignKeys(syntax); err != nil {
		return nil, err
	}
	for _, observed := range scope.PartitionRelations {
		stage.DomainBases = append(stage.DomainBases, observed)
	}
	var parent *catalog.Relation
	if syntax.PartitionOf != nil {
		parentAuth, err := e.PartitionParentAuthorization(op, auth, syntax)
		if err != nil {
			return nil, err
		}
		ref, err := e.ResolveRelationReference(parentAuth, syntax.PartitionOf.Name, true)
		if err != nil {
			return nil, err
		}
		parent = auth.Catalog.MustRelation(ref.Address)
		if ref != scope.LockedRef || parent != scope.LockedDescriptor {
			return nil, ids.ErrCatalogReplaced
		}
		if err = parentAuth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return nil, err
		}
	}
	definition, err := partition.StageCreate(TableHost{DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, syntax, namespace, parent, auth.State.CurrentUser, scope.Input, scope.foreignKeysOrZero().KeyTargets)
	if err != nil {
		return nil, err
	}
	pending := &pendingPartitionDefinition{Definition: *definition, stage: stage, auth: auth}
	if parent != nil && !pending.Bound.DefaultBound && parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
		for _, ref := range pending.DefaultLeaves {
			storage := e.PrivateStorageLocked(scope.Owner.Catalog, ref.Address)
			if storage == nil {
				return nil, ids.ErrCatalogReplaced
			}
			stage.StorageReads = append(stage.StorageReads, session.NewStorageReadFence(ref.Address, storage))
		}
		// Constraint validation must include writers completed before the
		// hierarchy lock grant, without advancing the active utility pair.
		pending.view, err = scope.acquirePartitionValidationViewsLocked(e, pending.DefaultLeaves)
		if err != nil {
			return nil, err
		}
	}
	return pending, nil
}

func (p *pendingPartitionDefinition) finish(scope *StatementScope) error {
	if p == nil {
		return nil
	}
	if err := partition.CheckCoverage(p.stage.StageRead(), p.stage, p.Child); err != nil {
		return err
	}
	if p.Parent == nil {
		return partition.CheckSupported(&p.Definition)
	}
	ctx := scope.Op.Context()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: HostOf(scope), Input: scope.Input, Output: scope.Output}}
	if !p.Bound.DefaultBound {
		if err := partition.EvaluateBounds(ctx, &p.Definition, partitionBoundBinder{scope: scope, pending: p}, &evaluation); err != nil {
			return err
		}
		if err := partition.ValidateBounds(ctx, scope.Input, &p.Definition); err != nil {
			return err
		}
		if err := partition.CheckOverlap(ctx, scope.Input, p.stage.Base, &p.Definition); err != nil {
			return err
		}
		if err := partition.CheckSupported(&p.Definition); err != nil {
			return err
		}
		evaluation.Catalog, evaluation.RoutineAuthority = p.auth.Catalog, &p.auth
		if err := partition.CheckDefaultRows(ctx, &evaluation, p.view, p.stage.Base, &p.Definition); err != nil {
			return err
		}
	}
	if err := partition.CheckSupported(&p.Definition); err != nil {
		return err
	}
	parent, err := partition.WithChild(p.Parent, p.Child.Address(), p.Bound.DefaultBound)
	if err != nil {
		return err
	}
	if err := p.stage.Replace(parent); err != nil {
		return err
	}
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return partition.StageIndexChildren(TableHost{DDLHost{E: e, Op: scope.Op, CatalogStage: p.stage}}, p.Parent, p.Child.Address(), false)
}
