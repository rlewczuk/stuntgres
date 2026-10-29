package core

import (
	"context"
	"fmt"
	"math"
	"slices"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// DomainOf returns the engine's domain, or nil for a nil engine, so a
// provenance comparison against a possibly-nil engine keeps its meaning.
func DomainOf(e *Engine) *ids.Domain {
	if e == nil {
		return nil
	}
	return e.Catalog.Domain
}

// RelationProvider validates relation descriptors; the root supplies the
// validators of the packages above table, built once.
var RelationProvider = table.RelationProvider{Validators: table.RelationValidators{
	Policies:   view.ValidatePolicies,
	Partition:  partition.ValidateDescriptor,
	Definition: view.ValidateViewDefinition,
}}

type PublicationToken struct{ Epoch ids.CatalogEpoch }
type ChangeSet struct {
	Definitions []ids.ObjectAddress
	Statistics  []ids.ObjectAddress
	Namespaces  []ids.ObjectAddress
	Replacement bool
}

// catalogStageChanges lists a stage's changed definitions in address order and
// its namespace changes.
func catalogStageChanges(s *CatalogStage) ChangeSet {
	changes := ChangeSet{Definitions: s.ChangedAddresses()}
	for _, a := range changes.Definitions {
		if a.ClassID == ids.NamespaceClassID {
			changes.Namespaces = append(changes.Namespaces, a)
		}
	}
	return changes
}

type catalogLifecycle uint8

const (
	CatalogOpen catalogLifecycle = iota
	catalogValidated
	catalogPublished
	catalogDiscarded
)

// CatalogStage is a private catalog change: the pure overlay it embeds plus
// the publication coordination the engine owns (storage row shapes, index
// candidates, domain validation, materialized replacement, role candidates).
type CatalogStage struct {
	Owner *Engine
	catalog.Candidate
	RowShapes    map[ids.ObjectAddress]storage.RowShape
	StorageReads []session.StorageReadFence
	// Metadata of these ALTER work items precedes their separately owned heap
	// and index work. This consumed stage must not build roots from old tuples.
	deferredAlterRows map[ids.ObjectAddress]bool
	// Fresh physical successors have a new catalog base but still read tuples
	// with the genuinely captured pre-ALTER physical column layout.
	AlterRowSources map[ids.ObjectAddress]*catalog.Relation
	Sequence        *sequence.Stage
	Typed           *typedef.TypedStage
	Lifecycle       catalogLifecycle
	Invalid         error
	AuthSession     *session.Session
	statement       *StatementScope
	AuthRevision    uint64
	RoleRevision    uint64
	// Selected lifecycle stages retain their original admission only until the
	// owning statement finishes, never in the completed transaction journal.
	FinishAuthority *session.CatalogAuthority
	DropRoles       authorization.Read // Immutable identity input, never publication authority.
	// A declaration callback has external effects and cannot be replayed by
	// definition-lock admission if its captured authority becomes stale.
	RoutineSettingNormalized bool
	ExtensionSlot            any // Opaque; owned by the extension host that began the stage.
	TriggerReplacement       view.TriggerReplacement
	CreationName             parser.ObjectName // Original destination position across namespace waits.
	namesAdmitted            bool
	DatabasePredecessor      catalog.Descriptor
	DatabaseAuthority        catalog.Descriptor
	// Optional DROP OWNED membership deletions join this publication, not a
	// separate role installation. Every other catalog caller leaves it nil.
	RoleCandidateRecord *access.RoleRecord
	SchemaCascadeNames  []string
	DropCascadeNames    []string
	DependencyPath      []ids.ObjectAddress
	DomainTargets       []ids.ObjectAddress
	DomainBases         []catalog.Descriptor
	domainAlter         *typedef.DomainAlterStage
	// Physical retirement alone may saturate dependent descriptor/store/index
	// counters; fresh identities and exact predecessor fences remain mandatory.
	terminalCleanup bool
	// New query-derived tables carry private initial storage, never replacements
	// of live storage. Publication installs the definition and data together.
	initialTables     map[ids.ObjectAddress]*storage.Relation
	InitialTx         ids.TxRef
	QuerySources      []catalog.Descriptor
	definitionSources []catalog.Descriptor // Checked routine/operator definition inputs.
	// Phased ALTER's final stage validates these exact, already privately
	// installed descriptors without restaging them or minting another revision.
	alterFinal       []catalog.Descriptor
	TableSlot        *table.Stage
	indexesSlot      *index.Stage
	MaterializedSlot *view.MaterializedStage
	DDLCommands      []sqltype.DDLCommand
}

// RecordDDLCommand retains explicit successful producer annotations until the
// catalog stage is admitted. Incidental descriptor changes never call this.
func (s *CatalogStage) RecordDDLCommand(tag string, address ids.ObjectAddress) {
	if s.statement == nil || s.statement.EventCommand == nil || !s.statement.EventCommand.CollectDDL {
		return
	}
	s.DDLCommands = append(s.DDLCommands, sqltype.DDLCommand{Tag: tag, ClassID: uint32(address.ClassID), ObjectID: uint32(address.ObjectID), SubID: address.SubID})
}

func (s *CatalogStage) CollectsDDLCommands() bool {
	return s.statement != nil && s.statement.EventCommand != nil && s.statement.EventCommand.CollectDDL
}

func (s *CatalogStage) RecordDDLPrivileges(tag, kind string) {
	if !s.CollectsDDLCommands() {
		return
	}
	s.DDLCommands = append(s.DDLCommands, sqltype.DDLCommand{Tag: tag, SpecialKind: kind})
}

// AdmitDDLNoop is for an already successful utility that returns no catalog
// stage. It records the resolved address without manufacturing a mutation.
func (s *CatalogStage) AdmitDDLNoop(tag string, address ids.ObjectAddress) {
	if s.CollectsDDLCommands() {
		s.statement.RecordDDLCommand(tag, address)
	}
}

// Only terminal dependency removal can saturate. Other DDL retains its ordinary
// overflow boundary; terminal replacements still have fresh identity.
func (s *CatalogStage) cascadeMetadataRevision(revision ids.DefinitionRevision) (ids.DefinitionRevision, error) {
	if revision == ids.DefinitionRevision(^uint64(0)) {
		if s.terminalCleanup {
			return revision, nil
		}
		return 0, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	return revision + 1, nil
}

type PreparedCatalogPublication struct {
	stage *CatalogStage
	epoch ids.CatalogEpoch
	ctx   context.Context
}

func (e *Engine) BeginCatalogStage(base catalog.Read) *CatalogStage {
	s := &CatalogStage{Owner: e, Candidate: catalog.NewCandidate(base)}
	if base.Owner() != &e.Catalog.Owner || base.State() == nil {
		s.Invalid = fmt.Errorf("stuntgres: foreign catalog view")
	}
	return s
}

// Retain selected private authority separately from the committed predecessor
// already captured by authorizationLocked; building a stage needs no live read.
func (s *CatalogStage) AuthorizeLocked(auth AuthorizationView, session *session.Session) {
	s.AuthSession, s.AuthRevision, s.RoleRevision = session, auth.Revision, auth.RoleRecord.Revision
	s.statement = auth.Statement
	s.DropRoles = auth.Roles
	s.DatabaseAuthority = auth.Catalog.Get(auth.Owner.DatabaseAddress())
	s.DatabasePredecessor = auth.DatabasePredecessor
}

func (s *CatalogStage) Create(d catalog.Descriptor) error {
	if s == nil || s.Lifecycle != CatalogOpen || s.Invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	return s.Candidate.Create(d)
}
func (s *CatalogStage) Replace(d catalog.Descriptor) error {
	return s.ReplaceDescriptor(d, true, false)
}

// A fully rebuilt immutable type closure is already reciprocal. Installing its
// members must not synthesize a second projection based on map iteration order.
func (s *CatalogStage) ReplaceDescriptor(d catalog.Descriptor, synchronize, preserveDomainPlan bool) error {
	if s == nil || s.Lifecycle != CatalogOpen || s.Invalid != nil || d == nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	previous := s.Get(d.Address())
	if previous == nil {
		previous = s.Base.Get(d.Address())
	}
	d = catalog.NormalizeTypeReplacement(previous, d, preserveDomainPlan)
	if err := s.Candidate.Replace(d); err != nil {
		return err
	}
	if relation, ok := d.(*catalog.Relation); ok && synchronize {
		return s.synchronizeRowType(relation)
	}
	return nil
}

// AlterRelation couples a same-identity revision with an immutable storage root.
func (s *CatalogStage) AlterRelation(ctx context.Context, ref ids.ObjectRef, d *catalog.Relation, original, replacement *storage.Relation, observedLength int, observedGeneration uint64, observedEnds *storage.DeletionRoot) error {
	if s == nil || s.Owner == nil || s.Lifecycle != CatalogOpen || s.Invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	e := s.Owner
	e.Mu.Lock()
	if err := e.Catalog.ValidateObjectRefLocked(s.Base, ref); err != nil {
		e.Mu.Unlock()
		return err
	}
	old, ok := s.Base.Relation(ref.Address)
	validRevision := ok && d != nil && old.Revision() != ids.DefinitionRevision(^uint64(0)) && d.Revision() == old.Revision()+1
	if s.terminalCleanup && ok && old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		validRevision = d != nil && d != old && d.Revision() == old.Revision()
	}
	if !ok || ref.Address.SubID != 0 || d == nil || d.Address() != old.Address() || d.Namespace() != old.Namespace() || d.Name() != old.Name() || !validRevision || original == nil || replacement == nil || e.StageStorageLocked(s, ref.Address) != original {
		e.Mu.Unlock()
		return catalog.Conflict{}
	}
	if len(original.Versions) != observedLength || len(replacement.Versions) != observedLength || original.Generation != observedGeneration || original.Ends != observedEnds || replacement.Ends != observedEnds {
		e.Mu.Unlock()
		return catalog.Conflict{}
	}
	if original == replacement && !slices.Equal(d.Attributes, old.Attributes) {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: invalid metadata-only row shape")
	}
	beforeRows := original.Versions
	e.Mu.Unlock()
	if original != replacement {
		for i, row := range replacement.Versions {
			if err := ctx.Err(); err != nil {
				return err
			}
			before := beforeRows[i]
			if len(row.Values) != len(d.Columns) || row.Row != before.Row || row.Version != before.Version || row.Born != before.Born {
				return fmt.Errorf("stuntgres: invalid altered row identity")
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.Replace(d); err != nil {
		return err
	}
	if s.RowShapes == nil {
		s.RowShapes = make(map[ids.ObjectAddress]storage.RowShape)
	}
	s.RowShapes[ref.Address] = storage.RowShape{Original: original, Root: beforeRows, Replacement: replacement, ObservedLength: observedLength, ObservedEnds: observedEnds, Generation: observedGeneration}
	return nil
}

// Remove stages a whole user relation with its coherent observed identity.
func (s *CatalogStage) Remove(ref ids.ObjectRef) error {
	return s.removeObject(ref, false)
}

// Schema closure uses the same removal authority for namespaces and relations.
func (s *CatalogStage) removeObject(ref ids.ObjectRef, namespace bool) error {
	if s == nil || s.Lifecycle != CatalogOpen || s.Invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	s.Owner.Mu.Lock()
	defer s.Owner.Mu.Unlock()
	return s.RemoveObjectLocked(ref, namespace)
}

// RemoveObjectLocked requires the owning engine mutex, including validation.
func (s *CatalogStage) RemoveObjectLocked(ref ids.ObjectRef, namespace bool) error {
	if s == nil || s.Lifecycle != CatalogOpen || s.Invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	return StageDropProvider(context.Background(), s, nil, nil, nil).removeObjectLocked(ref, namespace)
}

// removeObjectLocked is the one removal analysis for execution and immutable
// DROP preparation. The effect sink owns bookkeeping, never the discovery.
// The caller holds the engine mutex.
func (provider *DropProvider) removeObjectLocked(ref ids.ObjectRef, namespace bool) error {
	if ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: whole object required")
	}
	if err := provider.Owner.Catalog.ValidateObjectRefLocked(provider.Base, ref); err != nil {
		return err
	}
	if err := table.CheckRemovalLocked(provider.Base, provider.SearchPath, ref, namespace); err != nil {
		return err
	}
	// Provider/ref validation above belongs to the authentic predecessor.
	// AUTO retirement below follows the actual metadata entering this phase.
	d := provider.RetirementDescriptor(ref.Address)
	if provider.Removed(ref.Address) {
		return nil
	}
	if err := provider.RecordRemoval(ref, namespace); err != nil {
		return err
	}
	if operator, ok := d.(*catalog.Operator); ok {
		if err := provider.clearOperatorLinks(operator); err != nil {
			return err
		}
	}
	if typ, ok := d.(*catalog.Type); ok && typ.SQLType.IsComposite() {
		if backing, ok := provider.RetirementDescriptor(typ.OwningRelation).(*catalog.Relation); ok && backing.RelationKind == catalog.CompositeRelation {
			if err := provider.removeObjectLocked(provider.Owner.Catalog.Domain.Ref(provider.Base.Epoch(), backing.Address()), false); err != nil {
				return err
			}
		}
	}
	if relation, ok := d.(*catalog.Relation); ok {
		if err := provider.removePartitionHierarchyLocked(relation); err != nil {
			return err
		}
	}
	if r, ok := d.(*catalog.Relation); ok && r.RowType != 0 {
		row, ok := provider.RetirementDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.RowType)}).(*catalog.Type)
		if !ok || row.OwningRelation != r.Address() {
			return catalog.Conflict{}
		}
		provider.removedRowType(row.Address())
		for _, id := range []sqltype.ID{r.RowType, row.Array} {
			if id != 0 {
				if err := provider.removeObjectLocked(provider.Owner.Catalog.Domain.Ref(provider.Base.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}), false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (e *Engine) StageRead(s *CatalogStage) catalog.Read {
	return e.Catalog.Read(s.Candidate.State())
}

// normalizeCatalogStage is shared by ordinary publication and ALTER's private
// structural transfers. Registered providers remain the final publication veto.
func (e *Engine) normalizeCatalogStage(ctx context.Context, s *CatalogStage) error {
	if s == nil || s.Owner != e || s.Lifecycle != CatalogOpen || s.Invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := table.ValidateInitialTables(s.initialTables, s.Get, s.Base); err != nil {
		return err
	}
	if err := s.validateMaterializedReplacement(); err != nil {
		return err
	}
	e.Mu.Lock()
	if err := view.SynchronizeTriggersLocked(&s.Candidate, func() catalog.Read { return e.StageRead(s) }, &e.Catalog, s.TriggerReplacement); err != nil {
		e.Mu.Unlock()
		return err
	}
	if err := s.validateRoleCandidate(e.sessionRolesLocked(s.AuthSession)); err != nil {
		e.Mu.Unlock()
		return err
	}
	// Only a replaced generation can leave consumers on its old descriptor.
	// Added types (a new relation's row and array types) are built complete,
	// so a stage that only adds types skips the whole-catalog reownership.
	for address, descriptor := range s.Descriptors() {
		typ, ok := descriptor.(*catalog.Type)
		if !ok {
			continue
		}
		old, _ := s.Base.Type(address)
		if old != nil && old.SQLType != typ.SQLType {
			if err := e.ReownStagedTypeGenerationsLocked(s); err != nil {
				e.Mu.Unlock()
				return err
			}
			break
		}
	}
	if !e.storageReadsValidLocked(s) {
		e.Mu.Unlock()
		return catalog.Conflict{}
	}
	if err := s.validateIndexMaintenance(e.StageCatalogBaseLocked(s)); err != nil {
		e.Mu.Unlock()
		return err
	}
	if err := e.EvolveStageIndexesLocked(s, ids.ObjectAddress{}, nil); err != nil {
		e.Mu.Unlock()
		return err
	}
	if err := s.MarkIndexHistoryLocked(e.Catalog.RevisionLocked, s.Replace); err != nil {
		e.Mu.Unlock()
		return err
	}
	if err := s.MarkSubclassHistoryLocked(e.Catalog.RevisionLocked, s.Replace); err != nil {
		e.Mu.Unlock()
		return err
	}
	for address, shape := range s.RowShapes {
		if !shape.Matches(e.StageStorageLocked(s, address)) {
			e.Mu.Unlock()
			return catalog.Conflict{}
		}
	}
	if r := s.MaterializedOrZero().Replacement; r != nil {
		if err := s.materializedReplacementCurrent(e.StageCatalogBaseLocked(s), e.StageStorageLocked(s, r.Original.Address())); err != nil {
			e.Mu.Unlock()
			return err
		}
	}
	e.Mu.Unlock()
	return nil
}

func (e *Engine) catalogStageValidationRead(s *CatalogStage) (catalog.Read, error) {
	var read catalog.Read
	if s.DescriptorCount() != 0 || len(s.alterFinal) != 0 {
		read = e.StageRead(s)
		validate := s.Candidate.ValidateStagedDefinitionOIDs
		if len(s.alterFinal) != 0 {
			validate = catalog.ValidateDefinitionOIDs
		}
		if err := validate(read); err != nil {
			return catalog.Read{}, err
		}
	}
	return read, nil
}

func (e *Engine) validateCatalogDescriptor(s *CatalogStage, read catalog.Read, d catalog.Descriptor, p catalog.Provider) error {
	if p == nil {
		return fmt.Errorf("stuntgres: unregistered catalog class %d", d.Address().ClassID)
	}
	if err := catalog.ValidateExtensionMembership(read, d); err != nil {
		return err
	}
	if err := p.Validate(read, d); err != nil {
		return err
	}
	e.Mu.Lock()
	roles := e.sessionRolesLocked(s.AuthSession)
	err := access.ValidateACL(s.roleRead(roles, read), d)
	e.Mu.Unlock()
	return err
}

func (e *Engine) ValidateCatalogStage(ctx context.Context, s *CatalogStage) (*PreparedCatalogPublication, error) {
	if err := e.normalizeCatalogStage(ctx, s); err != nil {
		return nil, err
	}
	read, err := e.catalogStageValidationRead(s)
	if err != nil {
		return nil, err
	}
	for _, d := range s.Descriptors() {
		if err := e.validateCatalogDescriptor(s, read, d, e.Catalog.Provider(d.Address().ClassID)); err != nil {
			return nil, err
		}
	}
	for _, d := range s.alterFinal {
		if read.Get(d.Address()) != d {
			return nil, catalog.Conflict{}
		}
		if err := e.validateCatalogDescriptor(s, read, d, e.Catalog.Provider(d.Address().ClassID)); err != nil {
			return nil, err
		}
	}
	s.Lifecycle = catalogValidated
	return &PreparedCatalogPublication{stage: s, epoch: s.Base.Epoch(), ctx: ctx}, nil
}

func (s *StatementScope) acquireCatalogStageInputs(stage *CatalogStage) error {
	if err := s.acquireDefinitionObjectDependencies(stage); err != nil {
		return err
	}
	if err := s.acquireStageRoleDependencies(stage); err != nil {
		return err
	}
	if err := s.acquireRoutineRetirements(stage); err != nil {
		return err
	}
	if err := s.acquireDefinitionDependencyLocks(stage); err != nil {
		return err
	}
	return nil
}

func (s *StatementScope) acquireCatalogStageAdmissions(stage *CatalogStage) error {
	if err := s.acquireCatalogStageInputs(stage); err != nil {
		return err
	}
	if err := s.acquireCatalogNames(stage); err != nil {
		return err
	}
	return s.AcquireRoutineDefinitionProviders(stage)
}

func (s *StatementScope) releaseCatalogReservation() {
	if s.ReplacementRead {
		s.Engine().ReplacementMu.RUnlock()
		s.ReplacementRead = false
	}
}

// UsesCatalogOverlay reports whether catalog publication belongs to this
// statement's pending journal, including the first write in a routine body.
func (s *StatementScope) UsesCatalogOverlay() bool {
	return s != nil && (s.Owner.State == session.TxActive || s.EventCommand != nil && s.EventCommand.holdsEffects || s.Catalog != nil || s.activeSQLRoutineDepth != 0)
}

func (e *Engine) PublishCatalogOperation(op *session.Operation, scope *StatementScope, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	if p == nil || p.stage == nil || p.stage.Owner != e || p.stage.Lifecycle != catalogValidated {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
	}
	if token.Epoch != p.epoch {
		return ChangeSet{}, ids.ErrCatalogReplaced
	}
	if scope != nil {
		if scope.Op != op || op == nil {
			return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication scope")
		}
		if err := scope.acquireCatalogStageAdmissions(p.stage); err != nil {
			return ChangeSet{}, err
		}
	}
	if op != nil && op.Session.Transactions.RetainsTransaction() && (scope == nil || (scope.EventCommand == nil || !scope.EventCommand.holdsEffects) && scope.Catalog == nil && scope.activeSQLRoutineDepth == 0) {
		return e.stageMessageCatalog(op, p, token)
	}
	if scope.UsesCatalogOverlay() {
		defer scope.releaseCatalogReservation()
		if scope.Op == nil || scope.Op != op || p == nil || p.stage == nil || p.stage.Owner != e || p.stage.Lifecycle != catalogValidated {
			return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
		}
		if token.Epoch != p.epoch {
			return ChangeSet{}, ids.ErrCatalogReplaced
		}
		prior := scope.Catalog
		if prior == nil {
			prior = scope.Owner.Catalog
		}
		if err := e.StageCatalogOverlay(scope, p.stage, prior.Clone()); err != nil {
			return ChangeSet{}, err
		}
		return catalogStageChanges(p.stage), nil
	}
	changes, err := e.PublishPreparedCatalog(op, scope, p, token)
	if err == nil && scope != nil && scope.EventCommand != nil && scope.EventCommand.CollectDDL {
		scope.EventCommand.Commands = append(scope.EventCommand.Commands, p.stage.DDLCommands...)
	}
	return changes, err
}
func (e *Engine) PublishPreparedCatalog(op *session.Operation, scope *StatementScope, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	if p == nil || p.stage == nil || p.stage.Owner != e || p.stage.Lifecycle != catalogValidated {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
	}
	s := p.stage
	changes := catalogStageChanges(s)
	for {
		e.Mu.Lock()
		if s.Lifecycle != catalogValidated {
			e.Mu.Unlock()
			return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
		}
		current := e.Catalog.State
		if err := s.DefaultPreflight(current); err != nil {
			e.Mu.Unlock()
			return ChangeSet{}, err
		}
		currentStorage := e.Storage.Relations
		if p.epoch != current.Epoch() || token.Epoch != current.Epoch() {
			e.Mu.Unlock()
			return ChangeSet{}, ids.ErrCatalogReplaced
		}
		if err := s.validateIndexMaintenance(current); err != nil {
			e.Mu.Unlock()
			return ChangeSet{}, err
		}
		if !e.Storage.IndexRootsCurrent(s.domainAlter.AlteredIndexRoots()) {
			e.Mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		if fence := s.domainAlter.ValidationFence(); fence != nil && !fence.Current(e.Catalog.Read(current), currentStorage) {
			e.Mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		if !table.TypedObservationValid(s.Typed, e.Catalog.Read(current)) {
			e.Mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		if !s.AccessMethodsCurrent(e.Catalog.Read(current)) {
			e.Mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		if !s.DomainBasesCurrent(current, s.DomainBases, s.terminalCleanup) {
			e.Mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		for _, source := range s.definitionSources {
			if current.Get(source.Address()) != source {
				e.Mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
		}
		if err := s.validateLikeSources(e.Catalog.Read(current)); err != nil {
			e.Mu.Unlock()
			return ChangeSet{}, err
		}
		for _, source := range s.QuerySources {
			if !view.MaterializedSourceCurrent(s.MaterializedOrZero().SourceView, source, current, currentStorage[source.Address()]) {
				e.Mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
		}
		if !s.DescriptorsCurrent(current, e.Catalog.Domain) || !extensionMaterializedViewsCurrent(s, current) {
			e.Mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		if !e.storageReadsValidLocked(s) {
			e.Mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		for a, shape := range s.RowShapes {
			if !shape.MatchesWithEnds(currentStorage[a]) {
				e.Mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
		}
		if r := s.MaterializedOrZero().Replacement; r != nil {
			if err := s.materializedReplacementCurrent(current, currentStorage[r.Original.Address()]); err != nil {
				e.Mu.Unlock()
				return ChangeSet{}, err
			}
		}
		stageIndexes := s.IndexesOrZero()
		if !index.CandidatesMatch(stageIndexes.Candidates, &s.Candidate, current, func(parent ids.ObjectAddress) *storage.Relation {
			if shape, changed := s.RowShapes[parent]; changed {
				return shape.Replacement
			}
			if initial := s.initialTables[parent]; initial != nil {
				return initial
			}
			return currentStorage[parent]
		}) {
			e.Mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		indexStores := index.CaptureStagedIndexStores(currentStorage, current, &s.Candidate)
		indexPredecessors := e.Storage.IndexRoots
		var disclosure *AuthorizationView
		if op != nil {
			auth, authErr := e.AuthorizationLocked(op)
			if authErr != nil {
				e.Mu.Unlock()
				return ChangeSet{}, authErr
			}
			disclosure = &auth
		}
		e.Mu.Unlock()
		next := s.ApplyTo(current)
		published := s.storagePublication()
		relations := published.CandidateRelations(currentStorage, &s.Candidate)
		if err := catalogDependencyPreflight(current, next, s.Sequence.IdentityDependencyList(), s.Sequence.IdentityOriginalMap()); err != nil {
			return ChangeSet{}, err
		}
		build := index.PublicationBuildStores(indexStores, published, &s.Candidate, current, relations, indexPredecessors, stageIndexes.Candidates)
		var runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef
		if r := s.MaterializedOrZero().Replacement; r != nil && r.Concurrent {
			runtimeOwners = map[ids.ObjectAddress]authorization.PrincipalRef{r.Original.Address(): r.Original.Owner}
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: p.ctx, Host: HostOf(scope)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		if scope != nil {
			evaluation.Input, evaluation.Output = scope.Input, scope.Output
		} else if op != nil {
			evaluation.Input, evaluation.Output = op.EffectiveSettings().Presentation()
		}
		indexRoots, indexErr := e.prepareIndexCatalog(&evaluation, current, next, build.Build, build.Sources, build.Rebuild, stageIndexes.Rebuilds, stageIndexes.Output, disclosure, runtimeOwners, stageIndexes.Candidates, stageIndexes.Progress)
		if indexErr != nil {
			return ChangeSet{}, indexErr
		}
		if err := validatePublishedCatalog(e.Catalog.Read(next), current, next, s); err != nil {
			return ChangeSet{}, err
		}
		e.Mu.Lock()
		if op != nil {
			op.Session.Mu.Lock()
		}
		if e.Catalog.State != current {
			if op != nil {
				op.Session.Mu.Unlock()
			}
			e.Mu.Unlock()
			continue
		}
		if !index.StoresUnchanged(e.Storage.Relations, indexStores, current, next, indexRoots, indexPredecessors) {
			if op != nil {
				op.Session.Mu.Unlock()
			}
			e.Mu.Unlock()
			continue
		}
		if !e.storageReadsValidLocked(s) {
			if op != nil {
				op.Session.Mu.Unlock()
			}
			e.Mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		for a, shape := range s.RowShapes {
			if !shape.MatchesWithEnds(e.Storage.Relations[a]) {
				if op != nil {
					op.Session.Mu.Unlock()
				}
				e.Mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
		}
		if r := s.MaterializedOrZero().Replacement; r != nil {
			if err := s.materializedReplacementCurrent(e.Catalog.State, e.Storage.Relations[r.Original.Address()]); err != nil {
				if op != nil {
					op.Session.Mu.Unlock()
				}
				e.Mu.Unlock()
				return ChangeSet{}, err
			}
		}
		err := p.ctx.Err()
		if err == nil && !e.Storage.IndexRootsCurrent(s.domainAlter.AlteredIndexRoots()) {
			err = catalog.Conflict{}
		}
		if fence := s.domainAlter.ValidationFence(); err == nil && fence != nil && !fence.Current(e.Catalog.Current(), e.Storage.Relations) {
			err = catalog.Conflict{}
		}
		if err == nil && s.DatabasePredecessor != nil && !catalog.DatabaseAuthorityCompatible(e.Catalog.State.Get(e.Catalog.DatabaseAddress()), s.DatabasePredecessor) {
			err = ids.ErrCatalogReplaced
		}
		if err == nil {
			err = catalogDependencyPreflight(e.Catalog.State, next, s.Sequence.IdentityDependencyList(), s.Sequence.IdentityOriginalMap())
		}
		if err == nil {
			err = s.validateRoleCandidate(e.RoleStore.Published)
		}
		if err == nil && op != nil {
			err = PublicationOperationLocked(e, op)
		}
		if err == nil {
			err = catalog.ValidateDefaultACLs(s.roleRead(e.RoleStore.Published, e.Catalog.Read(next)), e.Catalog.Read(next))
		}
		if err == nil {
			for _, d := range s.Descriptors() {
				if err = access.ValidateACL(s.roleRead(e.RoleStore.Published, e.Catalog.Read(next)), d); err != nil {
					break
				}
			}
		}
		if err == nil && s.AuthSession != nil {
			// SQL stages use the operation's session, already locked above.
			if op == nil || s.AuthSession != op.Session {
				s.AuthSession.Mu.Lock()
			}
			auth := s.AuthSession.Authorization
			valid := auth != nil && auth.Revision == s.AuthRevision && e.RoleStore.Published.Revision == s.RoleRevision
			if op == nil || s.AuthSession != op.Session {
				s.AuthSession.Mu.Unlock()
			}
			if !valid {
				err = fmt.Errorf("stuntgres: authorization changed before publication")
			}
		}
		if err == nil && s.MaterializedOrZero().Replacement != nil && s.AuthSession != nil {
			if op == nil || s.AuthSession != op.Session {
				s.AuthSession.Mu.Lock()
			}
			auth := e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: e.Catalog.Current(), RoleRecord: e.RoleStore.Published, Catalog: e.Catalog.Current(), Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current()), State: s.AuthSession.Authorization.State})
			err = auth.Check(access.Requirement{Object: e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), s.MaterializedOrZero().Replacement.Original.Address()), Privileges: authorization.MaintainPrivilege})
			if op == nil || s.AuthSession != op.Session {
				s.AuthSession.Mu.Unlock()
			}
		}
		if err == nil {
			err = p.ctx.Err()
		}
		if err == nil {
			if s.NamespaceChanged() {
				err = next.AdvanceNamespaceGeneration()
			}
		}
		if err == nil && (len(s.initialTables) != 0 || s.MaterializedOrZero().Replacement != nil) && e.Visibility.CommitSeq() == ids.CommitSeq(math.MaxUint64) {
			err = fmt.Errorf("stuntgres: commit sequence exhausted")
		}
		if err == nil && s.InitialTx != (ids.TxRef{}) {
			err = e.Visibility.SerializablePrecommitLocked(s.InitialTx)
		}
		commitRef := s.InitialTx
		if scope != nil {
			commitRef = scope.TxRef
		}
		if err == nil && commitRef != (ids.TxRef{}) {
			err = e.AssignTransactionXIDLocked(commitRef)
		}
		var roleRevision uint64
		if err == nil && s.RoleCandidateRecord != nil {
			roleRevision, err = e.RoleStore.RevisionCandidateLocked()
		}
		var sharedRoots catalog.SharedProjection
		if err == nil {
			sharedRoots = e.Catalog.ProjectSharedLocked(next, e.Cluster.Catalogs)
		}
		if err == nil {
			relationMetadata := e.prepareRelationMetadataLocked(next, nil)
			// Staged replacements may advance revisions without reserving from
			// the allocator. Commit its frontier with the successful publication.
			nextRevision := catalog.RevisionFrontier(e.Catalog.NextRevision(), s.Descriptors())
			if len(s.initialTables) != 0 || s.MaterializedOrZero().Replacement != nil {
				frontier, fullXID := e.Visibility.CommitSeq()+1, e.Visibility.FullXIDLocked(commitRef)
				for _, initial := range s.initialTables {
					initial.StampPublishedBirth(frontier, fullXID)
					initial.Generation = 1
					e.Visibility.InitializeMaintenanceLocked(initial)
					initial.CountPublishedInserts()
				}
				if replacement := s.MaterializedOrZero().Replacement; replacement != nil {
					// Refresh retains this object's cumulative facts, captured from
					// the validated current predecessor only inside publication.
					replacement.Storage.Maintenance = replacement.Predecessor.Maintenance
					replacement.Storage.Maintenance.FrozenXID = 0
					replacement.Storage.Maintenance.Live = 0
					replacement.Storage.Maintenance.Dead = 0
					replacement.Storage.StampPublishedBirth(frontier, fullXID)
					if replacement.Concurrent {
						lineage := replacement.Predecessor.MaterializedLineage
						if lineage == nil {
							lineage = &storage.MaterializedLineage{}
						}
						replacement.Predecessor.MaterializedLineage = lineage
						replacement.Storage.MaterializedLineage = lineage
					}
					e.Visibility.InitializeMaintenanceLocked(replacement.Storage)
					replacement.Storage.CountPublishedInserts()
				}
				e.Visibility.AdvanceCommitSeqLocked()
			}
			for address, descriptor := range s.Descriptors() {
				if relation, ok := descriptor.(*catalog.Relation); ok && relation.RelationKind.Stored() {
					e.Visibility.InitializeMaintenanceLocked(relations[address])
				}
			}
			if s.InitialTx != (ids.TxRef{}) {
				e.Visibility.SerializableCommittedLocked(s.InitialTx, e.Visibility.CommitSeq())
			}
			e.Catalog.PublishLocked(next)
			sharedRoots.InstallLocked()
			e.Storage.ReplaceRelationsLocked(relations)
			e.Storage.RelationMetadata = relationMetadata
			index.PreserveUnchangedIndexRoots(indexRoots, indexPredecessors, e.Storage.IndexRoots)
			e.Storage.ReplaceIndexRootsLocked(indexRoots)
			for a := range s.Removals() {
				e.Storage.RemoveSequenceLocked(a)
			}
			for a, generator := range s.Sequence.StagedGenerators() {
				e.Storage.InstallSequenceLocked(a, generator)
			}
			e.Catalog.InstallRevisionFrontierLocked(nextRevision)
			if s.RoleCandidateRecord != nil {
				s.RoleCandidateRecord.Revision = roleRevision
				e.RoleStore.PublishLocked(s.RoleCandidateRecord)
				e.RoleStore.InstallRevisionFrontierLocked(roleRevision + 1)
			}
			if commitRef != (ids.TxRef{}) {
				e.Visibility.RecordCommitTimestampLocked(commitRef)
			}
			s.Lifecycle = catalogPublished
		}
		if op != nil {
			op.Session.Mu.Unlock()
		}
		e.Mu.Unlock()
		if err != nil {
			return ChangeSet{}, err
		}
		return changes, nil
	}
}

// extensionMaterializedViewsCurrent rechecks every removed extension at the
// final publication fence. A newly attached MV was not in the validated
// retirement closure, so its live immutable x authority must still be absent.
// The root calls it under Engine.mu, right after the candidate's descriptor
// fences; catalog cannot read materialized-view payloads.
func extensionMaterializedViewsCurrent(s *CatalogStage, current *catalog.State) bool {
	for a := range s.Removals() {
		if a.ClassID != ids.ExtensionClassID {
			continue
		}
		for address, descriptor := range current.Descriptors() {
			if descriptor == s.Base.Get(address) {
				continue
			}
			if relation, ok := descriptor.(*catalog.Relation); ok && view.MaterializedExtensionDepends(relation, a) {
				return false
			}
		}
	}
	return true
}

func (e *Engine) DiscardCatalogStage(s *CatalogStage) {
	if s != nil && s.Owner == e && s.Lifecycle != catalogPublished {
		s.Lifecycle = catalogDiscarded
	}
}

// ReplaceCatalog installs replacement, a catalog view that source owns, with
// source's committed rows, sequences and roles. source may be e.
func (e *Engine) ReplaceCatalog(ctx context.Context, source *Engine, replacement catalog.Read) (ChangeSet, error) {
	return e.ReplaceCatalogInOperation(ctx, source, replacement, nil)
}

// Restore retains its genuine private operation through owner-cell validation.
func (e *Engine) ReplaceCatalogInOperation(ctx context.Context, source *Engine, replacement catalog.Read, restoreOp *session.Operation) (ChangeSet, error) {
	var installed ids.CatalogEpoch
	defer func() {
		if installed != 0 {
			e.Locks.Fence(e.Catalog.Domain, installed)
		}
	}()
	if replacement.State() == nil || replacement.Owner() == nil || source == nil || replacement.Owner() != &source.Catalog.Owner {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid replacement catalog")
	}
	if err := ctx.Err(); err != nil {
		return ChangeSet{}, err
	}
	e.Mu.Lock()
	parameterPredecessor := e.Catalog.State
	e.Mu.Unlock()
	if source.ImportParameterPredecessor != nil {
		parameterPredecessor = source.ImportParameterPredecessor
	}
	var err error
	replacement, err = control.PersistentReplacementCatalog(replacement)
	if err != nil {
		return ChangeSet{}, err
	}
	if err := catalog.NormalizeReplacementIndexHistory(ctx, replacement); err != nil {
		return ChangeSet{}, err
	}
	if err := catalog.NormalizeReplacementSubclassHistory(ctx, replacement); err != nil {
		return ChangeSet{}, err
	}
	targetRoles := e.Roles()
	if err := catalog.ValidateDefinitionOIDs(replacement); err != nil {
		return ChangeSet{}, err
	}
	for _, d := range replacement.Descriptors() {
		p := e.Catalog.Provider(d.Address().ClassID)
		if p == nil {
			return ChangeSet{}, fmt.Errorf("stuntgres: unregistered catalog class %d", d.Address().ClassID)
		}
		if err := catalog.ValidateExtensionMembership(replacement, d); err != nil {
			return ChangeSet{}, err
		}
		if err := catalog.Validate(p, replacement, d); err != nil {
			return ChangeSet{}, err
		}
	}
	var next *catalog.State
	relationAddresses := make([]ids.ObjectAddress, 0, replacement.DescriptorCount())
	hasHierarchy := false
	for a, d := range replacement.Descriptors() {
		if relation, ok := d.(*catalog.Relation); ok && catalog.HierarchyRelation(relation) {
			hasHierarchy = true
		}
		if r, ok := d.(*catalog.Relation); ok && r.RelationKind.Stored() {
			relationAddresses = append(relationAddresses, a)
		}
	}
	relations := make(map[ids.ObjectAddress]*storage.Relation, len(relationAddresses))
	var sequences map[ids.ObjectAddress]*storage.SequenceGenerator
	var indexes *index.Snapshot
	copyRows := func(owner *Engine) error {
		source, frontier := owner.Storage.Relations, owner.Visibility.CommitSeq()
		for _, a := range relationAddresses {
			if source[a] == nil {
				return fmt.Errorf("stuntgres: relation storage is missing")
			}
			relation, ok := owner.Catalog.Current().Relation(a)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			heap, err := visibility.ResolveHeapAccess(owner.Catalog.Current(), relation.TableMethod)
			if err != nil {
				return err
			}
			selected, err := exec.SelectQuerySource(ctx, &exec.Source{Committed: source[a].Versions}, frontier, source[a].Ends, nil, heap)
			if err != nil {
				return err
			}
			versions := make([]storage.RowVersion, selected.Count())
			for i := range versions {
				versions[i] = selected.VersionAt(selected.RawIndex(i))
			}
			selected.Close()
			relations[a] = &storage.Relation{Versions: versions}
		}
		var err error
		indexes, err = owner.captureIndexSnapshot(ctx, replacement, source, owner.Storage.IndexRoots, frontier)
		return err
	}
	if replacement.Owner() == &e.Catalog.Owner {
		e.ReplacementMu.Lock()
		defer e.ReplacementMu.Unlock()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		oids := e.Catalog.OIDReservationLocked()
		if e.prepared.Len() != 0 {
			return ChangeSet{}, fmt.Errorf("stuntgres: cannot replace catalog while prepared transactions exist")
		}
		if err := ctx.Err(); err != nil {
			return ChangeSet{}, err
		}
		if err := e.checkTemporaryRelationSourcesReplacementLocked(); err != nil {
			return ChangeSet{}, err
		}
		if replacement.Epoch() != e.Catalog.State.Epoch() {
			return ChangeSet{}, ids.ErrCatalogReplaced
		}
		next = catalog.CloneState(replacement.State())
		if err := copyRows(e); err != nil {
			return ChangeSet{}, err
		}
		var sequenceErr error
		sequences, sequenceErr = sequence.CopyGenerators(replacement, e.Storage.Sequences)
		if sequenceErr != nil {
			return ChangeSet{}, sequenceErr
		}
		if err := e.preserveTemporaryLocked(ctx, next, relations, sequences, restoreOp, indexes, nil, &oids); err != nil {
			return ChangeSet{}, err
		}
		nextRevision, err := e.replacementACLsLocked(next)
		if err != nil {
			return ChangeSet{}, err
		}
		if err := e.Storage.ReplacementCapacityLocked(relations, e.Catalog.State.Epoch(), e.Visibility.CommitSeq()); err != nil {
			return ChangeSet{}, err
		}
		if hasHierarchy {
			candidate := e.Catalog.Read(next)
			for _, descriptor := range next.Descriptors() {
				if relation, ok := descriptor.(*catalog.Relation); ok && catalog.HierarchyRelation(relation) {
					if err := partition.ValidateDescriptor(candidate, relation); err != nil {
						return ChangeSet{}, err
					}
				}
			}
		}
		roots, nextRow, nextVersion, err := e.prepareRemintedStorage(ctx, next, relations, indexes, true)
		if err != nil {
			return ChangeSet{}, err
		}
		metadata, err := e.prepareRetainedRelationMetadataLocked(ctx, next, relations)
		if err != nil {
			return ChangeSet{}, err
		}
		if err := ctx.Err(); err != nil {
			return ChangeSet{}, err
		}
		if !e.Catalog.State.ParametersUnchanged(parameterPredecessor) || e.Catalog.NextOID() != oids.Base || !e.sharedOIDReservationCurrentLocked(oids) {
			return ChangeSet{}, ids.ErrCatalogReplaced
		}
		e.installReplacementLocked(next, relations, sequences, metadata, roots, nextRow, nextVersion, nextRevision, oids.Next)
		installed = next.Epoch()
		return ChangeSet{Replacement: true}, nil
	}
	source.Mu.Lock()
	if replacement.Epoch() != source.Catalog.State.Epoch() {
		source.Mu.Unlock()
		return ChangeSet{}, ids.ErrCatalogReplaced
	}
	err = copyRows(source)
	if err == nil {
		sequences, err = sequence.CopyGenerators(replacement, source.Storage.Sequences)
	}
	sourceRoles := source.RoleStore.PublishedRead(&source.Catalog.Owner, replacement)
	sourceForeign := source.Foreign
	extendedStatistics, captureErr := storage.CaptureExtendedStatisticsSnapshots(ctx, replacement, source.Storage.RelationMetadata, StatisticsSnapshotCodec(ctx, source, replacement))
	source.Mu.Unlock()
	if err != nil {
		return ChangeSet{}, err
	}
	if captureErr != nil {
		return ChangeSet{}, captureErr
	}
	e.ReplacementMu.Lock()
	defer e.ReplacementMu.Unlock()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	oids := e.Catalog.OIDReservationLocked()
	if e.prepared.Len() != 0 {
		return ChangeSet{}, fmt.Errorf("stuntgres: cannot replace catalog while prepared transactions exist")
	}
	if err := ctx.Err(); err != nil {
		return ChangeSet{}, err
	}
	if err := e.checkTemporaryRelationSourcesReplacementLocked(); err != nil {
		return ChangeSet{}, err
	}
	if err := e.Storage.ReplacementCapacityLocked(relations, e.Catalog.State.Epoch(), e.Visibility.CommitSeq()); err != nil {
		return ChangeSet{}, err
	}
	replacement.State().SetACLItemImport(e.RoleStore.ACLItemImporter(source.RoleStore, &e.Catalog, sourceRoles, targetRoles, source.ImportTargets))
	defer func() { replacement.State().SetACLItemImport(nil) }()
	var procedure catalog.ProcedureRemapper
	next, procedure, err = e.remapPreparedCatalog(replacement.State(), sourceRoles, targetRoles, source.ImportTargets)
	if err != nil {
		return ChangeSet{}, err
	}
	if err = e.preserveTemporaryLocked(ctx, next, relations, sequences, restoreOp, indexes, procedure, &oids); err != nil {
		return ChangeSet{}, err
	}
	nextRevision, err := e.replacementACLsLocked(next)
	if err != nil {
		return ChangeSet{}, err
	}
	prepared, err := e.prepareForeignIndexStorage(ctx, next, relations, indexes)
	if err != nil {
		return ChangeSet{}, err
	}
	if !e.Catalog.State.ParametersUnchanged(parameterPredecessor) {
		return ChangeSet{}, ids.ErrCatalogReplaced
	}
	if err := e.installForeignCatalogLocked(ctx, next, relations, sequences, nextRevision, prepared, oids, extendedStatistics); err != nil {
		return ChangeSet{}, err
	}
	e.Foreign.AdoptNamespaces(sourceForeign)
	installed = next.Epoch()
	return ChangeSet{Replacement: true}, nil
}

// installForeignCatalogLocked owns the final cancellation boundary and the
// indivisible installation of a prepared foreign generation. The caller holds
// replacementMu and mu and has validated capacity, descriptors and authority.
// Storage vectors and sequence generator scalars are private copies; payload
// values remain immutable. All prepared containers publish at the same boundary.
func (e *Engine) installForeignCatalogLocked(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, nextRevision ids.DefinitionRevision, prepared index.RemintedIndexStorage, oids catalog.OIDReservation, extendedStatistics []storage.ExtendedStatisticsSnapshot) error {
	if prepared.Catalog != next || prepared.BaseCatalog != e.Catalog.State || prepared.BaseRow != e.Storage.RowFrontier() || prepared.BaseVersion != e.Storage.VersionFrontier() || prepared.Frontier != e.Visibility.CommitSeq() || e.Catalog.NextOID() != oids.Base || !e.sharedOIDReservationCurrentLocked(oids) {
		return ids.ErrCatalogReplaced
	}
	metadata, err := e.prepareRetainedRelationMetadataLocked(ctx, next, relations)
	if err != nil {
		return err
	}
	read := e.Catalog.Read(next)
	imported, err := storage.RestoreExtendedStatisticsSnapshots(ctx, read, extendedStatistics, StatisticsSnapshotCodec(ctx, e, read))
	if err != nil {
		return err
	}
	if len(imported) != 0 {
		if metadata == nil {
			metadata = make(map[ids.ObjectAddress]*storage.RelationMetadata, len(imported))
		}
		for address, entry := range imported {
			metadata[address] = entry
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if prepared.BaseCatalog != e.Catalog.State || e.Catalog.NextOID() != oids.Base || !e.sharedOIDReservationCurrentLocked(oids) {
		return ids.ErrCatalogReplaced
	}
	e.installReplacementLocked(next, relations, sequences, metadata, prepared.Roots, prepared.NextRow, prepared.NextVersion, nextRevision, oids.Next)
	return nil
}

// replacementACLsLocked validates the default ACLs and every descriptor ACL of
// a replacement catalog and returns the revision frontier it needs.
func (e *Engine) replacementACLsLocked(next *catalog.State) (ids.DefinitionRevision, error) {
	if err := e.RoleStore.ValidateReplacementACLsLocked(&e.Catalog.Owner, e.Catalog.Read(next), next); err != nil {
		return 0, err
	}
	return catalog.RevisionFrontier(e.Catalog.NextRevision(), next.Descriptors()), nil
}

// installReplacementLocked installs a validated replacement generation: its
// catalog, rows, index roots and sequences publish at one boundary, and the
// reserved OID, row, version, commit and serializable frontiers publish together.
// The caller holds replacementMu and mu.
func (e *Engine) installReplacementLocked(next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, metadata map[ids.ObjectAddress]*storage.RelationMetadata, roots map[ids.ObjectAddress]*storage.IndexRoot, nextRow, nextVersion uint64, nextRevision ids.DefinitionRevision, nextOID ids.OID) {
	sharedRoots := e.Catalog.ProjectSharedLocked(next, e.Cluster.Catalogs)
	e.Storage.SetFrontierLocked(nextRow, nextVersion)
	e.Storage.ReplaceIndexRootsLocked(roots)
	e.Visibility.AdvanceCommitSeqLocked()
	next.SetEpoch(e.Catalog.State.Epoch() + 1)
	e.Catalog.PublishLocked(next)
	sharedRoots.InstallLocked()
	e.Catalog.InstallOIDFrontierLocked(nextOID)
	for store := range e.Cluster.Catalogs {
		store.InstallOIDFrontierLocked(max(store.NextOID(), nextOID))
	}
	e.Storage.ReplaceRelationsLocked(relations)
	e.Storage.RelationMetadata = metadata
	e.DatabaseFrozenXID = 0
	e.VacuumProgress = nil
	for address, store := range relations {
		if relation, ok := next.Relation(address); ok && relation.RelationKind.Stored() {
			e.Visibility.InitializeMaintenanceLocked(store)
		}
	}
	e.UpdateDatabaseMaintenanceLocked()
	e.Exclusions.ResetLocked()
	e.Storage.ReplaceSequencesLocked(sequences)
	e.clearPersistentSequenceMemoryLocked()
	e.Catalog.InstallRevisionFrontierLocked(nextRevision)
	e.fenceSerializableLocked()
	e.Visibility.RetireCommitTimestampsLocked()
}

func (e *Engine) RegisterCatalogProvider(p catalog.Provider) error {
	if p == nil {
		return fmt.Errorf("stuntgres: nil catalog provider")
	}
	id := p.ClassID()
	if id == 0 {
		return fmt.Errorf("stuntgres: zero catalog class")
	}
	if e.Mu == nil {
		return fmt.Errorf("stuntgres: engine is not initialized")
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.Catalog.RegisterProviderLocked(id, p)
}
