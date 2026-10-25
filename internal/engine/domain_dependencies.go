package engine

import (
	"context"
	"slices"
	"sort"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
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

func domainRestrictError(stage *CatalogStage, targets []ids.ObjectAddress, edges []table.DomainDependency, originalRootCount int, subject ids.ObjectAddress) error {
	return view.RestrictError(StageDropProvider(context.Background(), stage, nil, nil, nil), targets, edges, originalRootCount, subject)
}

func stageDomainDependents(scope *StatementScope, stage *CatalogStage, mode parser.DropMode) error {
	start := len(stage.DropCascadeNames)
	if err := scope.StageExpressionDependents(stage, stage.DomainTargets, mode, nil); err != nil {
		return err
	}
	stage.SchemaCascadeNames = append(stage.SchemaCascadeNames, stage.DropCascadeNames[start:]...)
	stage.DropCascadeNames = stage.DropCascadeNames[:start]
	return nil
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

func stageSchemaDomainsLocked(e *Engine, op *session.Operation, auth AuthorizationView, stage *CatalogStage, schemas []*catalog.Namespace, relations []*catalog.Relation, domains []catalog.Descriptor, cascade bool) error {
	var contained []catalog.Descriptor
	for _, r := range relations {
		contained = append(contained, r)
	}
	for _, d := range domains {
		contained = append(contained, d)
		stage.DomainTargets = append(stage.DomainTargets, d.Address())
		if extension, ok := d.(*catalog.Extension); ok {
			for _, member := range extension.Members {
				stage.DomainTargets = append(stage.DomainTargets, member)
				if err := stage.RemoveObjectLocked(e.Catalog.Domain.Ref(stage.Base.Epoch(), member), false); err != nil {
					return err
				}
			}
		}
	}
	sort.Slice(contained, func(i, j int) bool { return contained[i].Address().ObjectID < contained[j].Address().ObjectID })
	sort.Slice(stage.DomainTargets, func(i, j int) bool { return stage.DomainTargets[i].ObjectID < stage.DomainTargets[j].ObjectID })
	stage.DependencyPath, _ = ReferencePath(auth)
	for _, d := range contained {
		if err := stage.RemoveObjectLocked(e.Catalog.Domain.Ref(stage.Base.Epoch(), d.Address()), false); err != nil {
			return err
		}
	}
	edges, err := DomainDependencyClosure(op.Context(), stage, stage.DomainTargets)
	if err != nil {
		return err
	}
	var details []string
	for _, d := range contained {
		label := stored.DependencyObject(stage.Base, d.Address(), stage.DependencyPath)
		namespaceAddress := d.Namespace()
		if typ, ok := d.(*catalog.Type); ok && typ.SQLType.IsRange() {
			for _, schema := range schemas {
				if schema.Address() == namespaceAddress {
					break
				}
				companion := stage.Base.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.SQLType.MultirangeType().ID())})
				if companion != nil && companion.Namespace() == schema.Address() {
					namespaceAddress = schema.Address()
					break
				}
			}
		}
		if extension, ok := d.(*catalog.Extension); ok {
			namespaceAddress = extension.TargetNamespace
		}
		namespace := stage.Base.Get(namespaceAddress).Name()
		details = append(details, label+" depends on schema "+catalog.DependencyIdentifier(namespace))
		stage.SchemaCascadeNames = append(stage.SchemaCascadeNames, "drop cascades to "+label)
		for _, edge := range edges {
			root := edge.Root == d.Address()
			if extension, ok := d.(*catalog.Extension); ok {
				root = root || slices.Contains(extension.Members[:], edge.Root)
			}
			if root && !edge.Automatic {
				details = append(details, edge.Label+" depends on "+stored.DependencyObject(stage.Base, edge.Target, stage.DependencyPath))
			}
		}
	}
	if !cascade {
		message := "cannot drop desired object(s) because other objects depend on them"
		if len(schemas) == 1 {
			message = "cannot drop schema " + catalog.DependencyIdentifier(schemas[0].Name()) + " because other objects depend on it"
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
	}
	for _, d := range schemas {
		if err = stage.RemoveObjectLocked(e.Catalog.Domain.Ref(stage.Base.Epoch(), d.Address()), true); err != nil {
			return err
		}
	}
	return nil
}
