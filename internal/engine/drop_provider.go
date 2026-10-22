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
	prepared       *preparedDrop
	ctx            context.Context
	scope          *StatementScope
	changes        *session.Journal
	project        func(*catalog.Relation, *catalog.Relation) error
	identitySource *catalog.Relation   // Actual grant-time identity provider only.
	identityDrop   identityDropCapture // The sequence DROP IDENTITY captured at its grant.
}

type dropEffect struct {
	descriptor    catalog.Descriptor
	remove        ids.ObjectRef
	before, after *catalog.Relation
	notice        string
	missing       *sqlstate.Error
	rowType       ids.ObjectAddress
	identityRoots []ids.ObjectAddress
	identityState *catalog.State
	index         ids.ObjectAddress
	layout        *index.LayoutLineage
}

// Slots index only real ordered effects, never a second descriptor/name catalog.
// Indices are one-based so an address can have independent metadata and lineage.
type dropEffectSlot struct {
	descriptor, layout int
	removed            bool
}

type preparedDrop struct {
	old      *catalog.Relation
	ref      ids.ObjectRef
	next     *catalog.Relation
	prior    *preparedDrop
	effects  []dropEffect
	observed map[ids.ObjectAddress]catalog.Descriptor
	slots    map[ids.ObjectAddress]dropEffectSlot
	// statement is the preparing statement's identity: compared with the
	// consuming scope, never used to reach it.
	statement any
	txRef     ids.TxRef
	base      catalog.Read
	// Authentic read returned by admission; never replaces original base proofs.
	discovery catalog.Read
	input     catalog.DefinitionReader
	grants    []ids.ObjectRef
}

type definitionIndexLayoutReader interface {
	IndexLayout(ids.ObjectAddress) (index.LayoutLineage, bool)
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
	for prepared := p.prepared; prepared != nil; prepared = prepared.prior {
		if slot := prepared.slots[address]; slot.descriptor != 0 {
			effect := prepared.effects[slot.descriptor-1]
			if effect.remove.Address == address {
				return nil
			}
			if effect.after != nil {
				return effect.after
			}
			return effect.descriptor
		}
	}
	if p.identitySource != nil && p.identitySource.Address() == address {
		return p.identitySource
	}
	if p.prepared != nil && p.prepared.input != nil {
		descriptor, present := p.prepared.input.Descriptor(address)
		if present && descriptor != p.prepared.base.Get(address) {
			return descriptor
		}
	}
	return nil
}

func (p *dropProvider) Descriptor(address ids.ObjectAddress) catalog.Descriptor {
	p.observe(address)
	return p.projectedDescriptor(address)
}

func (p *dropProvider) Removed(address ids.ObjectAddress) bool {
	if p.stage != nil {
		return p.stage.Removed(address)
	}
	for prepared := p.prepared; prepared != nil; prepared = prepared.prior {
		if prepared.slots[address].removed {
			return true
		}
	}
	if p.prepared != nil && p.prepared.input != nil && p.prepared.base.Get(address) != nil {
		_, present := p.prepared.input.Descriptor(address)
		return !present
	}
	return false
}

func (p *dropProvider) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address, descriptor := range p.base.Descriptors() {
			if p.prepared != nil && p.prepared.input != nil {
				if projected, present := p.prepared.input.Descriptor(address); present {
					descriptor = projected
				}
			}
			if !yield(address, descriptor) {
				return
			}
		}
		if p.stage != nil {
			for address, descriptor := range p.stage.Descriptors() {
				if p.base.Get(address) == nil && !yield(address, descriptor) {
					return
				}
			}
		}
		if p.prepared != nil && p.prepared.input != nil {
			for address, descriptor := range p.prepared.input.Descriptors() {
				if p.base.Get(address) == nil && !yield(address, descriptor) {
					return
				}
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
	p.prepared.recordEffect(dropEffect{descriptor: d})
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
	if p.prepared == nil {
		return
	}
	if p.prepared.observed == nil {
		p.prepared.observed = make(map[ids.ObjectAddress]catalog.Descriptor)
	}
	if _, captured := p.prepared.observed[address]; captured {
		return
	}
	descriptor, original := p.prepared.base.Find(address)
	if !original {
		descriptor = p.base.Get(address)
	}
	p.prepared.observed[address] = descriptor
}

func (p *dropProvider) DescriptorCount() int {
	count := p.base.DescriptorCount()
	if p.stage != nil {
		for address := range p.stage.Descriptors() {
			if p.base.Get(address) == nil {
				count++
			}
		}
	}
	if p.prepared != nil && p.prepared.input != nil {
		for address := range p.prepared.input.Descriptors() {
			if p.base.Get(address) == nil {
				count++
			}
		}
	}
	return count
}

func (p *dropProvider) removalDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
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
	if p.prepared == nil {
		p.scope.op.EmitNotice(session.LevelNotice, notice)
		return
	}
	p.prepared.recordEffect(dropEffect{missing: &notice})
}

