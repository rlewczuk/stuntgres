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
	op := scope.op
	if err := partition.RejectForeignKeys(syntax); err != nil {
		return nil, err
	}
	for _, observed := range scope.PartitionRelations {
		stage.domainBases = append(stage.domainBases, observed)
	}
	var parent *catalog.Relation
	if syntax.PartitionOf != nil {
		parentAuth, err := e.partitionParentAuthorization(op, auth, syntax)
		if err != nil {
			return nil, err
		}
		ref, err := e.resolveRelationReference(parentAuth, syntax.PartitionOf.Name, true)
		if err != nil {
			return nil, err
		}
		parent = auth.Catalog.MustRelation(ref.Address)
		if ref != scope.lockedRef || parent != scope.lockedDescriptor {
			return nil, ids.ErrCatalogReplaced
		}
		if err = parentAuth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return nil, err
		}
	}
	definition, err := partition.StageCreate(ddlHost{e: e, op: op, stage: stage}, auth, syntax, namespace, parent, auth.State.CurrentUser, scope.input, scope.foreignKeysOrZero().KeyTargets)
	if err != nil {
		return nil, err
	}
	pending := &pendingPartitionDefinition{Definition: *definition, stage: stage, auth: auth}
	if parent != nil && !pending.Bound.DefaultBound && parent.Partition.DefaultChild != (ids.ObjectAddress{}) {
		for _, ref := range pending.DefaultLeaves {
			storage := e.privateStorageLocked(scope.owner.Catalog, ref.Address)
			if storage == nil {
				return nil, ids.ErrCatalogReplaced
			}
			stage.storageReads = append(stage.storageReads, session.NewStorageReadFence(ref.Address, storage))
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
	ctx := scope.op.Context()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(scope), Input: scope.input, Output: scope.output}}
	if !p.Bound.DefaultBound {
		if err := partition.EvaluateBounds(ctx, &p.Definition, partitionBoundBinder{scope: scope, pending: p}, &evaluation); err != nil {
			return err
		}
		if err := partition.ValidateBounds(ctx, scope.input, &p.Definition); err != nil {
			return err
		}
		if err := partition.CheckOverlap(ctx, scope.input, p.stage.Base, &p.Definition); err != nil {
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
	e := scope.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	return partition.StageIndexChildren(ddlHost{e: e, op: scope.op, stage: p.stage}, p.Parent, p.Child.Address(), false)
}
