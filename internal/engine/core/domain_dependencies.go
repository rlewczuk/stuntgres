package core

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// PostgreSQL dependency traversal visits a dependent domain's descendants before
// resuming its parent's remaining dependents. Stable attributes are separate
// dependents; a containing table is never a domain-drop target.
func DomainDependencyClosure(ctx context.Context, stage *CatalogStage, targets []ids.ObjectAddress) ([]table.DomainDependency, error) {
	return StageDropProvider(ctx, stage, nil, nil, nil).DependencyClosure(targets)
}

// All stored-expression owners use the same immutable authority traversal.
// Generated owners are removed through the existing column/storage projection.
func (scope *StatementScope) StageExpressionDependents(stage *CatalogStage, roots []ids.ObjectAddress, mode parser.DropMode, changes *session.Journal) error {
	return StageExpressionDependencies(scope.Op.Context(), stage, roots, mode, scope, changes, nil, ids.ObjectAddress{})
}

// The same ordered discovery/descriptor core serves SQL, outer Commit and
// drained physical retirement. Only real SQL scopes perform grant acquisition;
// Commit supplies its actual private-root projector without inventing a scope.
// A nonzero subject attributes only the RESTRICT message to one SQL DROP;
// retirement roots, dependency discovery, locks and mutations stay unchanged.
func StageExpressionDependencies(ctx context.Context, stage *CatalogStage, roots []ids.ObjectAddress, mode parser.DropMode, scope *StatementScope, changes *session.Journal, project func(*catalog.Relation, *catalog.Relation) error, subject ids.ObjectAddress) error {
	return StageDropProvider(ctx, stage, scope, changes, project).ExpressionDependencies(roots, mode, subject)
}

func (provider *DropProvider) ExpressionDependencies(roots []ids.ObjectAddress, mode parser.DropMode, subject ids.ObjectAddress) error {
	return provider.expressionDependencies(roots, mode, subject, ids.ObjectAddress{})
}

// ColumnDependencies shares discovery, admissions and retirement with provider
// drops, but leaves the original column's AUTO payload and shape to its owner.
// Like ExpressionDependencies, it is a controller: callers hold no engine lock.
func (provider *DropProvider) ColumnDependencies(target ids.ObjectAddress, mode parser.DropMode) error {
	if target.ClassID != ids.RelationClassID || target.SubID <= 0 {
		return fmt.Errorf("stuntgres: invalid column dependency target")
	}
	address := target
	address.SubID = 0
	relation, ok := provider.Descriptor(address).(*catalog.Relation)
	if !ok || relation.IndexForAttribute(target.SubID) < 0 {
		return ids.ErrCatalogReplaced
	}
	start := provider.noticePosition()
	provider.MarkDropRoots([]ids.ObjectAddress{target})
	err := provider.expressionDependencies([]ids.ObjectAddress{target}, mode, target, target)
	if err != nil || provider.Scope == nil {
		return err
	}
	// PostgreSQL reports each successful column action before the next action,
	// even when that later action fails. Preparation captures these ordinary
	// operation notices in its incumbent per-action replay, not another channel.
	var names []string
	if provider.Stage != nil {
		names = provider.Stage.DropCascadeNames[start:]
	} else {
		for _, effect := range provider.Prepared.Effects()[start:] {
			if effect.Notice != "" {
				names = append(names, effect.Notice)
			}
		}
	}
	view.EmitCascade(provider.Scope.Op, names)
	provider.rewindNotices(start)
	return nil
}

// DropDependencyPlan is execution-only ownership of one captured dependency
// phase. It is neither a catalog nor prepared-statement authority. Its sole
// provider applies it once, after that controller's original root removals.
type DropDependencyPlan struct {
	provider                  *DropProvider
	stage                     *CatalogStage
	prepared                  *table.DropPreparation
	scope                     *StatementScope
	changes                   *session.Journal
	epoch                     ids.CatalogEpoch
	selected                  int
	roots                     []ids.ObjectAddress
	edges, effectEdges        []table.DomainDependency
	plan, effectPlan          view.DropPlan
	mode                      parser.DropMode
	column                    ids.ObjectAddress
	consumed                  bool
	hierarchyOwned, rootOwned []ownedDropPhase
	ownedConsumed             bool
}

func (provider *DropProvider) PrepareExpressionDependencies(roots []ids.ObjectAddress, mode parser.DropMode, subject ids.ObjectAddress) (*DropDependencyPlan, error) {
	return provider.prepareDropDependencies(roots, mode, subject, ids.ObjectAddress{})
}

