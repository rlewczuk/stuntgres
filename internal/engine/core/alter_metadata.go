package core

import (
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// AlterMetadataPhase is the immutable result of the real metadata publication,
// not an alternate execution catalog. Its observations protect physical work
// against callback-private successors while the journal retains commit fences.
type AlterMetadataPhase struct {
	View    catalog.Read
	indexes map[ids.ObjectAddress][]*catalog.Relation
	Targets map[ids.ObjectAddress]bool
	final   map[ids.ObjectAddress]catalog.Descriptor
	sources map[ids.ObjectAddress]session.StorageReadFence
}

func (e *Engine) PublishAlterMetadata(scope *StatementScope, stage *CatalogStage, parents, validityOnly map[ids.ObjectAddress]bool) (AlterMetadataPhase, error) {
	stage.deferredAlterRows = parents
	if err := scope.AcquireDefinitionProviders(stage); err != nil {
		return AlterMetadataPhase{}, err
	}
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	if scope.Catalog == nil {
		scope.Catalog = scope.PendingCatalog().Clone()
	}
	sources := make(map[ids.ObjectAddress]session.StorageReadFence, len(parents))
	e.Mu.Lock()
	for parent := range parents {
		if validityOnly[parent] {
			sources[parent] = session.NewStorageIdentityFence(parent, e.PrivateStorageLocked(scope.PendingCatalog(), parent))
			scope.Catalog.AddFence(session.NewStorageIdentityFence(parent, e.Storage.Relations[parent]))
		} else {
			sources[parent] = session.NewStorageReadFence(parent, e.PrivateStorageLocked(scope.PendingCatalog(), parent))
			scope.Catalog.AddFence(session.NewStorageReadFence(parent, e.Storage.Relations[parent]))
		}
	}
	e.Mu.Unlock()
	err := e.TransferAlterStage(scope, stage)
	if err != nil {
		return AlterMetadataPhase{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return AlterMetadataPhase{}, err
	}
	phase := AlterMetadataPhase{View: auth.Catalog, Targets: parents, final: make(map[ids.ObjectAddress]catalog.Descriptor), sources: sources}
	phase.Record(stage)
	for _, d := range phase.View.Descriptors() {
		idx, ok := d.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || !parents[idx.SQLIndex.Parent] {
			continue
		}
		if phase.indexes == nil {
			phase.indexes = make(map[ids.ObjectAddress][]*catalog.Relation)
		}
		phase.indexes[idx.SQLIndex.Parent] = append(phase.indexes[idx.SQLIndex.Parent], idx)
	}
	copiedTargets := false
	for a, d := range stage.Descriptors() {
		relation, ok := d.(*catalog.Relation)
		if !ok || relation.SQLIndex != nil || phase.Targets[a] {
			continue
		}
		if !copiedTargets {
			phase.Targets = maps.Clone(parents)
			if phase.Targets == nil {
				phase.Targets = make(map[ids.ObjectAddress]bool)
			}
			copiedTargets = true
		}
		phase.Targets[a] = true
	}
	for a, generator := range stage.Sequence.StagedGenerators() {
		if stage.Sequence.LazyPersistence(a) {
			continue
		}
		if memory := PayloadOf(scope.Op.Session).Sequences.Memory(a); memory != nil {
			memory.DiscardStaleTailLocked(generator.Generation)
		}
	}
	if err := e.AdvanceAlterGrantsLocked(scope, phase.Targets); err != nil {
		return AlterMetadataPhase{}, err
	}
	return phase, nil
}

func (e *Engine) AlterPhysicalProofLocked(phase AlterMetadataPhase, current catalog.Read, parent ids.ObjectAddress) error {
	if current.Epoch() != phase.View.Epoch() {
		return ids.ErrCatalogReplaced
	}
	expected, ok := phase.View.Relation(parent)
	if !ok || current.Get(parent) != expected {
		return catalog.Conflict{}
	}
	if current.State() == phase.View.State() {
		return nil
	}
	provider := func(a ids.ObjectAddress) error {
		if current.Get(a) != phase.View.Get(a) {
			return catalog.Conflict{}
		}
		return nil
	}
	if err := visitDefinitionProviders(phase.View, expected, provider); err != nil {
		return err
	}
	for _, idx := range phase.indexes[parent] {
		if current.Get(idx.Address()) != idx {
			return catalog.Conflict{}
		}
		if err := visitDefinitionProviders(phase.View, idx, provider); err != nil {
			return err
		}
	}
	count := 0
	for _, d := range current.Descriptors() {
		idx, ok := d.(*catalog.Relation)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Parent != parent {
			continue
		}
		if phase.View.Get(idx.Address()) != idx {
			return catalog.Conflict{}
		}
		count++
	}
	if count != len(phase.indexes[parent]) {
		return catalog.Conflict{}
	}
	return nil
}

func (e *Engine) BeginAlterPhysical(scope *StatementScope, phase AlterMetadataPhase, parent ids.ObjectAddress, source *storage.Relation, rewriteReason int32) (AuthorizationView, *CatalogStage, error) {
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(scope.Op)
	if err == nil {
		err = e.AlterPhysicalProofLocked(phase, auth.Catalog, parent)
	}
	if source == nil {
		source = e.PrivateStorageLocked(scope.PendingCatalog(), parent)
	}
	if err == nil && !phase.sources[parent].Matches(source) {
		err = catalog.Conflict{}
	}
	if err == nil && e.PrivateStorageLocked(scope.PendingCatalog(), parent) != source {
		err = catalog.Conflict{}
	}
	if err == nil && rewriteReason == 0 {
		stage := e.BeginCatalogStage(auth.Catalog)
		stage.AuthorizeLocked(auth, scope.Op.Session)
		scope.RoutineAuth = auth
		e.Mu.Unlock()
		return auth, stage, nil
	}
	e.Mu.Unlock()
	if err != nil {
		return AuthorizationView{}, nil, err
	}
	if err = scope.fireTableRewrite(parent.ObjectID, rewriteReason); err != nil {
		return AuthorizationView{}, nil, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err = e.AuthorizationLocked(scope.Op)
	if err == nil {
		err = e.AlterPhysicalProofLocked(phase, auth.Catalog, parent)
	}
	if err == nil && e.PrivateStorageLocked(scope.PendingCatalog(), parent) != source {
		err = catalog.Conflict{}
	}
	if err == nil && !phase.sources[parent].Matches(source) {
		err = catalog.Conflict{}
	}
	if err != nil {
		return AuthorizationView{}, nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	scope.RoutineAuth = auth
	return auth, stage, nil
}

func CarryAlterLayouts(stage *CatalogStage, layouts map[ids.ObjectAddress]index.LayoutLineage, parent ids.ObjectAddress) {
	for a, lineage := range layouts {
		idx, ok := stage.Base.Relation(a)
		if !ok || idx.SQLIndex == nil || idx.SQLIndex.Parent != parent {
			continue
		}
		if stage.IndexesOrZero().Layouts == nil {
			stage.Indexes().Layouts = make(map[ids.ObjectAddress]index.LayoutLineage)
		}
		stage.Indexes().Layouts[a] = lineage
	}
}

// Move only this ALTER's already admitted Targets to its authentic private
// successor. Physical work retains its separate old descriptor/store facts;
// journal Originals remain the unchanged committed publication fences.
func (e *Engine) AdvanceAlterGrantsLocked(scope *StatementScope, targets map[ids.ObjectAddress]bool) error {
	current := e.PrivateCatalogLocked(scope.PendingCatalog())
	var err error
	e.visitRelationGrantsLocked(scope, nil, func(ref ids.ObjectRef, captured catalog.Descriptor, store *storage.Relation) *catalog.Relation {
		if targets[ref.Address] {
			if _, ok := current.Relation(ref.Address); !ok {
				err = catalog.Conflict{}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	e.visitRelationGrantsLocked(scope, scope.PendingCatalog(), func(ref ids.ObjectRef, captured catalog.Descriptor, store *storage.Relation) *catalog.Relation {
		if targets[ref.Address] {
			return current.MustRelation(ref.Address)
		}
		return nil
	})
	return nil
}

// The selector either leaves an existing admission alone or supplies its proven
// successor. Callers prove every transition first; this transfer cannot fail.
// Engine.Mu and the publication owner's locks protect both passes.
func (e *Engine) visitRelationGrantsLocked(scope *StatementScope, changes *session.Journal, selectNext func(ids.ObjectRef, catalog.Descriptor, *storage.Relation) *catalog.Relation) {
	advance := func(m *StatementMutation) {
		if next := selectNext(m.LockedRef, m.LockedDescriptor, m.LockedStorage); next != nil {
			m.LockedDescriptor = next
			if m.LockedStorage != nil {
				m.LockedStorage = e.PrivateStorageLocked(changes, m.LockedRef.Address)
			}
		}
	}
	advance(&scope.StatementMutation)
	for i := range scope.mutations {
		advance(&scope.mutations[i])
	}
	for i := range scope.Inheritance.relations {
		source := &scope.Inheritance.relations[i]
		if next := selectNext(source.Ref, source.Relation, source.Storage); next != nil {
			source.Relation = next
			if source.Storage != nil {
				source.Storage = e.PrivateStorageLocked(changes, source.Ref.Address)
			}
		}
	}
	// Topology owns independent captures, not new grants to discover here.
	for address, captured := range scope.PartitionRelations {
		if next := selectNext(e.Catalog.Domain.Ref(scope.TxRef.Epoch(), address), captured, nil); next != nil {
			scope.PartitionRelations[address] = next
		}
	}
}

// Ordinary stages may move only their exact base predecessors to their own
// finalized successors. ALTER's structural transfers retain their established
// target-specific advancement after TransferAlterStage.
func (e *Engine) validateCatalogStageGrantsLocked(scope *StatementScope, stage *CatalogStage, source, installed catalog.Read) error {
	if scope == nil || stage.Lifecycle != catalogValidated {
		return nil
	}
	var err error
	e.visitRelationGrantsLocked(scope, nil, func(ref ids.ObjectRef, captured catalog.Descriptor, store *storage.Relation) *catalog.Relation {
		previous, replaced := catalogStageRelationPredecessor(stage, ref.Address)
		if !replaced {
			return nil
		}
		if ref.Domain() != e.Catalog.Domain || ref.Domain() != stage.Base.Domain() || ref.Epoch != stage.Base.Epoch() || ref.Epoch != source.Epoch() || ref.Epoch != installed.Epoch() || ref.Epoch != e.Catalog.State.Epoch() {
			err = ids.ErrCatalogReplaced
			return nil
		}
		next, ok := installed.Relation(ref.Address)
		if captured != previous || source.Get(ref.Address) != previous || !ok || next == previous || next.Address() != previous.Address() {
			err = catalog.Conflict{}
		}
		if store != nil && store != e.PrivateStorageLocked(scope.PendingCatalog(), ref.Address) {
			err = catalog.Conflict{}
		}
		return nil
	})
	return err
}

func catalogStageRelationPredecessor(stage *CatalogStage, address ids.ObjectAddress) (*catalog.Relation, bool) {
	next, ok := stage.Get(address).(*catalog.Relation)
	if !ok {
		return nil, false
	}
	previous, ok := stage.Base.Relation(address)
	if !ok || previous == next {
		return nil, false
	}
	if _, removed := stage.Removal(address); removed {
		return nil, false
	}
	return previous, true
}

func (e *Engine) commitCatalogStageGrantsLocked(scope *StatementScope, stage *CatalogStage, installed catalog.Read, changes *session.Journal) {
	if scope == nil || stage.Lifecycle != catalogValidated {
		return
	}
	e.visitRelationGrantsLocked(scope, changes, func(ref ids.ObjectRef, captured catalog.Descriptor, store *storage.Relation) *catalog.Relation {
		if _, replaced := catalogStageRelationPredecessor(stage, ref.Address); replaced {
			return installed.MustRelation(ref.Address)
		}
		return nil
	})
}
