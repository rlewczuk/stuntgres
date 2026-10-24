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

// dropProvider is the ordinary dependency algorithm's input/effect boundary.
// A preparation records real ordered effects; it is not a catalog or stage.
// Only the execution adapter can publish metadata or project physical rows.
type dropProvider struct {
	base           catalog.Read
	owner          *Engine
	path           []ids.ObjectAddress
	stage          *CatalogStage
	prepared       *table.DropPreparation
	ctx            context.Context
	scope          *StatementScope
	changes        *session.Journal
	project        func(*catalog.Relation, *catalog.Relation) error
	identitySource *catalog.Relation   // Actual grant-time identity provider only.
	identityDrop   identityDropCapture // The sequence DROP IDENTITY captured at its grant.
}

func stageDropProvider(ctx context.Context, stage *CatalogStage, scope *StatementScope, changes *session.Journal, project func(*catalog.Relation, *catalog.Relation) error) *dropProvider {
	if ctx == nil {
		ctx = context.Background()
	}
	if stage == nil {
		// Missing-column IF EXISTS is an incumbent read-only fast path and
		// deliberately has no catalog stage to allocate or discard.
		return &dropProvider{ctx: ctx, scope: scope, changes: changes, project: project}
	}
	return &dropProvider{base: stage.Base, owner: stage.owner, path: stage.dependencyPath, stage: stage, ctx: ctx, scope: scope, changes: changes, project: project}
}

func (p *dropProvider) Replacement(address ids.ObjectAddress) catalog.Descriptor {
	if p.stage != nil {
		return p.stage.Replacement(address)
	}
	return p.preparedView().Replacement(address)
}

// preparedView reads through the prepared drop; a bare provider has none.
func (p *dropProvider) preparedView() table.DropPreparationView {
	return table.DropPreparationView{Base: p.base, Preparation: p.prepared, IdentitySource: p.identitySource}
}

func (p *dropProvider) Descriptor(address ids.ObjectAddress) catalog.Descriptor {
	p.observe(address)
	return p.projectedDescriptor(address)
}

func (p *dropProvider) Removed(address ids.ObjectAddress) bool {
	if p.stage != nil {
		return p.stage.Removed(address)
	}
	return p.preparedView().Removed(address)
}