func (provider *DropProvider) expressionDependencies(roots []ids.ObjectAddress, mode parser.DropMode, subject, column ids.ObjectAddress) error {
	plan, err := provider.prepareDropDependencies(roots, mode, subject, column)
	if err != nil {
		return err
	}
	if provider.Stage != nil && len(provider.Stage.selectedDropRoots) != 0 && !provider.Stage.selectedDropsApplied {
		if err = provider.Stage.ApplySelectedDropRoots(plan); err != nil {
			return err
		}
	}
	return provider.ApplyExpressionDependencies(plan)
}
func (provider *DropProvider) prepareExpressionDependencies(roots []ids.ObjectAddress, mode parser.DropMode, subject, column ids.ObjectAddress, capturedEdges map[ids.ObjectAddress]bool) (*DropDependencyPlan, error) {
	scope := provider.Scope
	originalRootCount := len(roots)
	roots, selected := provider.selectedDropRootUnion(roots)
	if originalRootCount == 0 {
		originalRootCount = len(roots)
	}
	if len(roots) == 0 {
		return &DropDependencyPlan{provider: provider, stage: provider.Stage, prepared: provider.Prepared, scope: scope, changes: provider.Changes, epoch: provider.Base.Epoch(), selected: selected, mode: mode, column: column}, nil
	}
	// Previously staged whole removals are retirement roots too. Discover their
	// complete dependent closure before classifying any surviving-owner mutation.
	// In particular a schema can remove both a provider and an inheritance parent.
	roots = view.DropRoots(roots, provider.RemovalDescriptors())
	if err := provider.admitDropRootInputs(roots); err != nil {
		return nil, err
	}
	if err := provider.lockInheritanceClosure(roots); err != nil {
		return nil, err
	}
	edges, err := provider.DependencyClosure(roots)
	if err != nil {
		return nil, err
	}
	// Earlier successful phases own these exact effects and diagnostics. The
	// traversal still reads every incoming edge so new secondary consumers keep
	// their ordinary RESTRICT, grant and physical retirement semantics.
	if len(capturedEdges) != 0 {
		edges = slices.DeleteFunc(edges, func(edge table.DomainDependency) bool {
			return capturedEdges[edge.Address]
		})
	}
	visible := false
	for _, edge := range edges {
		visible = visible || !edge.Automatic
	}
	if mode == parser.DropRestrict && visible {
		err := view.RestrictError(provider, roots, edges, originalRootCount, subject)
		if column != (ids.ObjectAddress{}) && scope != nil && scope.Inheritance.Alter != nil && scope.Inheritance.Alter.ColumnDropMultiple(provider.Base, column) {
			err.(*sqlstate.Error).Message = "cannot drop desired object(s) because other objects depend on them"
		}
		return nil, err
	}
	plan := view.PlanDrop(provider, roots, edges)
	// Full captured targets own observations and grants. Only execution
	// defers local AUTO payload until all of its column metadata is cooked.
	targets := plan.Targets
	// Attached trigger retirement contributes payload identities, not relation
	// deletion or column projection targets. Retain the extracted shared plan
	// and extend its lock owners for these selected lifecycle edges.
	for _, edge := range edges {
		if edge.Address.ClassID == 2620 {
			targets = append(targets, edge.Address)
		}
	}
	plan.Targets = targets
	effectPlan, effectEdges := view.ColumnDropEffects(plan, edges, column)
	if plan.ObserveIdentities || slices.ContainsFunc(targets, func(address ids.ObjectAddress) bool {
		return address.ClassID == ids.TypeClassID || DefinitionObjectClass(address.ClassID)
	}) {
		provider.observeIdentityDependencies(targets)
	}
	e := provider.Owner
	// Low-level stage callers can have an operation scope without an admitted
	// transaction. Only real tickets enter closure grant acquisition; private
	// effects, observations and final publication fences remain shared.
	if scope != nil && scope.LockTicket != nil {
		if err := scope.acquireRetiredDefinitionObjects(provider.Base, targets); err != nil {
			return nil, err
		}
	}
	if scope != nil && scope.LockTicket != nil {
		for _, address := range plan.Hierarchy {
			if err := scope.AcquirePartitionDropLocks(e.Catalog.Domain.Ref(provider.Base.Epoch(), address)); err != nil {
				return nil, err
			}
		}
		lockRelations := plan.LockRelations(edges)
		for _, edge := range edges {
			if edge.Address.ClassID != 2620 || edge.Relation == nil {
				continue
			}
			address := edge.Relation.Address()
			if !slices.ContainsFunc(lockRelations, func(r *catalog.Relation) bool { return r.Address() == address }) {
				lockRelations = append(lockRelations, edge.Relation)
			}
		}
		sort.Slice(lockRelations, func(i, j int) bool { return lockRelations[i].Address().ObjectID < lockRelations[j].Address().ObjectID })
		for _, r := range lockRelations {
			ref := e.Catalog.Domain.Ref(provider.Base.Epoch(), r.Address())
			if err := provider.Acquire(ref, true); err != nil {
				return nil, err
			}
		}
		for _, edge := range edges {
			if edge.Relation == nil || edge.Relation.RelationKind != catalog.SequenceRelation || provider.Removed(edge.Address) {
				continue
			}
			ref := provider.BaseRef(edge.Address)
			owned := edge.Relation.Sequence.Owned
			owner := owned
			owner.SubID = 0
			// Capture a surviving identity owner's authentic linkage at the
			// real grant, before the batch removes its column metadata.
			if relation, ok := provider.Descriptor(owner).(*catalog.Relation); ok && edge.Relation.Sequence.Identity {
				if err := provider.CaptureIdentitySequence(relation, owned.SubID, ref); err != nil {
					return nil, err
				}
			} else if err := provider.Acquire(ref, false); err != nil {
				return nil, err
			}
		}
	}
	return &DropDependencyPlan{provider: provider, stage: provider.Stage, prepared: provider.Prepared, scope: scope, changes: provider.Changes, epoch: provider.Base.Epoch(), selected: selected, roots: roots, edges: edges, effectEdges: effectEdges, plan: plan, effectPlan: effectPlan, mode: mode, column: column}, nil
}

