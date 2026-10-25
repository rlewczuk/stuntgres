package engine

import (
	"context"
	"fmt"
	"iter"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// DropProvider is the ordinary dependency algorithm's input/effect boundary.
// A preparation records real ordered effects; it is not a catalog or stage.
// Only the execution adapter can publish metadata or project physical rows.
type DropProvider struct {
	Base           catalog.Read
	Owner          *Engine
	SearchPath     []ids.ObjectAddress
	Stage          *CatalogStage
	Prepared       *table.DropPreparation
	Ctx            context.Context
	Scope          *StatementScope
	Changes        *session.Journal
	project        func(*catalog.Relation, *catalog.Relation) error
	identitySource *catalog.Relation   // Actual grant-time identity provider only.
	IdentityDrop   identityDropCapture // The sequence DROP IDENTITY captured at its grant.
}

func StageDropProvider(ctx context.Context, stage *CatalogStage, scope *StatementScope, changes *session.Journal, project func(*catalog.Relation, *catalog.Relation) error) *DropProvider {
	if ctx == nil {
		ctx = context.Background()
	}
	if stage == nil {
		// Missing-column IF EXISTS is an incumbent read-only fast path and
		// deliberately has no catalog stage to allocate or discard.
		return &DropProvider{Ctx: ctx, Scope: scope, Changes: changes, project: project}
	}
	return &DropProvider{Base: stage.Base, Owner: stage.Owner, SearchPath: stage.DependencyPath, Stage: stage, Ctx: ctx, Scope: scope, Changes: changes, project: project}
}

func (p *DropProvider) Replacement(address ids.ObjectAddress) catalog.Descriptor {
	if p.Stage != nil {
		return p.Stage.Replacement(address)
	}
	return p.preparedView().Replacement(address)
}

// preparedView reads through the prepared drop; a bare provider has none.
func (p *DropProvider) preparedView() table.DropPreparationView {
	return table.DropPreparationView{Base: p.Base, Preparation: p.Prepared, IdentitySource: p.identitySource}
}

func (p *DropProvider) Descriptor(address ids.ObjectAddress) catalog.Descriptor {
	p.Observe(address)
	return p.projectedDescriptor(address)
}

func (p *DropProvider) Removed(address ids.ObjectAddress) bool {
	if p.Stage != nil {
		return p.Stage.Removed(address)
	}
	return p.preparedView().Removed(address)
}

func (p *DropProvider) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	if p.Stage == nil {
		// An inheritance admission may replace p.base during the iteration.
		return p.preparedView().DescriptorsOver(func() catalog.Read { return p.Base })
	}
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address, descriptor := range p.Base.Descriptors() {
			if !yield(address, descriptor) {
				return
			}
		}
		for address, descriptor := range p.Stage.Descriptors() {
			if p.Base.Get(address) == nil && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (p *DropProvider) Replace(d catalog.Descriptor) error {
	p.Observe(d.Address())
	if err := p.Ctx.Err(); err != nil {
		return err
	}
	if p.Stage != nil {
		return p.Stage.Replace(d)
	}
	if p.Prepared == nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	p.Prepared.RecordEffect(table.DropEffect{Descriptor: d})
	if relation, ok := d.(*catalog.Relation); ok {
		return p.synchronizeRowType(relation)
	}
	return nil
}

func (p *DropProvider) Revision(revision ids.DefinitionRevision) (ids.DefinitionRevision, error) {
	if p.Stage != nil {
		return p.Stage.cascadeMetadataRevision(revision)
	}
	if revision == ids.DefinitionRevision(^uint64(0)) {
		return 0, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	return revision + 1, nil
}

func (p *DropProvider) Observe(address ids.ObjectAddress) {
	if p.Prepared != nil {
		p.Prepared.Observe(address, p.Base)
	}
}

func (p *DropProvider) DescriptorCount() int {
	if p.Stage == nil {
		return p.preparedView().DescriptorCount()
	}
	count := p.Base.DescriptorCount()
	for address := range p.Stage.Descriptors() {
		if p.Base.Get(address) == nil {
			count++
		}
	}
	return count
}

func (p *DropProvider) RemovalDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		if p.Stage != nil {
			for address, removal := range p.Stage.Removals() {
				if !yield(address, removal.Descriptor) {
					return
				}
			}
			return
		}
		for address, descriptor := range p.Base.Descriptors() {
			if p.Removed(address) && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (p *DropProvider) InheritanceAlterGuard(relation *catalog.Relation) error {
	return table.InheritanceDependencyAlterGuard(p.Descriptors(), p.Replacement, p.Removed, relation)
}

func (p *DropProvider) ApplyViewDependents(targets []ids.ObjectAddress, mode parser.DropMode, foreign ...table.DomainDependency) error {
	p.Owner.Mu.Lock()
	defer p.Owner.Mu.Unlock()
	return p.viewDependents(targets, mode, foreign...)
}

func (p *DropProvider) applyForeignKeyDependents(edges []table.DomainDependency) error {
	if p.Stage != nil {
		p.Owner.Mu.Lock()
		defer p.Owner.Mu.Unlock()
	}
	return table.ForeignKeyDependents(p, edges)
}

func (p *DropProvider) Notice(notice sqlstate.Error) {
	if p.Prepared != nil {
		p.Prepared.RecordEffect(table.DropEffect{Missing: &notice})
	} else if p.Scope != nil {
		p.Scope.Op.EmitNotice(session.LevelNotice, notice)
	}
}

func (p *DropProvider) cascadeNotice(notice string) {
	if p.Stage != nil {
		p.Stage.DropCascadeNames = append(p.Stage.DropCascadeNames, notice)
		return
	}
	p.Prepared.RecordEffect(table.DropEffect{Notice: notice})
}

func (p *DropProvider) noticePosition() int {
	if p.Stage != nil {
		return len(p.Stage.DropCascadeNames)
	}
	return p.Prepared.NoticePosition()
}

func (p *DropProvider) rewindNotices(position int) {
	if p.Stage != nil {
		p.Stage.DropCascadeNames = p.Stage.DropCascadeNames[:position]
		return
	}
	p.Prepared.RewindNotices(position)
}

func (p *DropProvider) observeIdentityDependencies(roots []ids.ObjectAddress) {
	targets := view.IdentityObservationTargets(p, roots)
	for _, address := range roots {
		if address.SubID == 0 && (address.ClassID == ids.TypeClassID || DefinitionObjectClass(address.ClassID)) && !slices.Contains(targets, address) {
			targets = append(targets, address)
		}
	}
	if p.Stage != nil {
		p.Stage.SequenceState().ObserveIdentityDependencies(session.DependencyObservation{p.Stage.Base.State(), targets})
		return
	}
	p.Prepared.RecordEffect(table.DropEffect{IdentityRoots: targets})
}

func (p *DropProvider) removedRowType(address ids.ObjectAddress) {
	if p.Stage != nil {
		p.Stage.DomainTargets = append(p.Stage.DomainTargets, address)
		return
	}
	p.Prepared.RecordEffect(table.DropEffect{RowType: address})
}

func (p *DropProvider) Remove(ref ids.ObjectRef) error {
	if p.Stage != nil {
		return p.Stage.Remove(ref)
	}
	if err := p.Ctx.Err(); err != nil {
		return err
	}
	p.Owner.Mu.Lock()
	defer p.Owner.Mu.Unlock()
	return p.removeObjectLocked(ref, false)
}

func (p *DropProvider) RecordRemoval(ref ids.ObjectRef, namespace bool) error {
	d := p.Base.Get(ref.Address)
	key := catalog.DescriptorNameKey(d)
	if p.Base.NameAddress(key) != ref.Address {
		return catalog.Conflict{}
	}
	p.Observe(ref.Address)
	if p.Stage == nil {
		p.Prepared.RecordEffect(table.DropEffect{Remove: ref})
		return nil
	}
	stage := p.Stage
	stage.RecordRemoval(ref, d)
	if namespace {
		stage.RemoveSchemaDefaults(ref.Address)
	}
	return nil
}

func (p *DropProvider) ReplacePartitionDescriptor(d *catalog.Relation) error {
	if p.Stage != nil {
		return index.StagePartitionDescriptor(p.Stage, d)
	}
	return p.Replace(d)
}

func (p *DropProvider) ProjectColumn(old, next *catalog.Relation) error {
	if p.Stage == nil {
		p.Observe(old.Address())
		if old.RelationKind != catalog.CompositeRelation && old.RelationKind != catalog.ForeignRelation {
			p.Owner.Mu.Lock()
			err := index.EvolveIndexes(p, old.Address(), nil)
			p.Owner.Mu.Unlock()
			if err != nil {
				return catalog.RenderDefaultClassAmbiguity(err, catalog.TypeDiagnosticNamer(p.Base, p.SearchPath))
			}
		}
		p.Prepared.RecordEffect(table.DropEffect{Before: old, After: next})
		return nil
	}
	stage, e, ctx := p.Stage, p.Owner, p.Ctx
	if old.RelationKind == catalog.CompositeRelation || old.RelationKind == catalog.ForeignRelation {
		return stage.Replace(next)
	}
	if p.project != nil {
		if err := p.project(old, next); err != nil {
			return err
		}
		return stage.Replace(next)
	}
	if p.Changes != nil {
		return p.Scope.stagePrivateColumnShape(stage, p.Changes, old, next)
	}
	e.Mu.Lock()
	stored := e.StageStorageLocked(stage, old.Address())
	storageOwner := stage.Base.MustRelation(old.Address())
	if e.StageCatalogBaseLocked(stage).Get(old.Address()) != storageOwner || stored == nil {
		e.Mu.Unlock()
		return catalog.Conflict{}
	}
	rows, ends, generation := stored.Versions, stored.Ends, stored.Generation
	e.Mu.Unlock()
	nextGeneration := generation + 1
	if generation == ^uint64(0) {
		if !stage.terminalCleanup {
			return fmt.Errorf("stuntgres: storage generation exhausted")
		}
		nextGeneration = generation
	}
	projected, err := session.ProjectVersions(ctx, rows, storageOwner, next, nil)
	if err != nil {
		return err
	}
	replacement := &storage.Relation{Versions: projected, Ends: ends, Generation: nextGeneration}
	ref := e.Catalog.Domain.Ref(stage.Base.Epoch(), old.Address())
	return stage.AlterRelation(ctx, ref, next, stored, replacement, len(rows), generation, ends)
}

func (p *DropProvider) lockInheritanceClosure(roots []ids.ObjectAddress) error {
	catalog, err := admitInheritanceDropClosure(p.Scope, p.Base, roots, p.RemovalDescriptors())
	if err != nil {
		return err
	}
	if p.Stage != nil {
		p.Stage.Base = catalog
	}
	p.Base = catalog
	if p.Prepared != nil {
		p.Prepared.Discovery = catalog
	}
	return nil
}

func (p *DropProvider) Acquire(ref ids.ObjectRef, reserveReplacement bool) error {
	if err := p.Scope.AcquireSequence(ref, lock.AccessExclusive, reserveReplacement); err != nil {
		return err
	}
	if p.Prepared != nil && !slices.Contains(p.Prepared.Grants, ref) {
		p.Prepared.Grants = append(p.Prepared.Grants, ref)
	}
	p.Observe(ref.Address)
	return nil
}

func (p *DropProvider) associatedArrayName(namespace ids.ObjectAddress, name string) string {
	if p.Stage != nil {
		return AssociatedArrayName(p.Stage, namespace, name)
	}
	return typedef.ChooseAssociatedArrayName(name, func(candidate string) bool {
		return p.Prepared.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: candidate}) != (ids.ObjectAddress{})
	})
}

// Some dependency passes retain retired providers for AUTO-edge discovery;
// partition validity instead reads only the actual surviving projection.
func (p *DropProvider) LiveDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	if p.Removed(address) {
		return nil
	}
	return p.projectedDescriptor(address)
}

// EvolutionBase is the catalog the pass started from.
func (p *DropProvider) EvolutionBase() catalog.Read { return p.Base }

// RemoveIndex removes the index at address from the pass.
func (p *DropProvider) RemoveIndex(address ids.ObjectAddress) error {
	return p.removeObjectLocked(p.Owner.Catalog.Domain.Ref(p.Base.Epoch(), address), false)
}

func (p *DropProvider) TerminalCleanup() bool {
	return p.Stage != nil && p.Stage.terminalCleanup
}

func (p *DropProvider) IndexLayout(address ids.ObjectAddress) (index.LayoutLineage, bool) {
	if p.Stage != nil {
		lineage, present := p.Stage.IndexesOrZero().Layouts[address]
		return lineage, present
	}
	return p.preparedView().IndexLayout(address)
}

func (p *DropProvider) RecordIndexLayout(address ids.ObjectAddress, lineage index.LayoutLineage) {
	if p.Stage != nil {
		if p.Stage.IndexesOrZero().Layouts == nil {
			p.Stage.Indexes().Layouts = make(map[ids.ObjectAddress]index.LayoutLineage)
		}
		p.Stage.Indexes().Layouts[address] = lineage
		return
	}
	p.Observe(address)
	p.Prepared.RecordEffect(table.DropEffect{Index: address, Layout: &lineage})
}

// inputDescriptor reads the actual metadata entering this preparation. It never
// substitutes that candidate for the captured predecessor used by observe.
func (p *DropProvider) inputDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	p.Observe(address)
	return p.readInputDescriptor(address)
}