func (p *dropProvider) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	if p.stage == nil {
		// An inheritance admission may replace p.base during the iteration.
		return p.preparedView().DescriptorsOver(func() catalog.Read { return p.base })
	}
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address, descriptor := range p.base.Descriptors() {
			if !yield(address, descriptor) {
				return
			}
		}
		for address, descriptor := range p.stage.Descriptors() {
			if p.base.Get(address) == nil && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (p *dropProvider) Replace(d catalog.Descriptor) error {
	p.observe(d.Address())
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if p.stage != nil {
		return p.stage.Replace(d)
	}
	if p.prepared == nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	p.prepared.RecordEffect(table.DropEffect{Descriptor: d})
	if relation, ok := d.(*catalog.Relation); ok {
		return p.synchronizeRowType(relation)
	}
	return nil
}

func (p *dropProvider) Revision(revision ids.DefinitionRevision) (ids.DefinitionRevision, error) {
	if p.stage != nil {
		return p.stage.cascadeMetadataRevision(revision)
	}
	if revision == ids.DefinitionRevision(^uint64(0)) {
		return 0, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	return revision + 1, nil
}

func (p *dropProvider) observe(address ids.ObjectAddress) {
	if p.prepared != nil {
		p.prepared.Observe(address, p.base)
	}
}

func (p *dropProvider) DescriptorCount() int {
	if p.stage == nil {
		return p.preparedView().DescriptorCount()
	}
	count := p.base.DescriptorCount()
	for address := range p.stage.Descriptors() {
		if p.base.Get(address) == nil {
			count++
		}
	}
	return count
}

func (p *dropProvider) RemovalDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		if p.stage != nil {
			for address, removal := range p.stage.Removals() {
				if !yield(address, removal.Descriptor) {
					return
				}
			}
			return
		}
		for address, descriptor := range p.base.Descriptors() {
			if p.Removed(address) && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (p *dropProvider) InheritanceAlterGuard(relation *catalog.Relation) error {
	return table.InheritanceDependencyAlterGuard(p.Descriptors(), p.Replacement, p.Removed, relation)
}

func (p *dropProvider) ApplyViewDependents(targets []ids.ObjectAddress, mode parser.DropMode, foreign ...table.DomainDependency) error {
	p.owner.mu.Lock()
	defer p.owner.mu.Unlock()
	return p.viewDependents(targets, mode, foreign...)
}

func (p *dropProvider) applyForeignKeyDependents(edges []table.DomainDependency) error {
	if p.stage != nil {
		p.owner.mu.Lock()
		defer p.owner.mu.Unlock()
	}
	return table.ForeignKeyDependents(p, edges)
}

func (p *dropProvider) Notice(notice sqlstate.Error) {
	if p.prepared != nil {
		p.prepared.RecordEffect(table.DropEffect{Missing: &notice})
	} else if p.scope != nil {
		p.scope.op.EmitNotice(session.LevelNotice, notice)
	}
}

func (p *dropProvider) cascadeNotice(notice string) {
	if p.stage != nil {
		p.stage.dropCascadeNames = append(p.stage.dropCascadeNames, notice)
		return
	}
	p.prepared.RecordEffect(table.DropEffect{Notice: notice})
}

func (p *dropProvider) noticePosition() int {
	if p.stage != nil {
		return len(p.stage.dropCascadeNames)
	}
	return p.prepared.NoticePosition()
}

func (p *dropProvider) rewindNotices(position int) {
	if p.stage != nil {
		p.stage.dropCascadeNames = p.stage.dropCascadeNames[:position]
		return
	}
	p.prepared.RewindNotices(position)
}

func (p *dropProvider) observeIdentityDependencies(roots []ids.ObjectAddress) {
	targets := view.IdentityObservationTargets(p, roots)
	for _, address := range roots {
		if address.SubID == 0 && (address.ClassID == ids.TypeClassID || definitionObjectClass(address.ClassID)) && !slices.Contains(targets, address) {
			targets = append(targets, address)
		}
	}
	if p.stage != nil {
		p.stage.sequenceState().ObserveIdentityDependencies(session.DependencyObservation{p.stage.Base.State(), targets})
		return
	}
	p.prepared.RecordEffect(table.DropEffect{IdentityRoots: targets})
}

func (p *dropProvider) removedRowType(address ids.ObjectAddress) {
	if p.stage != nil {
		p.stage.domainTargets = append(p.stage.domainTargets, address)
		return
	}
	p.prepared.RecordEffect(table.DropEffect{RowType: address})
}

func (p *dropProvider) Remove(ref ids.ObjectRef) error {
	if p.stage != nil {
		return p.stage.Remove(ref)
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	p.owner.mu.Lock()
	defer p.owner.mu.Unlock()
	return p.removeObjectLocked(ref, false)
}

func (p *dropProvider) recordRemoval(ref ids.ObjectRef, namespace bool) error {
	d := p.base.Get(ref.Address)
	key := catalog.DescriptorNameKey(d)
	if p.base.NameAddress(key) != ref.Address {
		return catalog.Conflict{}
	}
	p.observe(ref.Address)
	if p.stage == nil {
		p.prepared.RecordEffect(table.DropEffect{Remove: ref})
		return nil
	}
	stage := p.stage
	stage.RecordRemoval(ref, d)
	if namespace {
		stage.RemoveSchemaDefaults(ref.Address)
	}
	return nil
}

func (p *dropProvider) ReplacePartitionDescriptor(d *catalog.Relation) error {
	if p.stage != nil {
		return index.StagePartitionDescriptor(p.stage, d)
	}
	return p.Replace(d)
}

func (p *dropProvider) projectColumn(old, next *catalog.Relation) error {
	if p.stage == nil {
		p.observe(old.Address())
		if old.RelationKind != catalog.CompositeRelation && old.RelationKind != catalog.ForeignRelation {
			p.owner.mu.Lock()
			err := index.EvolveIndexes(p, old.Address(), nil)
			p.owner.mu.Unlock()
			if err != nil {
				return err
			}
		}
		p.prepared.RecordEffect(table.DropEffect{Before: old, After: next})
		return nil
	}
	stage, e, ctx := p.stage, p.owner, p.ctx
	if old.RelationKind == catalog.CompositeRelation || old.RelationKind == catalog.ForeignRelation {
		return stage.Replace(next)
	}
	if p.project != nil {
		if err := p.project(old, next); err != nil {
			return err
		}
		return stage.Replace(next)
	}
	if p.changes != nil {
		return p.scope.stagePrivateColumnShape(stage, p.changes, old, next)
	}
	e.mu.Lock()
	stored := e.stageStorageLocked(stage, old.Address())
	storageOwner := stage.Base.MustRelation(old.Address())
	if e.stageCatalogBaseLocked(stage).Get(old.Address()) != storageOwner || stored == nil {
		e.mu.Unlock()
		return catalog.Conflict{}
	}
	rows, ends, generation := stored.Versions, stored.Ends, stored.Generation
	e.mu.Unlock()
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
	ref := e.catalog.Domain.Ref(stage.Base.Epoch(), old.Address())
	return stage.alterRelation(ctx, ref, next, stored, replacement, len(rows), generation, ends)
}

func (p *dropProvider) lockInheritanceClosure(roots []ids.ObjectAddress) error {
	catalog, err := admitInheritanceDropClosure(p.scope, p.base, roots, p.RemovalDescriptors())
	if err != nil {
		return err
	}
	if p.stage != nil {
		p.stage.Base = catalog
	}
	p.base = catalog
	if p.prepared != nil {
		p.prepared.Discovery = catalog
	}
	return nil
}

func (p *dropProvider) Acquire(ref ids.ObjectRef, reserveReplacement bool) error {
	if err := p.scope.acquireSequence(ref, lock.AccessExclusive, reserveReplacement); err != nil {
		return err
	}
	if p.prepared != nil && !slices.Contains(p.prepared.Grants, ref) {
		p.prepared.Grants = append(p.prepared.Grants, ref)
	}
	p.observe(ref.Address)
	return nil
}

func (scope *StatementScope) holdsColumnDropGrant(ref ids.ObjectRef) bool {
	if scope.ticket == nil || scope.ticket.Ref() != scope.txRef {
		return false
	}
	return scope.engine().locks.Holds(scope.ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive)
}

// prepareColumnDrop records the actual ordinary provider effects without owning
// a catalog stage, visibility view, expression evaluation or physical rewrite.
func (scope *StatementScope) prepareColumnDrop(auth AuthorizationView, old *catalog.Relation, ref ids.ObjectRef, action parser.AlterTableAction) (*table.DropPreparation, error) {
	return scope.prepareColumnDropAfter(auth, old, ref, action, nil)
}

// prior is the immediately preceding successful DROP preparation. Its ordered
// real effects remain visible to dependency discovery, not just its root shape.
func (scope *StatementScope) prepareColumnDropAfter(auth AuthorizationView, old *catalog.Relation, ref ids.ObjectRef, action parser.AlterTableAction, prior *table.DropPreparation) (*table.DropPreparation, error) {
	provider, err := scope.prepareDropDefinitionProvider(auth, old, ref, prior)
	if err != nil {
		return nil, err
	}
	next, err := table.OrdinaryColumnDrop(provider, old, ref, action)
	if err != nil {
		return nil, err
	}
	return scope.finishDropDefinition(provider, next)
}

func (scope *StatementScope) prepareDropDefinitionProvider(auth AuthorizationView, old *catalog.Relation, ref ids.ObjectRef, prior *table.DropPreparation) (*dropProvider, error) {
	if err := scope.active(); err != nil {
		return nil, err
	}
	e := scope.engine()
	if err := scope.op.Context().Err(); err != nil {
		return nil, err
	}
	if scope.finished || auth.Owner != &e.catalog.Owner || auth.session != scope.op.Session || auth.Catalog.Owner() != &e.catalog.Owner || ref.Domain() != e.catalog.Domain || ref.Epoch != auth.Catalog.Epoch() || ref.Epoch != scope.txRef.Epoch() || old == nil || old.Address() != ref.Address || ref.Address.SubID != 0 || !scope.holdsColumnDropGrant(ref) {
		return nil, ids.ErrCatalogReplaced
	}
	if _, present := auth.Catalog.Relation(ref.Address); !present {
		return nil, ids.ErrCatalogReplaced
	}
	if prior != nil && (prior.Statement != any(scope) || prior.TxRef != scope.txRef || prior.Ref != ref || prior.Next() == nil) {
		return nil, ids.ErrCatalogReplaced
	}
	e.mu.Lock()
	path, err := referencePath(auth)
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	prepared := &table.DropPreparation{Old: old, Ref: ref, Prior: prior, Statement: scope, TxRef: scope.txRef, Base: auth.Catalog, Discovery: auth.Catalog, Input: dropDiscoveryInput(auth.definition)}
	provider := &dropProvider{base: auth.Catalog, owner: e, path: path, prepared: prepared, ctx: scope.op.Context(), scope: scope}
	provider.observe(ref.Address)
	return provider, nil
}

func (scope *StatementScope) finishDropDefinition(provider *dropProvider, next *catalog.Relation) (*table.DropPreparation, error) {
	prepared := provider.prepared
	prepared.Finish(next)
	e := provider.owner
	e.mu.Lock()
	err := scope.validateColumnDropObservationsLocked(prepared, e.privateCatalogLocked(scope.owner.Catalog))
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return prepared, nil
}

// validateColumnDropObservationsLocked never treats an additive catalog member
// as replacement. Only captured provider identities and the operation are fenced.
func (scope *StatementScope) validateColumnDropObservationsLocked(prepared *table.DropPreparation, catalog catalog.Read) error {
	if prepared == nil || scope.finished {
		return ids.ErrCatalogReplaced
	}
	return prepared.ValidateObservations(scope, scope.txRef, catalog)
}

// consumePreparedDrop applies the exact immutable provider effects to the sole
// command stage. Neither dependency analysis nor metadata cooking runs again.
func (scope *StatementScope) consumePreparedDrop(stage *CatalogStage, prepared *table.DropPreparation, old *catalog.Relation, ref ids.ObjectRef, changes *session.Journal) (*catalog.Relation, error) {
	if err := scope.active(); err != nil {
		return nil, err
	}
	if err := scope.op.Context().Err(); err != nil {
		return nil, err
	}
	if prepared == nil || prepared.Old != old || prepared.Ref != ref || prepared.Next() == nil || stage == nil || stage.owner != scope.engine() || stage.lifecycle != catalogOpen || stage.invalid != nil || !scope.holdsColumnDropGrant(ref) {
		return nil, ids.ErrCatalogReplaced
	}
	if _, consumed := scope.dropsOrZero().consumedColumns[prepared]; consumed {
		return nil, ids.ErrCatalogReplaced
	}
	if prepared.Prior != nil {
		if _, consumed := scope.dropsOrZero().consumedColumns[prepared.Prior]; !consumed {
			return nil, ids.ErrCatalogReplaced
		}
	}
	for _, grant := range prepared.Grants {
		if !scope.holdsColumnDropGrant(grant) {
			return nil, ids.ErrCatalogReplaced
		}
	}
	e := stage.owner
	e.mu.Lock()
	err := scope.validateColumnDropObservationsLocked(prepared, stage.Base)
	if err == nil {
		err = scope.validateColumnDropObservationsLocked(prepared, e.privateCatalogLocked(scope.owner.Catalog))
	}
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if scope.dropsOrZero().consumedColumns == nil {
		scope.drops().consumedColumns = make(map[*table.DropPreparation]struct{})
	}
	scope.drops().consumedColumns[prepared] = struct{}{}
	provider := stageDropProvider(scope.op.Context(), stage, scope, changes, nil)
	for _, effect := range prepared.Effects() {
		if err := scope.op.Context().Err(); err != nil {
			return nil, err
		}
		switch {
		case effect.Descriptor != nil:
			if err := stage.replaceDescriptor(effect.Descriptor, false, false); err != nil {
				return nil, err
			}
		case effect.Remove.Address != (ids.ObjectAddress{}):
			e.mu.Lock()
			err = provider.recordRemoval(effect.Remove, false)
			e.mu.Unlock()
			if err != nil {
				return nil, err
			}
		case effect.Before != nil:
			if err := provider.projectPreparedColumn(effect.Before, effect.After); err != nil {
				return nil, err
			}
		case effect.Notice != "":
			stage.dropCascadeNames = append(stage.dropCascadeNames, effect.Notice)
		case effect.Missing != nil:
			scope.op.EmitNotice(session.LevelNotice, *effect.Missing)
		case effect.RowType != (ids.ObjectAddress{}):
			stage.domainTargets = append(stage.domainTargets, effect.RowType)
		case len(effect.IdentityRoots) != 0:
			state := effect.IdentityState
			if state == nil {
				state = prepared.Base.State()
			}
			stage.sequenceState().ObserveIdentityDependencies(session.DependencyObservation{state, effect.IdentityRoots})
		case effect.Layout != nil:
			provider.RecordIndexLayout(effect.Index, *effect.Layout)
		}
	}
	return prepared.Next(), nil
}

func (p *dropProvider) associatedArrayName(namespace ids.ObjectAddress, name string) string {
	if p.stage != nil {
		return associatedArrayName(p.stage, namespace, name)
	}
	return typedef.ChooseAssociatedArrayName(name, func(candidate string) bool {
		return p.prepared.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: candidate}) != (ids.ObjectAddress{})
	})
}

// Some dependency passes retain retired providers for AUTO-edge discovery;
// partition validity instead reads only the actual surviving projection.
func (p *dropProvider) LiveDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	if p.Removed(address) {
		return nil
	}
	return p.projectedDescriptor(address)
}

// EvolutionBase is the catalog the pass started from.
func (p *dropProvider) EvolutionBase() catalog.Read { return p.base }

// RemoveIndex removes the index at address from the pass.
func (p *dropProvider) RemoveIndex(address ids.ObjectAddress) error {
	return p.removeObjectLocked(p.owner.catalog.Domain.Ref(p.base.Epoch(), address), false)
}

func (p *dropProvider) TerminalCleanup() bool {
	return p.stage != nil && p.stage.terminalCleanup
}

func (p *dropProvider) IndexLayout(address ids.ObjectAddress) (index.LayoutLineage, bool) {
	if p.stage != nil {
		lineage, present := p.stage.indexesOrZero().Layouts[address]
		return lineage, present
	}
	return p.preparedView().IndexLayout(address)
}

func (p *dropProvider) RecordIndexLayout(address ids.ObjectAddress, lineage index.LayoutLineage) {
	if p.stage != nil {
		if p.stage.indexesOrZero().Layouts == nil {
			p.stage.indexes().Layouts = make(map[ids.ObjectAddress]index.LayoutLineage)
		}
		p.stage.indexes().Layouts[address] = lineage
		return
	}
	p.observe(address)
	p.prepared.RecordEffect(table.DropEffect{Index: address, Layout: &lineage})
}

func (p *dropProvider) projectPreparedColumn(old, next *catalog.Relation) error {
	if old.RelationKind == catalog.CompositeRelation || old.RelationKind == catalog.ForeignRelation {
		return p.stage.replaceDescriptor(next, false, false)
	}
	if p.changes != nil {
		return p.scope.stagePreparedColumnShape(p.stage, p.changes, old, next)
	}
	// Non-SQL physical retirement already separates metadata from the storage
	// projector. This path retains its actual storage/lifetime checks.
	return p.projectColumn(old, next)
}

// inputDescriptor reads the actual metadata entering this preparation. It never
// substitutes that candidate for the captured predecessor used by observe.
func (p *dropProvider) inputDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	p.observe(address)
	return p.readInputDescriptor(address)
}

