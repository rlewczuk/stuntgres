package engine

import (
	"context"
	"slices"
	"sort"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// PostgreSQL dependency traversal visits a dependent domain's descendants before
// resuming its parent's remaining dependents. Stable attributes are separate
// dependents; a containing table is never a domain-drop target.
func domainDependencyClosure(ctx context.Context, stage *CatalogStage, targets []ids.ObjectAddress) ([]table.DomainDependency, error) {
	return stageDropProvider(ctx, stage, nil, nil, nil).DependencyClosure(targets)
}

func domainRestrictError(stage *CatalogStage, targets []ids.ObjectAddress, edges []table.DomainDependency, originalRootCount int, subject ids.ObjectAddress) error {
	return view.RestrictError(stageDropProvider(context.Background(), stage, nil, nil, nil), targets, edges, originalRootCount, subject)
}

func (scope *StatementScope) stageDomainDependents(stage *CatalogStage, mode parser.DropMode) error {
	start := len(stage.dropCascadeNames)
	if err := scope.stageExpressionDependents(stage, stage.domainTargets, mode, nil); err != nil {
		return err
	}
	stage.schemaCascadeNames = append(stage.schemaCascadeNames, stage.dropCascadeNames[start:]...)
	stage.dropCascadeNames = stage.dropCascadeNames[:start]
	return nil
}

// All stored-expression owners use the same immutable authority traversal.
// Generated owners are removed through the existing column/storage projection.
func (scope *StatementScope) stageExpressionDependents(stage *CatalogStage, roots []ids.ObjectAddress, mode parser.DropMode, changes *session.Journal) error {
	return stageExpressionDependencies(scope.op.Context(), stage, roots, mode, scope, changes, nil, ids.ObjectAddress{})
}

// The same ordered discovery/descriptor core serves SQL, outer Commit and
// drained physical retirement. Only real SQL scopes perform grant acquisition;
// Commit supplies its actual private-root projector without inventing a scope.
// A nonzero subject attributes only the RESTRICT message to one SQL DROP;
// retirement roots, dependency discovery, locks and mutations stay unchanged.
func stageExpressionDependencies(ctx context.Context, stage *CatalogStage, roots []ids.ObjectAddress, mode parser.DropMode, scope *StatementScope, changes *session.Journal, project func(*catalog.Relation, *catalog.Relation) error, subject ids.ObjectAddress) error {
	return stageDropProvider(ctx, stage, scope, changes, project).ExpressionDependencies(roots, mode, subject)
}

func (provider *dropProvider) ExpressionDependencies(roots []ids.ObjectAddress, mode parser.DropMode, subject ids.ObjectAddress) error {
	scope := provider.scope
	if len(roots) == 0 {
		return nil
	}
	originalRootCount := len(roots)
	// Previously staged whole removals are retirement roots too. Discover their
	// complete dependent closure before classifying any surviving-owner mutation.
	// In particular a schema can remove both a provider and an inheritance parent.
	var retirementRoots []ids.ObjectAddress
	for address, descriptor := range provider.removalDescriptors() {
		if _, relation := descriptor.(*catalog.Relation); relation && !slices.Contains(roots, address) {
			retirementRoots = append(retirementRoots, address)
		}
	}
	if len(retirementRoots) != 0 {
		sort.Slice(retirementRoots, func(i, j int) bool { return retirementRoots[i].ObjectID < retirementRoots[j].ObjectID })
		roots = append(append([]ids.ObjectAddress(nil), roots...), retirementRoots...)
	}
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
	targets := append([]ids.ObjectAddress(nil), roots...)
	var relations []*catalog.Relation
	var hierarchyTargets []ids.ObjectAddress
	seen := make(map[ids.ObjectAddress]bool)
	for _, target := range targets {
		if target.ClassID == ids.RelationClassID && target.SubID == 0 {
			seen[target] = true
		}
	}
	for _, edge := range edges {
		// Discovery can enter an owned provider without emitting a removal
		// edge for it: relation retirement already removes its row type and
		// array. Preserve the captured provider identity when pruning stored
		// expressions, not just the requested root and dependent addresses.
		if !slices.Contains(targets, edge.Target) {
			targets = append(targets, edge.Target)
		}
		if fdw.IsCatalogClass(edge.Address.ClassID) || edge.Address.ClassID == ids.OperatorClassID || edge.Address.ClassID == ids.OperatorFamilyClassID || edge.Address.ClassID == ids.AccessMethodOperatorClassID || edge.Address.ClassID == ids.AccessMethodProcedureClassID || edge.Address.ClassID == ids.TypeClassID || edge.Address.ClassID == ids.ExtensionClassID || edge.Address.ClassID == ids.ProcedureClassID || edge.Address.ClassID == ids.TSTemplateClassID || edge.Address.ClassID == ids.TSDictionaryClassID || edge.Column != "" || edge.Relation != nil && edge.Address == edge.Relation.Address() {
			targets = append(targets, edge.Address)
			if edge.Address.ClassID == ids.RelationClassID && edge.Address.SubID == 0 {
				seen[edge.Address] = true
			}
		}
		if edge.Relation != nil && edge.Address == edge.Relation.Address() && catalog.HierarchyRelation(edge.Relation) {
			hierarchyTargets = append(hierarchyTargets, edge.Address)
		}
	}
	// The final closure can discover SQL/MED providers below a schema,
	// registered procedure or wrapper root. Fence their incoming identities
	// together with the existing extension retirement observation.
	for _, target := range targets {
		observe := target.ClassID == ids.ExtensionClassID || target.ClassID == ids.ForeignWrapperClassID || target.ClassID == ids.ForeignServerClassID
		if target.ClassID == ids.ProcedureClassID {
			procedure, ok := provider.RetirementDescriptor(target).(*catalog.Procedure)
			observe = ok && procedure.Foreign != nil
		}
		if observe {
			provider.observeIdentityDependencies(targets)
			break
		}
	}
	for _, edge := range edges {
		if edge.Relation != nil && (edge.Column != "" || edge.Address.ClassID == 2606) && !seen[edge.Relation.Address()] {
			relations = append(relations, edge.Relation)
			seen[edge.Relation.Address()] = true
		}
	}
	for _, edge := range edges {
		if scope != nil && edge.Relation != nil && edge.Address == edge.Relation.Address() && catalog.HierarchyRelation(edge.Relation) {
			if err := scope.acquirePartitionDropLocks(provider.owner.catalog.Domain.Ref(provider.base.Epoch(), edge.Address)); err != nil {
				return err
			}
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].Address().ObjectID < relations[j].Address().ObjectID })
	e := provider.owner
	if scope != nil {
		// Lock owners are not deletion/projection targets. A policy edge
		// captures its surviving owner's identity, even without a column edge.
		lockRelations := make([]*catalog.Relation, 0, len(relations))
		lockSeen := make(map[ids.ObjectAddress]bool)
		for _, r := range relations {
			lockRelations = append(lockRelations, r)
			lockSeen[r.Address()] = true
		}
		for _, edge := range edges {
			r := edge.Relation
			if r == nil || lockSeen[r.Address()] {
				continue
			}
			if edge.Address.ClassID == ids.PolicyClassID || edge.Address == r.Address() && (r.RelationKind == catalog.TableRelation || r.RelationKind == catalog.ForeignRelation) {
				lockRelations = append(lockRelations, r)
				lockSeen[r.Address()] = true
			}
		}
		sort.Slice(lockRelations, func(i, j int) bool { return lockRelations[i].Address().ObjectID < lockRelations[j].Address().ObjectID })
		for _, r := range lockRelations {
			ref := e.catalog.Domain.Ref(provider.base.Epoch(), r.Address())
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
			if err = provider.Remove(e.catalog.Domain.Ref(provider.base.Epoch(), target)); err != nil {
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
	for _, old := range relations {
		if provider.Removed(old.Address()) {
			continue
		}
		// Whole-relation targets need no standalone shape projection or
		// surviving-participant capability check.
		if slices.Contains(targets, old.Address()) {
			continue
		}
		ref := e.catalog.Domain.Ref(provider.base.Epoch(), old.Address())
		next := old
		consumed := false
		for _, edge := range edges {
			if edge.Column == "" || edge.Relation.Address() != old.Address() {
				continue
			}
			if provider.Removed(old.Address()) {
				break
			}
			if replacement, ok := provider.Replacement(old.Address()).(*catalog.Relation); ok {
				next = replacement
			}
			// The closure captured a live stable attribute. A nested cascade
			// may already have consumed it through a different provider path.
			slot := next.IndexForAttribute(edge.Address.SubID)
			if slot < 0 {
				continue
			}
			if err = provider.InheritanceAlterGuard(next); err != nil {
				return err
			}
			next, err = table.OrdinaryColumnDrop(provider, next, ref, parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: next.Columns[slot].Name}, Mode: parser.DropCascade})
			if err != nil {
				return err
			}
			consumed = true
		}
		if !consumed {
			continue
		}
		if provider.Removed(old.Address()) {
			continue
		}
		revision, er := provider.Revision(old.Revision())
		if er != nil {
			return er
		}
		next.ObjectRevision = revision
		if err := provider.projectColumn(old, next); err != nil {
			return err
		}
	}
	for _, edge := range edges {
		if edge.Domain == nil {
			continue
		}
		if provider.Removed(edge.Domain.Address()) {
			continue
		}
		if err := provider.removeDomainCheck(edge.Domain, edge.Address.ObjectID); err != nil {
			return err
		}
	}
	for _, target := range targets {
		if fdw.IsCatalogClass(target.ClassID) || target.ClassID == ids.OperatorClassID || target.ClassID == ids.OperatorFamilyClassID || target.ClassID == ids.AccessMethodOperatorClassID || target.ClassID == ids.AccessMethodProcedureClassID || target.ClassID == ids.TypeClassID || target.ClassID == ids.CollationClassID || target.ClassID == ids.ConversionClassID || target.ClassID == ids.ExtensionClassID || target.ClassID == ids.ProcedureClassID || target.ClassID == ids.TSTemplateClassID || target.ClassID == ids.TSDictionaryClassID {
			if err = provider.Remove(e.catalog.Domain.Ref(provider.base.Epoch(), target)); err != nil {
				break
			}
		}
	}
	if err != nil {
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
	if len(hierarchyTargets) != 0 {
		return provider.OwnedExpressionDependencies(hierarchyTargets, mode)
	}
	return nil
}

func (e *Engine) stageSchemaDomainsLocked(op *session.Operation, auth AuthorizationView, stage *CatalogStage, schemas []*catalog.Namespace, relations []*catalog.Relation, domains []catalog.Descriptor, cascade bool) error {
	var contained []catalog.Descriptor
	for _, r := range relations {
		contained = append(contained, r)
	}
	for _, d := range domains {
		contained = append(contained, d)
		stage.domainTargets = append(stage.domainTargets, d.Address())
		if extension, ok := d.(*catalog.Extension); ok {
			for _, member := range extension.Members {
				stage.domainTargets = append(stage.domainTargets, member)
				if err := stage.removeObjectLocked(e.catalog.Domain.Ref(stage.Base.Epoch(), member), false); err != nil {
					return err
				}
			}
		}
	}
	sort.Slice(contained, func(i, j int) bool { return contained[i].Address().ObjectID < contained[j].Address().ObjectID })
	sort.Slice(stage.domainTargets, func(i, j int) bool { return stage.domainTargets[i].ObjectID < stage.domainTargets[j].ObjectID })
	stage.dependencyPath, _ = e.referencePath(auth)
	for _, d := range contained {
		if err := stage.removeObjectLocked(e.catalog.Domain.Ref(stage.Base.Epoch(), d.Address()), false); err != nil {
			return err
		}
	}
	edges, err := domainDependencyClosure(op.Context(), stage, stage.domainTargets)
	if err != nil {
		return err
	}
	var details []string
	for _, d := range contained {
		label := stored.DependencyObject(stage.Base, d.Address(), stage.dependencyPath)
		namespaceAddress := d.Namespace()
		if extension, ok := d.(*catalog.Extension); ok {
			namespaceAddress = extension.TargetNamespace
		}
		namespace := stage.Base.Get(namespaceAddress).Name()
		details = append(details, label+" depends on schema "+catalog.DependencyIdentifier(namespace))
		stage.schemaCascadeNames = append(stage.schemaCascadeNames, "drop cascades to "+label)
		for _, edge := range edges {
			root := edge.Root == d.Address()
			if extension, ok := d.(*catalog.Extension); ok {
				root = root || slices.Contains(extension.Members[:], edge.Root)
			}
			if root && !edge.Automatic {
				details = append(details, edge.Label+" depends on "+stored.DependencyObject(stage.Base, edge.Target, stage.dependencyPath))
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
		if err = stage.removeObjectLocked(e.catalog.Domain.Ref(stage.Base.Epoch(), d.Address()), true); err != nil {
			return err
		}
	}
	return nil
}

func (stage *CatalogStage) removeDomainCheck(d *catalog.Type, oid ids.OID) error {
	return stageDropProvider(nil, stage, nil, nil, nil).removeDomainCheck(d, oid)
}

func (provider *dropProvider) removeDomainCheck(d *catalog.Type, oid ids.OID) error {
	if replacement, ok := provider.Replacement(d.Address()).(*catalog.Type); ok {
		d = replacement
	}
	next := *d
	next.DomainChecks = make([]catalog.DomainCheckConstraint, 0, len(d.DomainChecks))
	for _, check := range d.DomainChecks {
		if check.OID != oid {
			next.DomainChecks = append(next.DomainChecks, check)
		}
	}
	var err error
	next.SQLType, err = sqltype.Domain(d.SQLType.ID(), d.Name(), d.SQLType.SQLName(), d.SQLType.DomainBase(), len(next.DomainChecks) != 0, d.SQLType.OwnDomainNotNull())
	if err != nil {
		return err
	}
	next.ObjectRevision, err = provider.Revision(d.ObjectRevision)
	if err != nil {
		return err
	}
	return provider.Replace(&next)
}
