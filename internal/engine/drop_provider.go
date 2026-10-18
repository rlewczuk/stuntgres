package engine

import (
	"context"
	"fmt"
	"iter"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// dropProvider is the ordinary dependency algorithm's input/effect boundary.
// A preparation records real ordered effects; it is not a catalog or stage.
// Only the execution adapter can publish metadata or project physical rows.
type dropProvider struct {
	base           CatalogRead
	owner          *Engine
	path           []ids.ObjectAddress
	stage          *CatalogStage
	prepared       *preparedDrop
	ctx            context.Context
	scope          *StatementScope
	changes        *catalogChanges
	project        func(*relationDescriptor, *relationDescriptor) error
	identitySource *relationDescriptor // Actual grant-time identity provider only.
}

type dropEffect struct {
	descriptor    Descriptor
	remove        ids.ObjectRef
	before, after *relationDescriptor
	notice        string
	missing       *sqlstate.Error
	rowType       ids.ObjectAddress
	identityRoots []ids.ObjectAddress
	identityState *catalogState
	index         ids.ObjectAddress
	layout        *indexLayoutLineage
}

// Slots index only real ordered effects, never a second descriptor/name catalog.
// Indices are one-based so an address can have independent metadata and lineage.
type dropEffectSlot struct {
	descriptor, layout int
	removed            bool
}

type preparedDrop struct {
	old      *relationDescriptor
	ref      ids.ObjectRef
	next     *relationDescriptor
	prior    *preparedDrop
	effects  []dropEffect
	observed map[ids.ObjectAddress]Descriptor
	slots    map[ids.ObjectAddress]dropEffectSlot
	scope    *StatementScope
	txRef    ids.TxRef
	base     CatalogRead
	// Authentic read returned by admission; never replaces original base proofs.
	discovery CatalogRead
	input     definitionReader
	grants    []ids.ObjectRef
}

type definitionIndexLayoutReader interface {
	indexLayout(ids.ObjectAddress) (indexLayoutLineage, bool)
}

func stageDropProvider(ctx context.Context, stage *CatalogStage, scope *StatementScope, changes *catalogChanges, project func(*relationDescriptor, *relationDescriptor) error) *dropProvider {
	if ctx == nil {
		ctx = context.Background()
	}
	if stage == nil {
		// Missing-column IF EXISTS is an incumbent read-only fast path and
		// deliberately has no catalog stage to allocate or discard.
		return &dropProvider{ctx: ctx, scope: scope, changes: changes, project: project}
	}
	return &dropProvider{base: stage.base, owner: stage.owner, path: stage.dependencyPath, stage: stage, ctx: ctx, scope: scope, changes: changes, project: project}
}

func (p *dropProvider) replacement(address ids.ObjectAddress) Descriptor {
	if p.stage != nil {
		return p.stage.descriptors[address]
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
	if p.identitySource != nil && p.identitySource.address == address {
		return p.identitySource
	}
	if p.prepared != nil && p.prepared.input != nil {
		descriptor, present := p.prepared.input.descriptor(address)
		if present && descriptor != p.prepared.base.state.descriptors[address] {
			return descriptor
		}
	}
	return nil
}

func (p *dropProvider) descriptor(address ids.ObjectAddress) Descriptor {
	p.observe(address)
	return p.projectedDescriptor(address)
}

func (p *dropProvider) removed(address ids.ObjectAddress) bool {
	if p.stage != nil {
		_, removed := p.stage.removals[address]
		return removed
	}
	for prepared := p.prepared; prepared != nil; prepared = prepared.prior {
		if prepared.slots[address].removed {
			return true
		}
	}
	if p.prepared != nil && p.prepared.input != nil && p.prepared.base.state.descriptors[address] != nil {
		_, present := p.prepared.input.descriptor(address)
		return !present
	}
	return false
}

func (p *dropProvider) descriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return func(yield func(ids.ObjectAddress, Descriptor) bool) {
		for address, descriptor := range p.base.state.descriptors {
			if p.prepared != nil && p.prepared.input != nil {
				if projected, present := p.prepared.input.descriptor(address); present {
					descriptor = projected
				}
			}
			if !yield(address, descriptor) {
				return
			}
		}
		if p.stage != nil {
			for address, descriptor := range p.stage.descriptors {
				if p.base.state.descriptors[address] == nil && !yield(address, descriptor) {
					return
				}
			}
		}
		if p.prepared != nil && p.prepared.input != nil {
			for address, descriptor := range p.prepared.input.descriptors() {
				if p.base.state.descriptors[address] == nil && !yield(address, descriptor) {
					return
				}
			}
		}
	}
}

func (p *dropProvider) replace(d Descriptor) error {
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
	if relation, ok := d.(*relationDescriptor); ok {
		return p.synchronizeRowType(relation)
	}
	return nil
}

func (p *dropProvider) revision(revision ids.DefinitionRevision) (ids.DefinitionRevision, error) {
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
		p.prepared.observed = make(map[ids.ObjectAddress]Descriptor)
	}
	if _, captured := p.prepared.observed[address]; captured {
		return
	}
	descriptor, original := p.prepared.base.state.descriptors[address]
	if !original {
		descriptor = p.base.state.descriptors[address]
	}
	p.prepared.observed[address] = descriptor
}

