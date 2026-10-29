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
	advance := func(m *StatementMutation) error {
		if !targets[m.LockedRef.Address] {
			return nil
		}
		next, ok := current.Relation(m.LockedRef.Address)
		if !ok {
			return catalog.Conflict{}
		}
		m.LockedDescriptor = next
		if m.LockedStorage != nil {
			m.LockedStorage = e.PrivateStorageLocked(scope.PendingCatalog(), m.LockedRef.Address)
		}
		return nil
	}
	if err := advance(&scope.StatementMutation); err != nil {
		return err
	}
	for i := range scope.mutations {
		if err := advance(&scope.mutations[i]); err != nil {
			return err
		}
	}
	for i := range scope.Inheritance.relations {
		source := &scope.Inheritance.relations[i]
		if !targets[source.Ref.Address] {
			continue
		}
		next, ok := current.Relation(source.Ref.Address)
		if !ok {
			return catalog.Conflict{}
		}
		source.Relation = next
		if source.Storage != nil {
			source.Storage = e.PrivateStorageLocked(scope.PendingCatalog(), source.Ref.Address)
		}
	}
	// The topology admission retains its own descriptors independently of
	// mutation grants. Advance only existing captures touched by this ALTER;
	// unrelated hierarchy inputs keep their original append-only proof.
	for address := range scope.PartitionRelations {
		if !targets[address] {
			continue
		}
		next, ok := current.Relation(address)
		if !ok {
			return catalog.Conflict{}
		}
		scope.PartitionRelations[address] = next
	}
	return nil
}
