package core

import (
	"context"
	"slices"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
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
	scope := provider.Scope
	if len(roots) == 0 {
		return nil
	}
	originalRootCount := len(roots)
	// Previously staged whole removals are retirement roots too. Discover their
	// complete dependent closure before classifying any surviving-owner mutation.
	// In particular a schema can remove both a provider and an inheritance parent.
	roots = view.DropRoots(roots, provider.RemovalDescriptors())
	if err := provider.lockInheritanceClosure(roots); err != nil {
		return err
	}
	edges, err := provider.DependencyClosure(roots)
	if err != nil {
		return err
	}
	visible := false
	for _, edge := range edges {
		visible = visible || !edge.Automatic
	}
	if mode == parser.DropRestrict && visible {
		return view.RestrictError(provider, roots, edges, originalRootCount, subject)
	}
	plan := view.PlanDrop(provider, roots, edges)
	targets, relations := plan.Targets, plan.Relations
	// Attached trigger retirement contributes payload identities, not relation
	// deletion or column projection targets. Retain the extracted shared plan
	// and extend its lock owners for these selected lifecycle edges.
	for _, edge := range edges {
		if edge.Address.ClassID == 2620 {
			targets = append(targets, edge.Address)
		}
	}
	if plan.ObserveIdentities || slices.ContainsFunc(targets, func(address ids.ObjectAddress) bool {
		return address.ClassID == ids.TypeClassID || DefinitionObjectClass(address.ClassID)
	}) {
		provider.observeIdentityDependencies(targets)
	}
	e := provider.Owner
	// Effect-free stage preparation may have a scope but no admitted transaction.
	// Only execution with a real ticket schedules retirement locks; direct stage
	// publication still checks the captured identities and dependency observations.
	if scope != nil && scope.LockTicket != nil {
		if err := scope.acquireRetiredDefinitionObjects(provider.Base, targets); err != nil {
			return err
		}
	}
	if scope != nil {
		for _, address := range plan.Hierarchy {
			if err := scope.AcquirePartitionDropLocks(e.Catalog.Domain.Ref(provider.Base.Epoch(), address)); err != nil {
				return err
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
				return err
			}
		}
	}
	// The complete captured whole-relation closure is retirement authority before
	// any surviving DEFAULT/CHECK or column mutation is classified. Keep explicit
	// root removal policy and the original base/edges for discovery, owned objects,
	// diagnostics and publication fences. Type/collation providers retire later,
	// after surviving column candidates have finished binding against them.
	for _, target := range targets[len(roots):] {
		if target.ClassID == ids.RelationClassID && target.SubID == 0 {
			if err = provider.Remove(e.Catalog.Domain.Ref(provider.Base.Epoch(), target)); err != nil {
				break
			}
		}
	}
	if err != nil {
		return err
	}
	noticeStart := provider.noticePosition()
	if err = provider.ApplyViewDependents(targets, parser.DropCascade); err != nil {
		return err
	}
	if err := provider.applyForeignKeyDependents(edges); err != nil {
		return err
	}
	if err := table.CascadeColumnDrops(provider, relations, edges, targets, provider.ProjectColumn); err != nil {
		return err
	}
	provider.rewindNotices(noticeStart)
	for _, edge := range edges {
		if !edge.Automatic {
			provider.cascadeNotice("drop cascades to " + edge.Label)
		}
	}
	// Newly discovered hierarchy owners are not the caller's original roots.
	// Stage their AUTO closure after removing them so recursive expression
	// traversal cannot rediscover the same hierarchy during sequence preparation.
	if len(plan.Hierarchy) != 0 {
		return table.OwnedExpressionDependencies(provider, plan.Hierarchy, mode)
	}
	return nil
}