func (p *dropProvider) descriptorCount() int {
	count := len(p.base.state.descriptors)
	if p.stage != nil {
		for address := range p.stage.descriptors {
			if p.base.state.descriptors[address] == nil {
				count++
			}
		}
	}
	if p.prepared != nil && p.prepared.input != nil {
		for address := range p.prepared.input.descriptors() {
			if p.base.state.descriptors[address] == nil {
				count++
			}
		}
	}
	return count
}

func (p *dropProvider) removalDescriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return func(yield func(ids.ObjectAddress, Descriptor) bool) {
		if p.stage != nil {
			for address, removal := range p.stage.removals {
				if !yield(address, removal.descriptor) {
					return
				}
			}
			return
		}
		for address, descriptor := range p.base.state.descriptors {
			if p.removed(address) && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (p *dropProvider) inheritanceAlterGuard(relation *relationDescriptor) error {
	return inheritanceDependencyAlterGuard(p.descriptors(), p.replacement, p.removed, relation)
}

func (p *dropProvider) applyViewDependents(targets []ids.ObjectAddress, mode parser.DropMode, foreign ...domainDependency) error {
	p.owner.mu.Lock()
	defer p.owner.mu.Unlock()
	return p.viewDependents(targets, mode, foreign...)
}

func (p *dropProvider) applyForeignKeyDependents(edges []domainDependency) error {
	if p.stage != nil {
		p.owner.mu.Lock()
		defer p.owner.mu.Unlock()
	}
	return p.foreignKeyDependents(edges)
}

func (p *dropProvider) missingColumnNotice(column, relation string) {
	notice := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`column "%s" of relation "%s" does not exist, skipping`, column, relation)}
	if p.prepared == nil {
		p.scope.op.emitNotice(levelNotice, notice)
		return
	}
	p.prepared.recordEffect(dropEffect{missing: &notice})
}

func (p *dropProvider) cascadeNotice(notice string) {
	if p.stage != nil {
		p.stage.viewCascadeNames = append(p.stage.viewCascadeNames, notice)
		return
	}
	p.prepared.recordEffect(dropEffect{notice: notice})
}

func (p *dropProvider) noticePosition() int {
	if p.stage != nil {
		return len(p.stage.viewCascadeNames)
	}
	return len(p.prepared.effects)
}