func (provider *DropProvider) ApplyExpressionDependencies(captured *DropDependencyPlan) error {
	if captured == nil || captured.provider != provider || captured.stage != provider.Stage || captured.prepared != provider.Prepared || captured.scope != provider.Scope || captured.changes != provider.Changes || captured.epoch != provider.Base.Epoch() || captured.consumed {
		return ids.ErrCatalogReplaced
	}
	if err := provider.Ctx.Err(); err != nil {
		return err
	}
	if provider.Stage != nil && (provider.Stage.Owner != provider.Owner || provider.Stage.Lifecycle != CatalogOpen || provider.Stage.Invalid != nil) {
		return ids.ErrCatalogReplaced
	}
	if provider.Scope != nil {
		if err := provider.Scope.Active(); err != nil {
			return err
		}
	}
	captured.consumed = true
	if len(captured.roots) == 0 {
		return nil
	}
	roots, edges, effectEdges := captured.roots, captured.edges, captured.effectEdges
	effectPlan, column := captured.effectPlan, captured.column
	e := provider.Owner
	var err error
	// The complete captured whole-relation closure is retirement authority before
	// any surviving DEFAULT/CHECK or column mutation is classified. Keep explicit
	// root removal policy and the original base/edges for discovery, owned objects,
	// diagnostics and publication fences. Type/collation providers retire later,
	// after surviving column candidates have finished binding against them.
	for _, target := range effectPlan.Targets[len(roots):] {
		if target.ClassID == ids.RelationClassID && target.SubID == 0 {
			if err = provider.Remove(e.Catalog.Domain.Ref(provider.Base.Epoch(), target)); err != nil {
				break
			}
		}
	}
	if err != nil {
		return err
	}
	if column != (ids.ObjectAddress{}) {
		owner := column
		owner.SubID = 0
		if relation := provider.Descriptor(owner).(*catalog.Relation); relation.RelationKind == catalog.CompositeRelation {
			var prunedChecks []ids.ObjectAddress
			prunedChecks, err = table.PruneCompositeFieldChecks(provider, effectEdges)
			if err != nil {
				return err
			}
			if len(prunedChecks) != 0 {
				effectEdges = slices.DeleteFunc(slices.Clone(effectEdges), func(edge table.DomainDependency) bool {
					return slices.Contains(prunedChecks, edge.Address)
				})
			}
		}
	}
	noticeStart := provider.noticePosition()
	if err = provider.ApplyViewDependents(effectPlan.Targets, parser.DropCascade); err != nil {
		return err
	}
	if err := provider.applyForeignKeyDependents(effectEdges); err != nil {
		return err
	}
	if err := table.CascadeColumnDrops(provider, effectPlan.Relations, effectEdges, effectPlan.Targets, provider.ProjectColumn); err != nil {
		return err
	}
	provider.rewindNotices(noticeStart)
	for _, edge := range edges {
		if !edge.Automatic {
			provider.cascadeNotice("drop cascades to " + edge.Label)
		}
	}
	// The hierarchy-owned phase was captured before any root removal.
	return provider.applyCapturedOwnedDrops(captured.hierarchyOwned)
}