func (p *dropProvider) cascadeNotice(notice string) {
	if p.stage != nil {
		p.stage.dropCascadeNames = append(p.stage.dropCascadeNames, notice)
		return
	}
	p.prepared.recordEffect(dropEffect{notice: notice})
}

func (p *dropProvider) noticePosition() int {
	if p.stage != nil {
		return len(p.stage.dropCascadeNames)
	}
	return len(p.prepared.effects)
}

func (p *dropProvider) rewindNotices(position int) {
	if p.stage != nil {
		p.stage.dropCascadeNames = p.stage.dropCascadeNames[:position]
		return
	}
	// The position indexes effects, not notices. Keep preceding notices and
	// every non-notice effect in the actual provider order.
	kept := p.prepared.effects[:position]
	for _, effect := range p.prepared.effects[position:] {
		if effect.notice == "" {
			kept = append(kept, effect)
		}
	}
	p.prepared.effects = kept
	clear(p.prepared.slots)
	for i, effect := range p.prepared.effects {
		p.prepared.indexEffect(i+1, effect)
	}
}

func (p *dropProvider) observeIdentityDependencies(roots []ids.ObjectAddress) {
	var targets []ids.ObjectAddress
	if slices.ContainsFunc(roots, func(address ids.ObjectAddress) bool { return address.ClassID == ids.ExtensionClassID }) {
		// Extension retirement retains its existing complete expression fence.
		targets = slices.Clone(roots)
	} else {
		// SQL/MED observes only whole provider identities selected by the
		// completed closure, not unrelated relation/type/column targets.
		targets = make([]ids.ObjectAddress, 0, len(roots))
		for _, address := range roots {
			if address.SubID != 0 {
				continue
			}
			switch address.ClassID {
			case ids.ForeignWrapperClassID, ids.ForeignServerClassID:
				targets = append(targets, address)
			case ids.ProcedureClassID:
				if procedure, ok := p.RetirementDescriptor(address).(*catalog.Procedure); ok && procedure.Foreign != nil {
					targets = append(targets, address)
				}
			}
		}
	}
	if p.stage != nil {
		p.stage.sequenceState().ObserveIdentityDependencies(session.DependencyObservation{p.stage.Base.State(), targets})
		return
	}
	p.prepared.recordEffect(dropEffect{identityRoots: targets})
}

func (p *dropProvider) removedRowType(address ids.ObjectAddress) {
	if p.stage != nil {
		p.stage.domainTargets = append(p.stage.domainTargets, address)
		return
	}
	p.prepared.recordEffect(dropEffect{rowType: address})
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
		p.prepared.recordEffect(dropEffect{remove: ref})
		return nil
	}
	stage := p.stage
	stage.RecordRemoval(ref, d)
	if namespace {
		stage.removeSchemaDefaults(ref.Address)
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
		p.prepared.recordEffect(dropEffect{before: old, after: next})
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
	catalog, err := admitInheritanceDropClosure(p.scope, p.base, roots, p.removalDescriptors())
	if err != nil {
		return err
	}
	if p.stage != nil {
		p.stage.Base = catalog
	}
	p.base = catalog
	if p.prepared != nil {
		p.prepared.discovery = catalog
	}
	return nil
}