func (p *dropProvider) rewindNotices(position int) {
	if p.stage != nil {
		p.stage.viewCascadeNames = p.stage.viewCascadeNames[:position]
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
				if procedure, ok := p.retirementDescriptor(address).(*procedureDescriptor); ok && procedure.foreign != nil {
					targets = append(targets, address)
				}
			}
		}
	}
	if p.stage != nil {
		p.stage.identityDependencies = append(p.stage.identityDependencies, catalogDependencyObservation{p.stage.base.state, targets})
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

func (p *dropProvider) remove(ref ids.ObjectRef) error {
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
	d := p.base.state.descriptors[ref.Address]
	key := descriptorNameKey(d)
	if p.base.state.names[key] != ref.Address {
		return CatalogConflict{}
	}
	p.observe(ref.Address)
	if p.stage == nil {
		p.prepared.recordEffect(dropEffect{remove: ref})
		return nil
	}
	stage := p.stage
	if stage.removals == nil {
		stage.removals = make(map[ids.ObjectAddress]catalogRemoval)
	}
	stage.removals[ref.Address] = catalogRemoval{ref: ref, descriptor: d}
	if namespace {
		stage.removeSchemaDefaults(ref.Address)
	}
	// Retire private rebound candidates without resurrecting their name slots.
	if candidate := stage.descriptors[ref.Address]; candidate != nil {
		stagedKey := descriptorNameKey(candidate)
		if stage.names[stagedKey] == ref.Address {
			delete(stage.names, stagedKey)
		}
	}
	delete(stage.descriptors, ref.Address)
	stage.touched[key] = ref.Address
	return nil
}

func (p *dropProvider) replacePartitionDescriptor(d *relationDescriptor) error {
	if p.stage != nil {
		return stagePartitionDescriptor(p.stage, d)
	}
	return p.replace(d)
}

func (p *dropProvider) projectColumn(old, next *relationDescriptor) error {
	if p.stage == nil {
		p.observe(old.address)
		if old.relationKind != compositeRelation && old.relationKind != foreignRelation {
			p.owner.mu.Lock()
			err := p.evolveIndexes(old.address, nil)
			p.owner.mu.Unlock()
			if err != nil {
				return err
			}
		}
		p.prepared.recordEffect(dropEffect{before: old, after: next})
		return nil
	}
	stage, e, ctx := p.stage, p.owner, p.ctx
	if old.relationKind == compositeRelation || old.relationKind == foreignRelation {
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
	storage := e.stageStorageLocked(stage, old.address)
	storageOwner := stage.base.state.descriptors[old.address].(*relationDescriptor)
	if e.stageCatalogBaseLocked(stage).descriptors[old.address] != storageOwner || storage == nil {
		e.mu.Unlock()
		return CatalogConflict{}
	}
	rows, ends, generation := storage.versions, storage.ends, storage.generation
	e.mu.Unlock()
	nextGeneration := generation + 1
	if generation == ^uint64(0) {
		if !stage.terminalCleanup {
			return fmt.Errorf("stuntgres: storage generation exhausted")
		}
		nextGeneration = generation
	}
	projected, err := projectVersions(ctx, rows, storageOwner, next, nil)
	if err != nil {
		return err
	}
	replacement := &relationStorage{versions: projected, ends: ends, generation: nextGeneration}
	ref := e.catalog.domain.Ref(stage.base.Epoch(), old.address)
	return stage.alterRelation(ctx, ref, next, storage, replacement, len(rows), generation, ends)
}

func (p *dropProvider) lockInheritanceClosure(roots []ids.ObjectAddress) error {
	catalog, err := admitInheritanceDropClosure(p.scope, p.base, roots, p.removalDescriptors())
	if err != nil {
		return err
	}
	if p.stage != nil {
		p.stage.base = catalog
	}
	p.base = catalog
	if p.prepared != nil {
		p.prepared.discovery = catalog
	}
	return nil
}

func (p *dropProvider) acquire(ref ids.ObjectRef, reserveReplacement bool) error {
	if err := p.scope.acquireSequence(ref, accessExclusive, reserveReplacement); err != nil {
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
	return scope.op.session.Engine().locks.Holds(scope.ticket, lockResource{ObjectRef: ref}, accessExclusive)
}

// prepareColumnDrop records the actual ordinary provider effects without owning
// a catalog stage, visibility view, expression evaluation or physical rewrite.
func (scope *StatementScope) prepareColumnDrop(auth AuthorizationView, old *relationDescriptor, ref ids.ObjectRef, action parser.AlterTableAction) (*preparedDrop, error) {
	return scope.prepareColumnDropAfter(auth, old, ref, action, nil)
}

// prior is the immediately preceding successful DROP preparation. Its ordered
// real effects remain visible to dependency discovery, not just its root shape.
func (scope *StatementScope) prepareColumnDropAfter(auth AuthorizationView, old *relationDescriptor, ref ids.ObjectRef, action parser.AlterTableAction, prior *preparedDrop) (*preparedDrop, error) {
	provider, err := scope.prepareDropDefinitionProvider(auth, old, ref, prior)
	if err != nil {
		return nil, err
	}
	next, err := ordinaryColumnDrop(provider, old, ref, action)
	if err != nil {
		return nil, err
	}
	return scope.finishDropDefinition(provider, next)
}

func (scope *StatementScope) prepareDropDefinitionProvider(auth AuthorizationView, old *relationDescriptor, ref ids.ObjectRef, prior *preparedDrop) (*dropProvider, error) {
	if err := scope.active(); err != nil {
		return nil, err
	}
	e := scope.op.session.Engine()
	if err := scope.op.Context().Err(); err != nil {
		return nil, err
	}
	if scope.finished || auth.engine != e || auth.session != scope.op.session || auth.catalog.owner != e || ref.Domain() != e.catalog.domain || ref.Epoch != auth.catalog.Epoch() || ref.Epoch != scope.txRef.Epoch() || old == nil || old.address != ref.Address || ref.Address.SubID != 0 || !scope.holdsColumnDropGrant(ref) {
		return nil, ErrCatalogReplaced
	}
	if _, present := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor); !present {
		return nil, ErrCatalogReplaced
	}
	if prior != nil && (prior.scope != scope || prior.txRef != scope.txRef || prior.ref != ref || prior.next == nil) {
		return nil, ErrCatalogReplaced
	}
	e.mu.Lock()
	path, err := e.referencePath(auth)
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	prepared := &preparedDrop{old: old, ref: ref, prior: prior, scope: scope, txRef: scope.txRef, base: auth.catalog, discovery: auth.catalog, input: auth.definition}
	if reader, present := prepared.input.(interface{ discoveryReader() definitionReader }); present {
		prepared.input = reader.discoveryReader()
	}
	provider := &dropProvider{base: auth.catalog, owner: e, path: path, prepared: prepared, ctx: scope.op.Context(), scope: scope}
	provider.observe(ref.Address)
	return provider, nil
}

func (scope *StatementScope) finishDropDefinition(provider *dropProvider, next *relationDescriptor) (*preparedDrop, error) {
	prepared := provider.prepared
	prepared.next = next
	e := provider.owner
	e.mu.Lock()
	err := scope.validateColumnDropObservationsLocked(prepared, e.privateCatalogLocked(scope.owner.catalog))
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return prepared, nil
}

// validateColumnDropObservationsLocked never treats an additive catalog member
// as replacement. Only captured provider identities and the operation are fenced.
func (scope *StatementScope) validateColumnDropObservationsLocked(prepared *preparedDrop, catalog CatalogRead) error {
	if prepared == nil || prepared.scope != scope || prepared.txRef != scope.txRef || scope.finished || prepared.ref.Domain() != catalog.owner.catalog.domain || prepared.ref.Epoch != catalog.Epoch() {
		return ErrCatalogReplaced
	}
	for address, descriptor := range prepared.observed {
		if catalog.state.descriptors[address] != descriptor {
			return ErrCatalogReplaced
		}
	}
	return nil
}

// consumePreparedDrop applies the exact immutable provider effects to the sole
// command stage. Neither dependency analysis nor metadata cooking runs again.
func (scope *StatementScope) consumePreparedDrop(stage *CatalogStage, prepared *preparedDrop, old *relationDescriptor, ref ids.ObjectRef, changes *catalogChanges) (*relationDescriptor, error) {
	if err := scope.active(); err != nil {
		return nil, err
	}
	if err := scope.op.Context().Err(); err != nil {
		return nil, err
	}
	if prepared == nil || prepared.old != old || prepared.ref != ref || prepared.next == nil || stage == nil || stage.owner != scope.op.session.Engine() || stage.lifecycle != catalogOpen || stage.invalid != nil || !scope.holdsColumnDropGrant(ref) {
		return nil, ErrCatalogReplaced
	}
	if _, consumed := scope.consumedColumnDrops[prepared]; consumed {
		return nil, ErrCatalogReplaced
	}
	if prepared.prior != nil {
		if _, consumed := scope.consumedColumnDrops[prepared.prior]; !consumed {
			return nil, ErrCatalogReplaced
		}
	}
	for _, grant := range prepared.grants {
		if !scope.holdsColumnDropGrant(grant) {
			return nil, ErrCatalogReplaced
		}
	}
	e := stage.owner
	e.mu.Lock()
	err := scope.validateColumnDropObservationsLocked(prepared, stage.base)
	if err == nil {
		err = scope.validateColumnDropObservationsLocked(prepared, e.privateCatalogLocked(scope.owner.catalog))
	}
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if scope.consumedColumnDrops == nil {
		scope.consumedColumnDrops = make(map[*preparedDrop]struct{})
	}
	scope.consumedColumnDrops[prepared] = struct{}{}
	provider := stageDropProvider(scope.op.Context(), stage, scope, changes, nil)
	for _, effect := range prepared.effects {
		if err := scope.op.Context().Err(); err != nil {
			return nil, err
		}
		switch {
		case effect.descriptor != nil:
			if err := stage.replaceDescriptor(effect.descriptor, false); err != nil {
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
			stage.viewCascadeNames = append(stage.viewCascadeNames, effect.notice)
		case effect.missing != nil:
			scope.op.emitNotice(levelNotice, *effect.missing)
		case effect.rowType != (ids.ObjectAddress{}):
			stage.domainTargets = append(stage.domainTargets, effect.rowType)
		case len(effect.identityRoots) != 0:
			state := effect.identityState
			if state == nil {
				state = prepared.base.state
			}
			stage.identityDependencies = append(stage.identityDependencies, catalogDependencyObservation{state, effect.identityRoots})
		case effect.layout != nil:
			provider.recordIndexLayout(effect.index, *effect.layout)
		}
	}
	return prepared.next, nil
}

// descriptor, lookup and descriptors are the read-only binder seam over this
// preparation's actual input and ordered effects. Consumers retain their normal
// resolution/dependency observations; these methods do not recertify or mutate
// them and do not construct a catalog generation.
func (prepared *preparedDrop) descriptor(address ids.ObjectAddress) (Descriptor, bool) {
	provider := &dropProvider{base: prepared.discovery, prepared: prepared}
	if provider.removed(address) {
		return nil, false
	}
	if descriptor := provider.replacement(address); descriptor != nil {
		return descriptor, true
	}
	descriptor := provider.readInputDescriptor(address)
	return descriptor, descriptor != nil
}

func (prepared *preparedDrop) lookup(key ids.NameKey) ids.ObjectAddress {
	address := prepared.base.state.names[key]
	if prepared.input != nil {
		address = prepared.input.lookup(key)
	}
	if address == (ids.ObjectAddress{}) && prepared.base.state.names[key] == (ids.ObjectAddress{}) {
		address = prepared.discovery.state.names[key]
	}
	if descriptor, present := prepared.descriptor(address); present && descriptorNameKey(descriptor) == key {
		return address
	}
	return ids.ObjectAddress{}
}

func (prepared *preparedDrop) descriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return func(yield func(ids.ObjectAddress, Descriptor) bool) {
		provider := &dropProvider{base: prepared.discovery, prepared: prepared}
		for address := range provider.descriptors() {
			if descriptor, present := prepared.descriptor(address); present && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (p *dropProvider) associatedArrayName(namespace ids.ObjectAddress, name string) string {
	if p.stage != nil {
		return associatedArrayName(p.stage, namespace, name)
	}
	return chooseAssociatedArrayName(name, func(candidate string) bool {
		return p.prepared.lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: candidate}) != (ids.ObjectAddress{})
	})
}

// Some dependency passes retain retired providers for AUTO-edge discovery;
// partition validity instead reads only the actual surviving projection.
func (p *dropProvider) liveDescriptor(address ids.ObjectAddress) Descriptor {
	if p.removed(address) {
		return nil
	}
	return p.projectedDescriptor(address)
}

func (p *dropProvider) terminalCleanup() bool {
	return p.stage != nil && p.stage.terminalCleanup
}

func (p *dropProvider) indexLayout(address ids.ObjectAddress) (indexLayoutLineage, bool) {
	if p.stage != nil {
		lineage, present := p.stage.indexLayouts[address]
		return lineage, present
	}
	for prepared := p.prepared; prepared != nil; prepared = prepared.prior {
		if slot := prepared.slots[address]; slot.layout != 0 {
			return *prepared.effects[slot.layout-1].layout, true
		}
	}
	if p.prepared != nil {
		if input, present := p.prepared.input.(definitionIndexLayoutReader); present {
			return input.indexLayout(address)
		}
	}
	return indexLayoutLineage{}, false
}

func (p *dropProvider) recordIndexLayout(address ids.ObjectAddress, lineage indexLayoutLineage) {
	if p.stage != nil {
		if p.stage.indexLayouts == nil {
			p.stage.indexLayouts = make(map[ids.ObjectAddress]indexLayoutLineage)
		}
		p.stage.indexLayouts[address] = lineage
		return
	}
	p.observe(address)
	p.prepared.recordEffect(dropEffect{index: address, layout: &lineage})
}

func (p *dropProvider) projectPreparedColumn(old, next *relationDescriptor) error {
	if old.relationKind == compositeRelation || old.relationKind == foreignRelation {
		return p.stage.replaceDescriptor(next, false)
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
		address = effect.after.address
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
func (p *dropProvider) inputDescriptor(address ids.ObjectAddress) Descriptor {
	p.observe(address)
	return p.readInputDescriptor(address)
}

func (p *dropProvider) readInputDescriptor(address ids.ObjectAddress) Descriptor {
	if p.identitySource != nil && p.identitySource.address == address {
		return p.identitySource
	}
	if p.prepared != nil && p.prepared.input != nil {
		descriptor, present := p.prepared.input.descriptor(address)
		if present || p.prepared.base.state.descriptors[address] != nil {
			return descriptor
		}
	}
	return p.base.state.descriptors[address]
}

// Retirement discovery retains the original identity of an already removed
// provider so its AUTO closure remains traversable, just like the stage path.
func (p *dropProvider) retirementDescriptor(address ids.ObjectAddress) Descriptor {
	if descriptor := p.inputDescriptor(address); descriptor != nil {
		return descriptor
	}
	return p.base.state.descriptors[address]
}

func (p *dropProvider) inputDescriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	if p.prepared != nil {
		return p.descriptors()
	}
	return func(yield func(ids.ObjectAddress, Descriptor) bool) {
		for address, descriptor := range p.base.state.descriptors {
			if !yield(address, descriptor) {
				return
			}
		}
	}
}

func (p *dropProvider) liveDescriptors() iter.Seq2[ids.ObjectAddress, Descriptor] {
	return func(yield func(ids.ObjectAddress, Descriptor) bool) {
		for address := range p.descriptors() {
			if descriptor := p.liveDescriptor(address); descriptor != nil && !yield(address, descriptor) {
				return
			}
		}
	}
}

func (p *dropProvider) policyDependentLabels(targets []ids.ObjectAddress) []supportedDependent {
	if p.prepared == nil {
		return policyDependentLabels(p.base, targets, p.path)
	}
	return policyDependentLabelsFrom(p.base, targets, p.path, p.liveDescriptors())
}

// Catalog enumeration is discovery, not an observation of every unrelated
// descriptor. Selected providers are captured by descriptor/effect/grant paths.
func (p *dropProvider) projectedDescriptor(address ids.ObjectAddress) Descriptor {
	if descriptor := p.replacement(address); descriptor != nil {
		return descriptor
	}
	return p.readInputDescriptor(address)
}

func (prepared *preparedDrop) indexLayout(address ids.ObjectAddress) (indexLayoutLineage, bool) {
	provider := &dropProvider{base: prepared.base, prepared: prepared}
	return provider.indexLayout(address)
}