func (p *dropProvider) readInputDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	return p.preparedView().InputDescriptor(address)
}

// Retirement discovery retains the original identity of an already removed
// provider so its AUTO closure remains traversable, just like the stage path.
func (p *dropProvider) RetirementDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	if descriptor := p.inputDescriptor(address); descriptor != nil {
		return descriptor
	}
	return p.base.Get(address)
}

func (p *dropProvider) InputDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	if p.prepared != nil {
		return p.Descriptors()
	}
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address, descriptor := range p.base.Descriptors() {
			if !yield(address, descriptor) {
				return
			}
		}
	}
}

func (p *dropProvider) liveDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address := range p.Descriptors() {
			if descriptor := p.LiveDescriptor(address); descriptor != nil && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (p *dropProvider) policyDependentLabels(targets []ids.ObjectAddress) []view.Dependent {
	if p.prepared == nil {
		return view.PolicyDependentLabels(p.base, targets, p.path)
	}
	return view.PolicyDependentLabelsFrom(p.base, targets, p.path, p.liveDescriptors())
}

func (p *dropProvider) ruleDependentLabels(targets []ids.ObjectAddress) []view.Dependent {
	if p.prepared == nil {
		return view.RuleDependentLabels(p.base, targets, p.path)
	}
	return view.RuleDependentLabelsFrom(p.base, targets, p.path, p.liveDescriptors(), p.observe)
}

// Catalog enumeration is discovery, not an observation of every unrelated
// descriptor. Selected providers are captured by descriptor/effect/grant paths.
func (p *dropProvider) projectedDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	if descriptor := p.Replacement(address); descriptor != nil {
		return descriptor
	}
	return p.readInputDescriptor(address)
}

// dropScope is the statement's DROP slot: prepared column drops already
// consumed by this statement.
type dropScope struct {
	consumedColumns map[*table.DropPreparation]struct{}
}

// drops returns the statement's drops slot, allocating it on first use.
func (s *StatementScope) drops() *dropScope {
	if s.dropsSlot == nil {
		s.dropsSlot = new(dropScope)
	}
	return s.dropsSlot
}

// dropsOrZero reads the drops slot; a nil slot reads as zero.
func (s *StatementScope) dropsOrZero() dropScope {
	if s.dropsSlot == nil {
		return dropScope{}
	}
	return *s.dropsSlot
}

// dropDiscoveryInput is the definition a prepared drop discovers dependents
// through. An inheritance node reader's observations fence only the node's
// own cooking, so the drop reads through a copy that records nothing.
func dropDiscoveryInput(input catalog.DefinitionReader) catalog.DefinitionReader {
	if reader, present := input.(table.InheritanceDefinitionReader); present {
		return reader.DiscoveryReader()
	}
	return input
}
