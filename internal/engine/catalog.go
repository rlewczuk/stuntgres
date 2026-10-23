package engine

import (
	"context"
	"fmt"
	"math"
	"slices"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// domainOf returns the engine's domain, or nil for a nil engine, so a
// provenance comparison against a possibly-nil engine keeps its meaning.
func domainOf(e *Engine) *ids.Domain {
	if e == nil {
		return nil
	}
	return e.catalog.Domain
}

// relationProvider validates relation descriptors; the root supplies the
// validators of the packages above table, built once.
var relationProvider = table.RelationProvider{Validators: table.RelationValidators{
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

type catalogLifecycle uint8

const (
	catalogOpen catalogLifecycle = iota
	catalogValidated
	catalogPublished
	catalogDiscarded
)

type catalogRowShape struct {
	original       *storage.Relation
	root           []storage.RowVersion
	replacement    *storage.Relation
	observedLength int
	observedEnds   *storage.DeletionRoot
	generation     uint64
}

// CatalogStage is a private catalog change: the pure overlay it embeds plus
// the publication coordination the engine owns (storage row shapes, index
// candidates, domain validation, materialized replacement, role candidates).
type CatalogStage struct {
	owner *Engine
	catalog.Candidate
	rowShapes           map[ids.ObjectAddress]catalogRowShape
	storageReads        []storageReadFence
	sequence            *sequence.Stage
	typed               *typedef.TypedStage
	lifecycle           catalogLifecycle
	invalid             error
	authSession         *session.Session
	authRevision        uint64
	roleRevision        uint64
	extensionNamespace  ids.ObjectAddress
	databasePredecessor catalog.Descriptor
	databaseAuthority   catalog.Descriptor
	// Optional DROP OWNED membership deletions join this publication, not a
	// separate role installation. Every other catalog caller leaves it nil.
	roleCandidate      *access.RoleRecord
	schemaCascadeNames []string
	dropCascadeNames   []string
	dependencyPath     []ids.ObjectAddress
	domainTargets      []ids.ObjectAddress
	domainBases        []catalog.Descriptor
	domainAlter        *typedef.DomainAlterStage
	// Physical retirement alone may saturate dependent descriptor/store/index
	// counters; fresh identities and exact predecessor fences remain mandatory.
	terminalCleanup bool
	// New query-derived tables carry private initial storage, never replacements
	// of live storage. Publication installs the definition and data together.
	initialTables    map[ids.ObjectAddress]*storage.Relation
	initialTx        ids.TxRef
	querySources     []catalog.Descriptor
	tableSlot        *table.Stage
	indexesSlot      *index.Stage
	materializedSlot *view.MaterializedStage
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
	s := &CatalogStage{owner: e, Candidate: catalog.NewCandidate(base)}
	if base.Owner() != e.catalog.owner() || base.State() == nil {
		s.invalid = fmt.Errorf("stuntgres: foreign catalog view")
	}
	return s
}

// Retain selected private authority separately from the committed predecessor
// already captured by authorizationLocked; building a stage needs no live read.
func (s *CatalogStage) authorizeLocked(auth AuthorizationView, session *session.Session) {
	s.authSession, s.authRevision, s.roleRevision = session, auth.Revision, auth.RoleRecord.Revision
	s.databaseAuthority = auth.Catalog.Get(auth.Owner.DatabaseAddress())
	s.databasePredecessor = auth.DatabasePredecessor
}

func (s *CatalogStage) Create(d catalog.Descriptor) error {
	if s == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	return s.Candidate.Create(d)
}
func (s *CatalogStage) Replace(d catalog.Descriptor) error {
	return s.replaceDescriptor(d, true, false)
}

// A fully rebuilt immutable type closure is already reciprocal. Installing its
// members must not synthesize a second projection based on map iteration order.
func (s *CatalogStage) replaceDescriptor(d catalog.Descriptor, synchronize, preserveDomainPlan bool) error {
	if s == nil || s.lifecycle != catalogOpen || s.invalid != nil || d == nil {
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

// alterRelation couples a same-identity revision with an immutable storage root.
func (s *CatalogStage) alterRelation(ctx context.Context, ref ids.ObjectRef, d *catalog.Relation, original, replacement *storage.Relation, observedLength int, observedGeneration uint64, observedEnds *storage.DeletionRoot) error {
	if s == nil || s.owner == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	e := s.owner
	e.mu.Lock()
	if err := e.catalog.validateObjectRefLocked(s.Base, ref); err != nil {
		e.mu.Unlock()
		return err
	}
	old, ok := s.Base.Relation(ref.Address)
	validRevision := ok && d != nil && old.Revision() != ids.DefinitionRevision(^uint64(0)) && d.Revision() == old.Revision()+1
	if s.terminalCleanup && ok && old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		validRevision = d != nil && d != old && d.Revision() == old.Revision()
	}
	if !ok || ref.Address.SubID != 0 || d == nil || d.Address() != old.Address() || d.Namespace() != old.Namespace() || d.Name() != old.Name() || !validRevision || original == nil || replacement == nil || e.stageStorageLocked(s, ref.Address) != original {
		e.mu.Unlock()
		return catalog.Conflict{}
	}
	if len(original.Versions) != observedLength || len(replacement.Versions) != observedLength || original.Generation != observedGeneration || original.Ends != observedEnds || replacement.Ends != observedEnds {
		e.mu.Unlock()
		return catalog.Conflict{}
	}
	if original == replacement && !slices.Equal(d.Attributes, old.Attributes) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: invalid metadata-only row shape")
	}
	beforeRows := original.Versions
	e.mu.Unlock()
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
	if s.rowShapes == nil {
		s.rowShapes = make(map[ids.ObjectAddress]catalogRowShape)
	}
	s.rowShapes[ref.Address] = catalogRowShape{original: original, root: beforeRows, replacement: replacement, observedLength: observedLength, observedEnds: observedEnds, generation: observedGeneration}
	return nil
}

// Remove stages a whole user relation with its coherent observed identity.
func (s *CatalogStage) Remove(ref ids.ObjectRef) error {
	return s.removeObject(ref, false)
}

// Schema closure uses the same removal authority for namespaces and relations.
func (s *CatalogStage) removeObject(ref ids.ObjectRef, namespace bool) error {
	if s == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	return s.removeObjectLocked(ref, namespace)
}

// removeObjectLocked requires the owning engine mutex, including validation.
func (s *CatalogStage) removeObjectLocked(ref ids.ObjectRef, namespace bool) error {
	if s == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	return stageDropProvider(context.Background(), s, nil, nil, nil).removeObjectLocked(ref, namespace)
}

// removeObjectLocked is the one removal analysis for execution and immutable
// DROP preparation. The effect sink owns bookkeeping, never the discovery.
// The caller holds the engine mutex.
func (provider *dropProvider) removeObjectLocked(ref ids.ObjectRef, namespace bool) error {
	if ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: whole object required")
	}
	if err := provider.owner.catalog.validateObjectRefLocked(provider.base, ref); err != nil {
		return err
	}
	d := provider.base.Get(ref.Address)
	switch d.(type) {
	case *catalog.Relation:
		if ref.Address.ClassID != ids.RelationClassID {
			return fmt.Errorf("stuntgres: only user relations can be removed")
		}
	case *catalog.Namespace:
		if !namespace || ref.Address.ClassID != ids.NamespaceClassID {
			return fmt.Errorf("stuntgres: only user relations can be removed")
		}
	case *catalog.Type:
		t := d.(*catalog.Type)
		if t.OwningRelation == (ids.ObjectAddress{}) && !t.SQLType.IsUserDefined() && !(t.SQLType.IsArray() && t.SQLType.Element().IsUserDefined()) {
			return fmt.Errorf("stuntgres: builtin types cannot be removed")
		}
	case *catalog.Collation:
		if d.(*catalog.Collation).Seeded() {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(provider.base, d.Address(), provider.path)+" because it is required by the database system")
		}
	case *catalog.Conversion:
		if d.(*catalog.Conversion).Seeded() {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(provider.base, d.Address(), provider.path)+" because it is required by the database system")
		}
	case *catalog.AccessMethod:
		method := d.(*catalog.AccessMethod)
		if catalog.AccessMethodSeeded(method) {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(provider.base, d.Address(), provider.path)+" because it is required by the database system")
		}
		if err := (catalog.AccessMethodProvider{}).Validate(provider.base, d); err != nil {
			return err
		}
	case *catalog.OperatorClass:
		if catalog.OperatorClassSeeded(d) {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(provider.base, d.Address(), provider.path)+" because it is required by the database system")
		}
	case *catalog.OperatorFamily, *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport:
		if catalog.OperatorFamilySeeded(d) {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(provider.base, d.Address(), provider.path)+" because it is required by the database system")
		}
	case *catalog.Operator:
		if routine.OperatorSeeded(d.(*catalog.Operator)) {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(provider.base, d.Address(), provider.path)+" because it is required by the database system")
		}
	case *catalog.ForeignObject:
		if err := provider.owner.catalog.Provider(ref.Address.ClassID).Validate(provider.base, d); err != nil {
			return err
		}
	case *catalog.Cast, *catalog.Extension, *catalog.Procedure, *catalog.TSTemplate, *catalog.TSDictionary:
		if procedure, ok := d.(*catalog.Procedure); ok && procedure.Seeded() {
			return sqlError("2BP01", "cannot drop "+stored.DependencyObject(provider.base, d.Address(), provider.path)+" because it is required by the database system")
		}
		objectProvider := provider.owner.catalog.Provider(ref.Address.ClassID)
		if objectProvider == nil {
			return fmt.Errorf("stuntgres: missing installed object provider")
		}
		if err := objectProvider.Validate(provider.base, d); err != nil {
			return err
		}
	case *catalog.Parameter:
		if err := (role.ParameterProvider{Settings: &provider.owner.settings}).Validate(provider.base, d); err != nil {
			return err
		}
	case *catalog.Tablespace:
		return sqlError("2BP01", "cannot drop tablespace "+catalog.DependencyIdentifier(d.Name())+" because it is required by the database system")
	default:
		return fmt.Errorf("stuntgres: only user relations can be removed")
	}
	// Provider/ref validation above belongs to the authentic predecessor.
	// AUTO retirement below follows the actual metadata entering this phase.
	d = provider.RetirementDescriptor(ref.Address)
	if provider.Removed(ref.Address) {
		return nil
	}
	if err := provider.recordRemoval(ref, namespace); err != nil {
		return err
	}
	if operator, ok := d.(*catalog.Operator); ok {
		if err := provider.clearOperatorLinks(operator); err != nil {
			return err
		}
	}
	if typ, ok := d.(*catalog.Type); ok && typ.SQLType.IsComposite() {
		if backing, ok := provider.RetirementDescriptor(typ.OwningRelation).(*catalog.Relation); ok && backing.RelationKind == catalog.CompositeRelation {
			if err := provider.removeObjectLocked(provider.owner.catalog.Domain.Ref(provider.base.Epoch(), backing.Address()), false); err != nil {
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
				if err := provider.removeObjectLocked(provider.owner.catalog.Domain.Ref(provider.base.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}), false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (e *Engine) stageRead(s *CatalogStage) catalog.Read {
	return e.catalog.read(s.Candidate.State())
}
func (e *Engine) ValidateCatalogStage(ctx context.Context, s *CatalogStage) (*PreparedCatalogPublication, error) {
	if s == nil || s.owner != e || s.lifecycle != catalogOpen || s.invalid != nil {
		return nil, fmt.Errorf("stuntgres: invalid catalog stage")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := table.ValidateInitialTables(s.initialTables, s.Get, s.Base); err != nil {
		return nil, err
	}
	if err := s.validateMaterializedReplacement(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	if err := s.validateRoleCandidate(e.roles.published); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	for address, descriptor := range s.Descriptors() {
		typ, ok := descriptor.(*catalog.Type)
		if !ok || !typ.SQLType.IsComposite() && !typ.SQLType.IsDomain() {
			continue
		}
		old, _ := s.Base.Type(address)
		if old == nil || old.SQLType != typ.SQLType {
			if err := e.reownStagedTypeGenerationsLocked(s); err != nil {
				e.mu.Unlock()
				return nil, err
			}
			break
		}
	}
	if !e.storageReadsValidLocked(s) {
		e.mu.Unlock()
		return nil, catalog.Conflict{}
	}
	if err := s.validateIndexMaintenance(e.stageCatalogBaseLocked(s)); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	if err := e.evolveStageIndexesLocked(s, ids.ObjectAddress{}, nil); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	if err := e.markStageIndexHistoryLocked(s); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	if err := e.markStageSubclassHistoryLocked(s); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	for address, shape := range s.rowShapes {
		if e.stageStorageLocked(s, address) != shape.original || shape.original.Generation != shape.generation || len(shape.original.Versions) != shape.observedLength {
			e.mu.Unlock()
			return nil, catalog.Conflict{}
		}
	}
	if r := s.materializedOrZero().Replacement; r != nil {
		if err := s.materializedReplacementCurrent(e.stageCatalogBaseLocked(s), e.stageStorageLocked(s, r.Original.Address())); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}
	e.mu.Unlock()
	var view catalog.Read
	if s.DescriptorCount() != 0 {
		view = e.stageRead(s)
	}
	if view.State() != nil {
		if err := catalog.ValidateDefinitionOIDs(view); err != nil {
			return nil, err
		}
	}
	for _, d := range s.Descriptors() {
		p := e.catalog.Provider(d.Address().ClassID)
		if p == nil {
			return nil, fmt.Errorf("stuntgres: unregistered catalog class %d", d.Address().ClassID)
		}
		if err := p.Validate(view, d); err != nil {
			return nil, err
		}
		e.mu.Lock()
		roles := e.sessionRolesLocked(s.authSession)
		authErr := access.ValidateACL(s.roleRead(roles, view), d)
		e.mu.Unlock()
		if authErr != nil {
			return nil, authErr
		}
	}
	s.lifecycle = catalogValidated
	return &PreparedCatalogPublication{stage: s, epoch: s.Base.Epoch(), ctx: ctx}, nil
}
func (e *Engine) publishCatalogOperation(op *session.Operation, scope *StatementScope, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	if op != nil && op.Session.Transactions.RetainsTransaction() {
		return e.stageMessageCatalog(op, p, token)
	}
	if scope != nil && scope.owner.State == session.TxActive {
		if scope.op == nil || scope.op != op || p == nil || p.stage == nil || p.stage.owner != e || p.stage.lifecycle != catalogValidated {
			return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
		}
		if token.Epoch != p.epoch {
			return ChangeSet{}, ids.ErrCatalogReplaced
		}
		if err := e.stageCatalogOverlay(scope, p.stage, scope.owner.Catalog.Clone()); err != nil {
			return ChangeSet{}, err
		}
		return catalogStageChanges(p.stage), nil
	}
	return e.publishCatalog(op, scope, p, token)
}
func (e *Engine) publishCatalog(op *session.Operation, scope *StatementScope, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	if p == nil || p.stage == nil || p.stage.owner != e || p.stage.lifecycle != catalogValidated {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
	}
	s := p.stage
	changes := catalogStageChanges(s)
	for {
		e.mu.Lock()
		if s.lifecycle != catalogValidated {
			e.mu.Unlock()
			return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
		}
		current := e.catalog.state
		if err := s.defaultPreflight(current); err != nil {
			e.mu.Unlock()
			return ChangeSet{}, err
		}
		currentStorage := e.storage.Relations
		if p.epoch != current.Epoch() || token.Epoch != current.Epoch() {
			e.mu.Unlock()
			return ChangeSet{}, ids.ErrCatalogReplaced
		}
		if err := s.validateIndexMaintenance(current); err != nil {
			e.mu.Unlock()
			return ChangeSet{}, err
		}
		if !e.storage.IndexRootsCurrent(s.domainAlter.AlteredIndexRoots()) {
			e.mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		if fence := s.domainAlter.ValidationFence(); fence != nil && !fence.Current(e.catalog.read(current), currentStorage) {
			e.mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		if !table.TypedObservationValid(s.typed, e.catalog.read(current)) {
			e.mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		if !accessMethodStageCurrent(s, e.catalog.read(current)) {
			e.mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		for _, base := range s.domainBases {
			observed := base
			if s.Get(base.Address()) == base {
				observed = s.Base.Get(base.Address())
			}
			if current.Get(base.Address()) != observed {
				e.mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
			if ns, ok := base.(*catalog.Namespace); ok && s.terminalCleanup && ns.Temporary != (ids.SessionToken{}) {
				for address, d := range current.Descriptors() {
					if _, removed := s.Removal(address); d.Namespace() == ns.Address() && (!removed || d != s.Base.Get(address)) {
						e.mu.Unlock()
						return ChangeSet{}, catalog.Conflict{}
					}
				}
			}
		}
		if err := s.validateLikeSources(e.catalog.read(current)); err != nil {
			e.mu.Unlock()
			return ChangeSet{}, err
		}
		for _, source := range s.querySources {
			if !view.MaterializedSourceCurrent(s.materializedOrZero().SourceView, source, current, currentStorage[source.Address()]) {
				e.mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
		}
		for _, d := range s.Descriptors() {
			ns := d.Namespace()
			if ns != (ids.ObjectAddress{}) && s.Get(ns) == nil && current.Get(ns) != s.Base.Get(ns) {
				e.mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
			// A namespace move still relies on source lookup authority. Fence
			// that original namespace as well as the destination, without
			// republishing it or observing unrelated namespace changes.
			if old := s.Base.Get(d.Address()); old != nil {
				source := old.Namespace()
				if source != ns && source != (ids.ObjectAddress{}) && s.Get(source) == nil && current.Get(source) != s.Base.Get(source) {
					e.mu.Unlock()
					return ChangeSet{}, catalog.Conflict{}
				}
			}
		}
		for a := range s.Removals() {
			if a.ClassID == ids.NamespaceClassID {
				for address, d := range current.Descriptors() {
					if _, removed := s.Removal(address); d.Namespace() == a && (!removed || d != s.Base.Get(address)) {
						e.mu.Unlock()
						return ChangeSet{}, catalog.Conflict{}
					}
				}
			}
			// A newly attached MV was not in the validated retirement closure.
			// Recheck its live immutable x authority at the final publication fence.
			if a.ClassID == ids.ExtensionClassID {
				for address, descriptor := range current.Descriptors() {
					if descriptor == s.Base.Get(address) {
						continue
					}
					if relation, ok := descriptor.(*catalog.Relation); ok && view.MaterializedExtensionDepends(relation, a) {
						e.mu.Unlock()
						return ChangeSet{}, catalog.Conflict{}
					}
				}
			}
		}
		for a := range s.Descriptors() {
			if current.Get(a) != s.Base.Get(a) {
				e.mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
		}
		for a := range s.Removals() {
			if current.Get(a) != s.Base.Get(a) {
				e.mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
		}
		for k, observed := range s.Touched() {
			if current.NameAddress(k) != observed {
				e.mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
		}
		for a, removal := range s.Removals() {
			observed := removal.Descriptor
			d := current.Get(a)
			if removal.Ref.Domain() != e.catalog.Domain || removal.Ref.Epoch != current.Epoch() || removal.Ref.Address != a || d == nil || d != observed || d.Revision() != observed.Revision() {
				e.mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
		}
		if !e.storageReadsValidLocked(s) {
			e.mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		for a, shape := range s.rowShapes {
			if currentStorage[a] != shape.original || len(shape.original.Versions) != shape.observedLength || shape.original.Generation != shape.generation || shape.original.Ends != shape.observedEnds {
				e.mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
		}
		if r := s.materializedOrZero().Replacement; r != nil {
			if err := s.materializedReplacementCurrent(current, currentStorage[r.Original.Address()]); err != nil {
				e.mu.Unlock()
				return ChangeSet{}, err
			}
		}
		stageIndexes := s.indexesOrZero()
		for address, candidate := range stageIndexes.Candidates {
			index, ok := s.Get(address).(*catalog.Relation)
			if !ok {
				index, ok = current.Relation(address)
			}
			if !ok || index.SQLIndex == nil {
				e.mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
			store := currentStorage[index.SQLIndex.Parent]
			if shape, changed := s.rowShapes[index.SQLIndex.Parent]; changed {
				store = shape.replacement
			} else if initial := s.initialTables[index.SQLIndex.Parent]; initial != nil {
				store = initial
			}
			if !candidate.MatchesIndex(index, store) {
				e.mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
		}
		indexStores := index.CaptureIndexStores(currentStorage, current)
		for _, d := range s.Descriptors() {
			index, ok := d.(*catalog.Relation)
			if !ok || index.SQLIndex == nil {
				continue
			}
			if indexStores == nil {
				indexStores = make(map[ids.ObjectAddress]*storage.Relation)
			}
			a := index.SQLIndex.Parent
			if indexStores[a] == nil && currentStorage[a] != nil {
				copy := *currentStorage[a]
				copy.IndexSource = currentStorage[a]
				indexStores[a] = &copy
			}
		}
		indexPredecessors := e.storage.IndexRoots
		var disclosure *AuthorizationView
		if op != nil {
			auth, authErr := e.authorizationLocked(op)
			if authErr != nil {
				e.mu.Unlock()
				return ChangeSet{}, authErr
			}
			disclosure = &auth
		}
		e.mu.Unlock()
		next := catalog.CloneState(current)
		s.ApplyDefaultACLs(current, next)
		operators := catalog.OperatorIndexEdit{Index: current.OperatorNames()}
		procedures := catalog.ProcedureIndexEdit{Index: current.ProcedureNames()}
		relations := make(map[ids.ObjectAddress]*storage.Relation, len(currentStorage)+s.DescriptorCount())
		for a, v := range currentStorage {
			relations[a] = v
		}
		for a, removal := range s.Removals() {
			d := removal.Descriptor
			operators.Replace(current.Get(a), s.Get(a))
			procedures.Replace(current.Get(a), s.Get(a))
			next.RemoveDescriptor(a)
			next.RemoveDumpPriority(a)
			next.UnbindName(catalog.DescriptorNameKey(d))
			delete(relations, a)
		}
		for k := range s.OldNames() {
			next.UnbindName(k)
		}
		for a, d := range s.Descriptors() {
			if _, removed := s.Removal(a); !removed {
				operators.Replace(current.Get(a), d)
				procedures.Replace(current.Get(a), d)
			}
			next.SetDescriptor(a, d)
			if relation, ok := d.(*catalog.Relation); ok && relation.RelationKind.Stored() && relations[a] == nil {
				if initial := s.initialTables[a]; initial != nil {
					relations[a] = initial
				} else {
					relations[a] = &storage.Relation{}
				}
			}
		}
		for a, shape := range s.rowShapes {
			relations[a] = shape.replacement
		}
		if replacement := s.materializedOrZero().Replacement; replacement != nil {
			relations[replacement.Original.Address()] = replacement.Storage
		}
		for k, a := range s.Names() {
			next.BindName(k, a)
		}
		next.SetOperatorNames(operators.Index)
		next.SetProcedureNames(procedures.Index)
		if err := catalogDependencyPreflight(current, next, s.sequence.IdentityDependencyList(), s.sequence.IdentityOriginalMap()); err != nil {
			return ChangeSet{}, err
		}
		buildStores := indexStores
		candidateStores := false
		var rebuildIndexes map[ids.ObjectAddress]bool
		if len(indexStores) > 0 && (len(s.rowShapes) > 0 || s.materializedOrZero().Replacement != nil) {
			buildStores = make(map[ids.ObjectAddress]*storage.Relation, len(indexStores))
			for a, store := range indexStores {
				buildStores[a] = store
			}
			candidateStores = true
			rebuildIndexes = make(map[ids.ObjectAddress]bool, len(s.rowShapes))
			for a, shape := range s.rowShapes {
				buildStores[a] = shape.replacement
				rebuildIndexes[a] = true
			}
			if replacement := s.materializedOrZero().Replacement; replacement != nil {
				buildStores[replacement.Original.Address()] = replacement.Storage
				rebuildIndexes[replacement.Original.Address()] = true
			}
		}
		// New parents and their backing indices publish together. Build from
		// the exact candidate store, but keep committed absence in the fence.
		for _, d := range s.Descriptors() {
			index, ok := d.(*catalog.Relation)
			if !ok || index.SQLIndex == nil {
				continue
			}
			a := index.SQLIndex.Parent
			parent, staged := s.Get(a).(*catalog.Relation)
			if !staged || (parent.RelationKind != catalog.TableRelation && parent.RelationKind != catalog.MaterializedViewRelation) || current.Get(a) != nil {
				continue
			}
			if !candidateStores {
				buildStores = make(map[ids.ObjectAddress]*storage.Relation, len(indexStores)+1)
				for address, store := range indexStores {
					buildStores[address] = store
				}
				candidateStores = true
			}
			buildStores[a] = relations[a]
		}
		sourceRoots := indexPredecessors
		if len(stageIndexes.Candidates) != 0 {
			sourceRoots = make(map[ids.ObjectAddress]*storage.IndexRoot, len(indexPredecessors)+len(stageIndexes.Candidates))
			for a, root := range indexPredecessors {
				sourceRoots[a] = root
			}
			for a, candidate := range stageIndexes.Candidates {
				sourceRoots[a] = candidate.Root
			}
		}
		var runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef
		if r := s.materializedOrZero().Replacement; r != nil && r.Concurrent {
			runtimeOwners = map[ids.ObjectAddress]authorization.PrincipalRef{r.Original.Address(): r.Original.Owner}
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: p.ctx, Host: hostOf(scope)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		if scope != nil {
			evaluation.Input, evaluation.Output = scope.input, scope.output
		} else if op != nil {
			evaluation.Input, evaluation.Output = op.Session.Settings.Presentation()
		}
		indexRoots, indexErr := e.prepareIndexCatalog(&evaluation, current, next, buildStores, sourceRoots, rebuildIndexes, stageIndexes.Rebuilds, stageIndexes.Output, disclosure, runtimeOwners, stageIndexes.Candidates)
		if indexErr != nil {
			return ChangeSet{}, indexErr
		}
		domainChanged := len(s.domainTargets) != 0
		for _, d := range s.Descriptors() {
			if t, ok := d.(*catalog.Type); ok && t.SQLType.IsUserDefined() {
				domainChanged = true
				break
			}
		}
		for _, descriptor := range next.Descriptors() {
			if relation, ok := descriptor.(*catalog.Relation); ok && relation.Inheritance != nil {
				if err := table.ValidateInheritance(e.catalog.read(next), relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if domainChanged || s.Get(descriptor.Address()) != nil {
				if relation, ok := descriptor.(*catalog.Relation); ok {
					if err := typedef.ValidateTypedAttachment(e.catalog.read(next), relation); err != nil {
						return ChangeSet{}, err
					}
				}
				if err := stored.ValidateTypeProviders(e.catalog.read(next), descriptor); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*catalog.Relation); ok && len(relation.Security.Policies) != 0 {
				if err := view.ValidatePolicies(e.catalog.read(next), relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*catalog.Relation); ok && len(stored.RulesOf(relation)) != 0 {
				if err := stored.ValidateRules(e.catalog.read(next), relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if viewRelation, ok := descriptor.(*catalog.Relation); ok && viewRelation.View != nil {
				if err := view.ValidateViewDefinition(e.catalog.read(next), viewRelation); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*catalog.Relation); ok && len(relation.Constraints.Check) != 0 {
				if err := table.ValidateCheckProviders(e.catalog.read(next), relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*catalog.Relation); ok && len(relation.ColumnMeta.Defaults) != 0 {
				if err := table.ValidateDefaultProviders(e.catalog.read(next), relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*catalog.Relation); ok && len(relation.ColumnMeta.Generations) != 0 {
				if err := table.ValidateGenerationProviders(e.catalog.read(next), relation); err != nil {
					return ChangeSet{}, err
				}
			}
		}
		e.mu.Lock()
		if op != nil {
			op.Session.Mu.Lock()
		}
		if e.catalog.state != current {
			if op != nil {
				op.Session.Mu.Unlock()
			}
			e.mu.Unlock()
			continue
		}
		if !index.StoresUnchanged(e.storage.Relations, indexStores, current, next, indexRoots, indexPredecessors) {
			if op != nil {
				op.Session.Mu.Unlock()
			}
			e.mu.Unlock()
			continue
		}
		if !e.storageReadsValidLocked(s) {
			if op != nil {
				op.Session.Mu.Unlock()
			}
			e.mu.Unlock()
			return ChangeSet{}, catalog.Conflict{}
		}
		for a, shape := range s.rowShapes {
			if e.storage.Relations[a] != shape.original || len(shape.original.Versions) != shape.observedLength || shape.original.Generation != shape.generation || shape.original.Ends != shape.observedEnds {
				if op != nil {
					op.Session.Mu.Unlock()
				}
				e.mu.Unlock()
				return ChangeSet{}, catalog.Conflict{}
			}
		}
		if r := s.materializedOrZero().Replacement; r != nil {
			if err := s.materializedReplacementCurrent(e.catalog.state, e.storage.Relations[r.Original.Address()]); err != nil {
				if op != nil {
					op.Session.Mu.Unlock()
				}
				e.mu.Unlock()
				return ChangeSet{}, err
			}
		}
		err := p.ctx.Err()
		if err == nil && !e.storage.IndexRootsCurrent(s.domainAlter.AlteredIndexRoots()) {
			err = catalog.Conflict{}
		}
		if fence := s.domainAlter.ValidationFence(); err == nil && fence != nil && !fence.Current(e.catalog.read(e.catalog.state), e.storage.Relations) {
			err = catalog.Conflict{}
		}
		if err == nil && s.databasePredecessor != nil && e.catalog.state.Get(e.catalog.DatabaseAddress()) != s.databasePredecessor {
			err = ids.ErrCatalogReplaced
		}
		if err == nil {
			err = catalogDependencyPreflight(e.catalog.state, next, s.sequence.IdentityDependencyList(), s.sequence.IdentityOriginalMap())
		}
		if err == nil {
			err = s.validateRoleCandidate(e.roles.published)
		}
		if err == nil && op != nil {
			err = publicationOperationLocked(e, op)
		}
		if err == nil {
			err = catalog.ValidateDefaultACLs(s.roleRead(e.roles.published, e.catalog.read(next)), e.catalog.read(next))
		}
		if err == nil {
			for _, d := range s.Descriptors() {
				if err = access.ValidateACL(s.roleRead(e.roles.published, e.catalog.read(next)), d); err != nil {
					break
				}
			}
		}
		if err == nil && s.authSession != nil {
			// SQL stages use the operation's session, already locked above.
			if op == nil || s.authSession != op.Session {
				s.authSession.Mu.Lock()
			}
			auth := s.authSession.Authorization
			valid := auth != nil && auth.Revision == s.authRevision && e.roles.published.Revision == s.roleRevision
			if op == nil || s.authSession != op.Session {
				s.authSession.Mu.Unlock()
			}
			if !valid {
				err = fmt.Errorf("stuntgres: authorization changed before publication")
			}
		}
		if err == nil && s.materializedOrZero().Replacement != nil && s.authSession != nil {
			if op == nil || s.authSession != op.Session {
				s.authSession.Mu.Lock()
			}
			auth := e.authorityView(access.View{Owner: e.catalog.owner(), PairedCatalog: e.catalog.read(e.catalog.state), RoleRecord: e.roles.published, Catalog: e.catalog.read(e.catalog.state), Roles: e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published), State: s.authSession.Authorization.State})
			err = auth.Check(access.Requirement{Object: e.catalog.Domain.Ref(e.catalog.state.Epoch(), s.materializedOrZero().Replacement.Original.Address()), Privileges: authorization.MaintainPrivilege})
			if op == nil || s.authSession != op.Session {
				s.authSession.Mu.Unlock()
			}
		}
		if err == nil {
			err = p.ctx.Err()
		}
		if err == nil {
			namespaceChanged := false
			for a := range s.Descriptors() {
				namespaceChanged = namespaceChanged || a.ClassID == ids.NamespaceClassID
			}
			for a := range s.Removals() {
				namespaceChanged = namespaceChanged || a.ClassID == ids.NamespaceClassID
			}
			if namespaceChanged {
				err = next.AdvanceNamespaceGeneration()
			}
		}
		if err == nil && (len(s.initialTables) != 0 || s.materializedOrZero().Replacement != nil) && e.visibility.CommitSeq() == ids.CommitSeq(math.MaxUint64) {
			err = fmt.Errorf("stuntgres: commit sequence exhausted")
		}
		if err == nil && s.initialTx != (ids.TxRef{}) {
			err = e.visibility.SerializablePrecommitLocked(s.initialTx)
		}
		commitRef := s.initialTx
		if scope != nil {
			commitRef = scope.txRef
		}
		if err == nil && commitRef != (ids.TxRef{}) {
			err = e.assignTransactionXIDLocked(commitRef)
		}
		var databaseRoots map[*Engine]*catalog.State
		if err == nil {
			databaseRoots = e.databaseProjectionLocked(next)
		}
		if err == nil {
			// Staged replacements may advance revisions without reserving from
			// the allocator. Commit its frontier with the successful publication.
			nextRevision := e.catalog.nextRevision
			for _, d := range s.Descriptors() {
				nextRevision, _ = catalog.AdvanceRevisionFrontier(nextRevision, d)
			}
			if len(s.initialTables) != 0 || s.materializedOrZero().Replacement != nil {
				frontier := e.visibility.CommitSeq() + 1
				for _, initial := range s.initialTables {
					for i := range initial.Versions {
						initial.Versions[i].Born = frontier
						initial.Versions[i].InsertFullXID = e.visibility.FullXIDLocked(commitRef)
						initial.Maintenance.RecordBirth(initial.Versions[i].InsertFullXID)
						initial.Versions[i].Frozen, initial.Versions[i].ImportFrozen = false, false
					}
					initial.Generation = 1
					e.initializeMaintenanceLocked(initial)
					initial.Maintenance.Inserts += int64(len(initial.Versions))
					initial.Maintenance.InsertsSince += int64(len(initial.Versions))
					initial.Maintenance.Modifications += int64(len(initial.Versions))
					initial.Maintenance.Live += int64(len(initial.Versions))
				}
				if replacement := s.materializedOrZero().Replacement; replacement != nil {
					// Refresh retains this object's cumulative facts, captured from
					// the validated current predecessor only inside publication.
					replacement.Storage.Maintenance = replacement.Predecessor.Maintenance
					replacement.Storage.Maintenance.FrozenXID = 0
					replacement.Storage.Maintenance.Live = 0
					replacement.Storage.Maintenance.Dead = 0
					for i := range replacement.Storage.Versions {
						replacement.Storage.Versions[i].Born = frontier
						replacement.Storage.Versions[i].InsertFullXID = e.visibility.FullXIDLocked(commitRef)
						replacement.Storage.Maintenance.RecordBirth(replacement.Storage.Versions[i].InsertFullXID)
						replacement.Storage.Versions[i].Frozen, replacement.Storage.Versions[i].ImportFrozen = false, false
					}
					if replacement.Concurrent {
						lineage := replacement.Predecessor.MaterializedLineage
						if lineage == nil {
							lineage = &storage.MaterializedLineage{}
						}
						replacement.Predecessor.MaterializedLineage = lineage
						replacement.Storage.MaterializedLineage = lineage
					}
					e.initializeMaintenanceLocked(replacement.Storage)
					replacement.Storage.Maintenance.Inserts += int64(len(replacement.Storage.Versions))
					replacement.Storage.Maintenance.InsertsSince += int64(len(replacement.Storage.Versions))
					replacement.Storage.Maintenance.Modifications += int64(len(replacement.Storage.Versions))
					replacement.Storage.Maintenance.Live += int64(len(replacement.Storage.Versions))
				}
				e.visibility.AdvanceCommitSeqLocked()
			}
			for address := range s.Descriptors() {
				e.initializeMaintenanceLocked(relations[address])
			}
			if s.initialTx != (ids.TxRef{}) {
				e.visibility.SerializableCommittedLocked(s.initialTx, e.visibility.CommitSeq())
			}
			e.catalog.state = next
			installDatabaseProjectionLocked(databaseRoots)
			e.storage.ReplaceRelationsLocked(relations)
			index.PreserveUnchangedIndexRoots(indexRoots, indexPredecessors, e.storage.IndexRoots)
			e.storage.ReplaceIndexRootsLocked(indexRoots)
			for a := range s.Removals() {
				e.storage.RemoveSequenceLocked(a)
			}
			for a, generator := range s.sequence.StagedGenerators() {
				e.storage.InstallSequenceLocked(a, generator)
			}
			e.catalog.nextRevision = nextRevision
			if s.roleCandidate != nil {
				e.roles.published = s.roleCandidate
			}
			if commitRef != (ids.TxRef{}) {
				e.visibility.RecordCommitTimestampLocked(commitRef)
			}
			s.lifecycle = catalogPublished
		}
		if op != nil {
			op.Session.Mu.Unlock()
		}
		e.mu.Unlock()
		if err != nil {
			return ChangeSet{}, err
		}
		return changes, nil
	}
}
func (e *Engine) DiscardCatalogStage(s *CatalogStage) {
	if s != nil && s.owner == e && s.lifecycle != catalogPublished {
		s.lifecycle = catalogDiscarded
	}
}

// ReplaceCatalog installs replacement, a catalog view that source owns, with
// source's committed rows, sequences and roles. source may be e.
func (e *Engine) ReplaceCatalog(ctx context.Context, source *Engine, replacement catalog.Read) (ChangeSet, error) {
	return e.replaceCatalog(ctx, source, replacement, nil)
}

// Restore retains its genuine private operation through owner-cell validation.
func (e *Engine) replaceCatalog(ctx context.Context, source *Engine, replacement catalog.Read, restoreOp *session.Operation) (ChangeSet, error) {
	var installed ids.CatalogEpoch
	defer func() {
		if installed != 0 {
			e.locks.Fence(installed)
		}
	}()
	if replacement.State() == nil || replacement.Owner() == nil || source == nil || replacement.Owner() != source.catalog.owner() {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid replacement catalog")
	}
	if err := ctx.Err(); err != nil {
		return ChangeSet{}, err
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
		p := e.catalog.Provider(d.Address().ClassID)
		if p == nil {
			return ChangeSet{}, fmt.Errorf("stuntgres: unregistered catalog class %d", d.Address().ClassID)
		}
		if err := p.Validate(replacement, d); err != nil {
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
		source, frontier := owner.storage.Relations, owner.visibility.CommitSeq()
		heap, err := visibility.ResolveHeapAccess(owner.catalog.read(owner.catalog.state), 2)
		if err != nil {
			return err
		}
		for _, a := range relationAddresses {
			if source[a] == nil {
				return fmt.Errorf("stuntgres: relation storage is missing")
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
		indexes, err = owner.captureIndexSnapshot(ctx, replacement, source, owner.storage.IndexRoots, frontier)
		return err
	}
	if replacement.Owner() == e.catalog.owner() {
		e.replacementMu.Lock()
		defer e.replacementMu.Unlock()
		e.mu.Lock()
		defer e.mu.Unlock()
		if len(e.prepared) != 0 {
			return ChangeSet{}, fmt.Errorf("stuntgres: cannot replace catalog while prepared transactions exist")
		}
		if err := ctx.Err(); err != nil {
			return ChangeSet{}, err
		}
		if err := e.checkTemporaryRelationSourcesReplacementLocked(); err != nil {
			return ChangeSet{}, err
		}
		if replacement.Epoch() != e.catalog.state.Epoch() {
			return ChangeSet{}, ids.ErrCatalogReplaced
		}
		next = catalog.CloneState(replacement.State())
		if err := copyRows(e); err != nil {
			return ChangeSet{}, err
		}
		var sequenceErr error
		sequences, sequenceErr = sequence.CopyGenerators(replacement, e.storage.Sequences)
		if sequenceErr != nil {
			return ChangeSet{}, sequenceErr
		}
		if err := e.preserveTemporaryLocked(ctx, next, relations, sequences, restoreOp, indexes); err != nil {
			return ChangeSet{}, err
		}
		if err := catalog.ValidateDefaultACLs(e.roles.read(e.catalog.owner(), e.catalog.read(next), e.roles.published), e.catalog.read(next)); err != nil {
			return ChangeSet{}, err
		}
		nextRevision := e.catalog.nextRevision
		for _, d := range next.Descriptors() {
			if err := access.ValidateACL(e.roles.read(e.catalog.owner(), e.catalog.read(next), e.roles.published), d); err != nil {
				return ChangeSet{}, err
			}
			nextRevision, _ = catalog.AdvanceRevisionFrontier(nextRevision, d)
		}
		if e.catalog.state.Epoch() == ids.CatalogEpoch(math.MaxUint64) {
			return ChangeSet{}, fmt.Errorf("stuntgres: catalog epoch exhausted")
		}
		var imported uint64
		for _, relation := range relations {
			imported += uint64(len(relation.Versions))
		}
		if imported > ^uint64(0)-e.storage.RowFrontier() {
			return ChangeSet{}, fmt.Errorf("stuntgres: row identity exhausted")
		}
		if imported > ^uint64(0)-e.storage.VersionFrontier() {
			return ChangeSet{}, fmt.Errorf("stuntgres: version identity exhausted")
		}
		if e.visibility.CommitSeq() == ids.CommitSeq(^uint64(0)) {
			return ChangeSet{}, fmt.Errorf("stuntgres: commit sequence exhausted")
		}
		if hasHierarchy {
			candidate := e.catalog.read(next)
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
		if err := ctx.Err(); err != nil {
			return ChangeSet{}, err
		}
		e.storage.SetFrontierLocked(nextRow, nextVersion)
		e.storage.ReplaceIndexRootsLocked(roots)
		e.visibility.AdvanceCommitSeqLocked()
		next.SetEpoch(e.catalog.state.Epoch() + 1)
		e.catalog.state = next
		e.storage.ReplaceRelationsLocked(relations)
		e.databaseFrozenXID = 0
		e.vacuumProgress = nil
		for _, store := range relations {
			e.initializeMaintenanceLocked(store)
		}
		e.updateDatabaseMaintenanceLocked()
		e.exclusions.ResetLocked()
		e.storage.ReplaceSequencesLocked(sequences)
		e.clearPersistentSequenceMemoryLocked()
		e.catalog.nextRevision = nextRevision
		e.fenceSerializableLocked()
		e.visibility.RetireCommitTimestampsLocked()
		installed = next.Epoch()
		return ChangeSet{Replacement: true}, nil
	}
	source.mu.Lock()
	if replacement.Epoch() != source.catalog.state.Epoch() {
		source.mu.Unlock()
		return ChangeSet{}, ids.ErrCatalogReplaced
	}
	err = copyRows(source)
	if err == nil {
		sequences, err = sequence.CopyGenerators(replacement, source.storage.Sequences)
	}
	sourceRoles := source.roles.read(source.catalog.owner(), replacement, source.roles.published)
	sourceForeign := source.foreign
	source.mu.Unlock()
	if err != nil {
		return ChangeSet{}, err
	}
	var imported uint64
	for _, relation := range relations {
		imported += uint64(len(relation.Versions))
	}
	e.replacementMu.Lock()
	defer e.replacementMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.prepared) != 0 {
		return ChangeSet{}, fmt.Errorf("stuntgres: cannot replace catalog while prepared transactions exist")
	}
	if err := ctx.Err(); err != nil {
		return ChangeSet{}, err
	}
	if err := e.checkTemporaryRelationSourcesReplacementLocked(); err != nil {
		return ChangeSet{}, err
	}
	if e.catalog.state.Epoch() == ids.CatalogEpoch(math.MaxUint64) {
		return ChangeSet{}, fmt.Errorf("stuntgres: catalog epoch exhausted")
	}
	if imported > ^uint64(0)-e.storage.RowFrontier() {
		return ChangeSet{}, fmt.Errorf("stuntgres: row identity exhausted")
	}
	if imported > ^uint64(0)-e.storage.VersionFrontier() {
		return ChangeSet{}, fmt.Errorf("stuntgres: version identity exhausted")
	}
	if e.visibility.CommitSeq() == ids.CommitSeq(^uint64(0)) {
		return ChangeSet{}, fmt.Errorf("stuntgres: commit sequence exhausted")
	}
	replacement.State().SetACLItemImport(e.aclItemImporter(source, sourceRoles, targetRoles, source.importTargets))
	defer func() { replacement.State().SetACLItemImport(nil) }()
	next, err = e.remapPreparedCatalog(replacement.State(), sourceRoles, targetRoles, source.importTargets)
	if err != nil {
		return ChangeSet{}, err
	}
	if err = e.preserveTemporaryLocked(ctx, next, relations, sequences, restoreOp, indexes); err != nil {
		return ChangeSet{}, err
	}
	if err := catalog.ValidateDefaultACLs(e.roles.read(e.catalog.owner(), e.catalog.read(next), e.roles.published), e.catalog.read(next)); err != nil {
		return ChangeSet{}, err
	}
	nextRevision := e.catalog.nextRevision
	for _, d := range next.Descriptors() {
		if err := access.ValidateACL(e.roles.read(e.catalog.owner(), e.catalog.read(next), e.roles.published), d); err != nil {
			return ChangeSet{}, err
		}
		nextRevision, _ = catalog.AdvanceRevisionFrontier(nextRevision, d)
	}
	prepared, err := e.prepareForeignIndexStorage(ctx, next, relations, indexes)
	if err != nil {
		return ChangeSet{}, err
	}
	if err := e.installForeignCatalogLocked(ctx, next, relations, sequences, nextRevision, prepared); err != nil {
		return ChangeSet{}, err
	}
	e.foreign.AdoptNamespaces(sourceForeign)
	installed = next.Epoch()
	return ChangeSet{Replacement: true}, nil
}

// installForeignCatalogLocked owns the final cancellation boundary and the
// indivisible installation of a prepared foreign generation. The caller holds
// replacementMu and mu and has validated capacity, descriptors and authority.
// Storage vectors and sequence generator scalars are private copies; payload
// values remain immutable. All prepared containers publish at the same boundary.
func (e *Engine) installForeignCatalogLocked(ctx context.Context, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, nextRevision ids.DefinitionRevision, prepared index.RemintedIndexStorage) error {
	if prepared.Catalog != next || prepared.BaseCatalog != e.catalog.state || prepared.BaseRow != e.storage.RowFrontier() || prepared.BaseVersion != e.storage.VersionFrontier() || prepared.Frontier != e.visibility.CommitSeq() {
		return ids.ErrCatalogReplaced
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.storage.SetFrontierLocked(prepared.NextRow, prepared.NextVersion)
	e.storage.ReplaceIndexRootsLocked(prepared.Roots)
	e.visibility.AdvanceCommitSeqLocked()
	next.SetEpoch(e.catalog.state.Epoch() + 1)
	e.catalog.state = next
	e.storage.ReplaceRelationsLocked(relations)
	e.databaseFrozenXID = 0
	e.vacuumProgress = nil
	for _, store := range relations {
		e.initializeMaintenanceLocked(store)
	}
	e.updateDatabaseMaintenanceLocked()
	e.exclusions.ResetLocked()
	e.storage.ReplaceSequencesLocked(sequences)
	e.clearPersistentSequenceMemoryLocked()
	e.catalog.nextRevision = nextRevision
	e.fenceSerializableLocked()
	e.visibility.RetireCommitTimestampsLocked()
	return nil
}

func (e *Engine) RegisterCatalogProvider(p catalog.Provider) error {
	if p == nil {
		return fmt.Errorf("stuntgres: nil catalog provider")
	}
	id := p.ClassID()
	if id == 0 {
		return fmt.Errorf("stuntgres: zero catalog class")
	}
	if e.mu == nil {
		return fmt.Errorf("stuntgres: engine is not initialized")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.catalog.registerProviderLocked(id, p)
}