func (p *dropProvider) Acquire(ref ids.ObjectRef, reserveReplacement bool) error {
	if err := p.scope.acquireSequence(ref, lock.AccessExclusive, reserveReplacement); err != nil {
		return err
	}
	if p.prepared != nil && !slices.Contains(p.prepared.grants, ref) {
		p.prepared.grants = append(p.prepared.grants, ref)
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
func (scope *StatementScope) prepareColumnDrop(auth AuthorizationView, old *catalog.Relation, ref ids.ObjectRef, action parser.AlterTableAction) (*preparedDrop, error) {
	return scope.prepareColumnDropAfter(auth, old, ref, action, nil)
}

// prior is the immediately preceding successful DROP preparation. Its ordered
// real effects remain visible to dependency discovery, not just its root shape.
func (scope *StatementScope) prepareColumnDropAfter(auth AuthorizationView, old *catalog.Relation, ref ids.ObjectRef, action parser.AlterTableAction, prior *preparedDrop) (*preparedDrop, error) {
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

func (scope *StatementScope) prepareDropDefinitionProvider(auth AuthorizationView, old *catalog.Relation, ref ids.ObjectRef, prior *preparedDrop) (*dropProvider, error) {
	if err := scope.active(); err != nil {
		return nil, err
	}
	e := scope.engine()
	if err := scope.op.Context().Err(); err != nil {
		return nil, err
	}
	if scope.finished || auth.engine != e || auth.session != scope.op.Session || auth.Catalog.Owner() != e.catalog.owner() || ref.Domain() != e.catalog.Domain || ref.Epoch != auth.Catalog.Epoch() || ref.Epoch != scope.txRef.Epoch() || old == nil || old.Address() != ref.Address || ref.Address.SubID != 0 || !scope.holdsColumnDropGrant(ref) {
		return nil, ids.ErrCatalogReplaced
	}
	if _, present := auth.Catalog.Relation(ref.Address); !present {
		return nil, ids.ErrCatalogReplaced
	}
	if prior != nil && (prior.statement != any(scope) || prior.txRef != scope.txRef || prior.ref != ref || prior.next == nil) {
		return nil, ids.ErrCatalogReplaced
	}
	e.mu.Lock()
	path, err := referencePath(auth)
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	prepared := &preparedDrop{old: old, ref: ref, prior: prior, statement: scope, txRef: scope.txRef, base: auth.Catalog, discovery: auth.Catalog, input: dropDiscoveryInput(auth.definition)}
	provider := &dropProvider{base: auth.Catalog, owner: e, path: path, prepared: prepared, ctx: scope.op.Context(), scope: scope}
	provider.observe(ref.Address)
	return provider, nil
}

func (scope *StatementScope) finishDropDefinition(provider *dropProvider, next *catalog.Relation) (*preparedDrop, error) {
	prepared := provider.prepared
	prepared.next = next
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
func (scope *StatementScope) validateColumnDropObservationsLocked(prepared *preparedDrop, catalog catalog.Read) error {
	if prepared == nil || prepared.statement != any(scope) || prepared.txRef != scope.txRef || scope.finished || prepared.ref.Domain() != catalog.Owner().Domain || prepared.ref.Epoch != catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for address, descriptor := range prepared.observed {
		if catalog.Get(address) != descriptor {
			return ids.ErrCatalogReplaced
		}
	}
	return nil
}

// consumePreparedDrop applies the exact immutable provider effects to the sole
// command stage. Neither dependency analysis nor metadata cooking runs again.
func (scope *StatementScope) consumePreparedDrop(stage *CatalogStage, prepared *preparedDrop, old *catalog.Relation, ref ids.ObjectRef, changes *session.Journal) (*catalog.Relation, error) {
	if err := scope.active(); err != nil {
		return nil, err
	}
	if err := scope.op.Context().Err(); err != nil {
		return nil, err
	}
	if prepared == nil || prepared.old != old || prepared.ref != ref || prepared.next == nil || stage == nil || stage.owner != scope.engine() || stage.lifecycle != catalogOpen || stage.invalid != nil || !scope.holdsColumnDropGrant(ref) {
		return nil, ids.ErrCatalogReplaced
	}
	if _, consumed := scope.dropsOrZero().consumedColumns[prepared]; consumed {
		return nil, ids.ErrCatalogReplaced
	}
	if prepared.prior != nil {
		if _, consumed := scope.dropsOrZero().consumedColumns[prepared.prior]; !consumed {
			return nil, ids.ErrCatalogReplaced
		}
	}
	for _, grant := range prepared.grants {
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
		scope.drops().consumedColumns = make(map[*preparedDrop]struct{})
	}
	scope.drops().consumedColumns[prepared] = struct{}{}
	provider := stageDropProvider(scope.op.Context(), stage, scope, changes, nil)
	for _, effect := range prepared.effects {
		if err := scope.op.Context().Err(); err != nil {
			return nil, err
		}
		switch {
		case effect.descriptor != nil:
			if err := stage.replaceDescriptor(effect.descriptor, false, false); err != nil {
				return nil, err
			}
		case effect.remove.Address != (ids.ObjectAddress{}):
			e.mu.Lock()
			err = provider.recordRemoval(effect.remove, false)
			e.mu.Unlock()
			if err != nil {
				return nil, err
			}
		case effect.before != nil:
			if err := provider.projectPreparedColumn(effect.before, effect.after); err != nil {
				return nil, err
			}
		case effect.notice != "":
			stage.dropCascadeNames = append(stage.dropCascadeNames, effect.notice)
		case effect.missing != nil:
			scope.op.EmitNotice(session.LevelNotice, *effect.missing)
		case effect.rowType != (ids.ObjectAddress{}):
			stage.domainTargets = append(stage.domainTargets, effect.rowType)
		case len(effect.identityRoots) != 0:
			state := effect.identityState
			if state == nil {
				state = prepared.base.State()
			}
			stage.sequenceState().ObserveIdentityDependencies(session.DependencyObservation{state, effect.identityRoots})
		case effect.layout != nil:
			provider.RecordIndexLayout(effect.index, *effect.layout)
		}
	}
	return prepared.next, nil
}

// descriptor, lookup and descriptors are the read-only binder seam over this
// preparation's actual input and ordered effects. Consumers retain their normal
// resolution/dependency observations; these methods do not recertify or mutate
// them and do not construct a catalog generation.
func (prepared *preparedDrop) Descriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	provider := &dropProvider{base: prepared.discovery, prepared: prepared}
	if provider.Removed(address) {
		return nil, false
	}
	if descriptor := provider.Replacement(address); descriptor != nil {
		return descriptor, true
	}
	descriptor := provider.readInputDescriptor(address)
	return descriptor, descriptor != nil
}

func (prepared *preparedDrop) Lookup(key ids.NameKey) ids.ObjectAddress {
	address := prepared.base.NameAddress(key)
	if prepared.input != nil {
		address = prepared.input.Lookup(key)
	}
	if address == (ids.ObjectAddress{}) && prepared.base.NameAddress(key) == (ids.ObjectAddress{}) {
		address = prepared.discovery.NameAddress(key)
	}
	if descriptor, present := prepared.Descriptor(address); present && catalog.DescriptorNameKey(descriptor) == key {
		return address
	}
	return ids.ObjectAddress{}
}

func (prepared *preparedDrop) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		provider := &dropProvider{base: prepared.discovery, prepared: prepared}
		for address := range provider.Descriptors() {
			if descriptor, present := prepared.Descriptor(address); present && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (prepared *preparedDrop) ProceduresNamed(namespace ids.ObjectAddress, name string) iter.Seq[*catalog.Procedure] {
	return func(yield func(*catalog.Procedure) bool) {
		for d := range catalog.IndexedDefinitionProcedures(prepared.discovery, prepared, namespace, name) {
			if !yield(d) {
				return
			}
		}
		if prepared.input == nil {
			return
		}
		for candidate := range prepared.input.ProceduresNamed(namespace, name) {
			if original, exists := prepared.discovery.Procedure(candidate.Address()); exists && original.Namespace() == namespace && original.Name() == name {
				continue
			}
			descriptor, present := prepared.Descriptor(candidate.Address())
			if d, ok := descriptor.(*catalog.Procedure); present && ok && d.Namespace() == namespace && d.Name() == name {
				if !yield(d) {
					return
				}
			}
		}
	}
}
func (prepared *preparedDrop) HasSQLProcedureName(name string) bool {
	return prepared.discovery.State().HasSQLProcedureName(name) || prepared.input != nil && prepared.input.HasSQLProcedureName(name)
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
	for prepared := p.prepared; prepared != nil; prepared = prepared.prior {
		if slot := prepared.slots[address]; slot.layout != 0 {
			return *prepared.effects[slot.layout-1].layout, true
		}
	}
	if p.prepared != nil {
		if input, present := p.prepared.input.(definitionIndexLayoutReader); present {
			return input.IndexLayout(address)
		}
	}
	return index.LayoutLineage{}, false
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
	p.prepared.recordEffect(dropEffect{index: address, layout: &lineage})
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

func (prepared *preparedDrop) recordEffect(effect dropEffect) {
	prepared.effects = append(prepared.effects, effect)
	prepared.indexEffect(len(prepared.effects), effect)
}

func (prepared *preparedDrop) indexEffect(index int, effect dropEffect) {
	var address ids.ObjectAddress
	switch {
	case effect.descriptor != nil:
		address = effect.descriptor.Address()
	case effect.remove.Address != (ids.ObjectAddress{}):
		address = effect.remove.Address
	case effect.after != nil:
		address = effect.after.Address()
	case effect.layout != nil:
		address = effect.index
	default:
		return
	}
	if prepared.slots == nil {
		prepared.slots = make(map[ids.ObjectAddress]dropEffectSlot)
	}
	slot := prepared.slots[address]
	if effect.layout != nil {
		slot.layout = index
	} else {
		slot.descriptor = index
		slot.removed = slot.removed || effect.remove.Address != (ids.ObjectAddress{})
	}
	prepared.slots[address] = slot
}

// inputDescriptor reads the actual metadata entering this preparation. It never
// substitutes that candidate for the captured predecessor used by observe.
func (p *dropProvider) inputDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	p.observe(address)
	return p.readInputDescriptor(address)
}

func (p *dropProvider) readInputDescriptor(address ids.ObjectAddress) catalog.Descriptor {
	if p.identitySource != nil && p.identitySource.Address() == address {
		return p.identitySource
	}
	if p.prepared != nil && p.prepared.input != nil {
		descriptor, present := p.prepared.input.Descriptor(address)
		if present || p.prepared.base.Get(address) != nil {
			return descriptor
		}
	}
	return p.base.Get(address)
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

// Next is the relation the prepared drop leaves.
func (prepared *preparedDrop) Next() *catalog.Relation { return prepared.next }

// DefinitionDelta reports only the prepared drop's own descriptor effects.
func (prepared *preparedDrop) DefinitionDelta(address ids.ObjectAddress) (catalog.Descriptor, bool, bool) {
	if slot := prepared.slots[address]; slot.descriptor != 0 {
		if slot.removed {
			return nil, false, true
		}
		effect := prepared.effects[slot.descriptor-1]
		if effect.after != nil {
			return effect.after, true, true
		}
		return effect.descriptor, effect.descriptor != nil, true
	}
	return nil, false, false
}

// LayoutDelta reports only the prepared drop's own index layout effects.
func (prepared *preparedDrop) LayoutDelta(address ids.ObjectAddress) (index.LayoutLineage, bool) {
	if slot := prepared.slots[address]; slot.layout != 0 {
		return *prepared.effects[slot.layout-1].layout, true
	}
	return index.LayoutLineage{}, false
}

func (prepared *preparedDrop) IndexLayout(address ids.ObjectAddress) (index.LayoutLineage, bool) {
	provider := &dropProvider{base: prepared.base, prepared: prepared}
	return provider.IndexLayout(address)
}

// dropScope is the statement's DROP slot: prepared column drops already
// consumed by this statement.
type dropScope struct {
	consumedColumns map[*preparedDrop]struct{}
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