func (p *DropProvider) readInputDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	return p.preparedView().InputDescriptor(address)
}

// Retirement discovery retains the original identity of an already removed
// provider so its AUTO closure remains traversable, just like the stage path.
func (p *DropProvider) RetirementDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	if descriptor := p.inputDescriptor(address); descriptor != nil {
		return descriptor
	}
	return p.Base.Get(address)
}

func (p *DropProvider) InputDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	if p.Prepared != nil {
		return p.Descriptors()
	}
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address, descriptor := range p.Base.Descriptors() {
			if !yield(address, descriptor) {
				return
			}
		}
	}
}

func (p *DropProvider) liveDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address := range p.Descriptors() {
			if descriptor := p.LiveDescriptor(address); descriptor != nil && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (p *DropProvider) policyDependentLabels(targets []ids.ObjectAddress) []view.Dependent {
	if p.Prepared == nil {
		return view.PolicyDependentLabels(p.Base, targets, p.SearchPath)
	}
	return view.PolicyDependentLabelsFrom(p.Base, targets, p.SearchPath, p.liveDescriptors())
}

func (p *DropProvider) ruleDependentLabels(targets []ids.ObjectAddress) []view.Dependent {
	if p.Prepared == nil {
		return view.RuleDependentLabels(p.Base, targets, p.SearchPath)
	}
	return view.RuleDependentLabelsFrom(p.Base, targets, p.SearchPath, p.liveDescriptors(), p.Observe)
}

// Catalog enumeration is discovery, not an observation of every unrelated
// descriptor. Selected providers are captured by descriptor/effect/grant paths.
func (p *DropProvider) projectedDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	if descriptor := p.Replacement(address); descriptor != nil {
		return descriptor
	}
	return p.readInputDescriptor(address)
}
