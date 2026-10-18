package engine

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

// domainOf returns the engine's domain, or nil for a nil engine, so a
// provenance comparison against a possibly-nil engine keeps its meaning.
func domainOf(e *Engine) *ids.Domain {
	if e == nil {
		return nil
	}
	return e.catalog.Domain
}

// One immutable physical head per logical row, plus immutable end markers.
// Published vectors/end roots are retained by views; generation guards either.
type relationStorage struct {
	versions    []rowVersion
	ends        *deletionRoot
	generation  uint64
	indexSource *relationStorage // Origin only on detached index-build captures.
	// Metadata only, accessed under Engine.mu. Rows/end vectors are never
	// changed when this first committed TRUNCATE stamps its predecessor.
	truncateLineage *truncateLineage
	// Successful concurrent MV publication stamps both incarnations atomically.
	// It does not share TRUNCATE's current-root/birth-filter semantics.
	materializedLineage *materializedLineage
}

type Descriptor interface {
	Address() ids.ObjectAddress
	Revision() ids.DefinitionRevision
	Namespace() ids.ObjectAddress
	Name() string
	Kind() ids.ObjectKind
}
type namespaceDescriptor struct {
	address        ids.ObjectAddress
	name           string
	revision       ids.DefinitionRevision
	Owner          authorization.PrincipalRef
	ACL            authorization.ACLValue
	Comment        string
	Temporary      ids.SessionToken
	TemporaryToast bool
}

func (d *namespaceDescriptor) Address() ids.ObjectAddress       { return d.address }
func (d *namespaceDescriptor) Revision() ids.DefinitionRevision { return d.revision }
func (*namespaceDescriptor) Namespace() ids.ObjectAddress       { return ids.ObjectAddress{} }
func (d *namespaceDescriptor) Name() string                     { return d.name }
func (*namespaceDescriptor) Kind() ids.ObjectKind               { return ids.NamespaceObject }

type relationDescriptor struct {
	address, namespace   ids.ObjectAddress
	name                 string
	revision             ids.DefinitionRevision
	SequencePlanStamp    ids.DefinitionRevision
	Columns              []ColumnDescriptor
	Index                map[string]int
	Attributes           []int32
	AttributeHighWater   int32
	DroppedAttributes    map[int32]attributeMetadata
	CatalogAttributes    []catalogAttribute
	Owner                authorization.PrincipalRef
	ACL                  authorization.ACLValue
	ColumnACL            map[int32]authorization.ACLValue
	Comment              string
	ColumnComments       map[int32]string
	NotNullConstraints   []notNullConstraint
	PhysicalNotNull      map[int32]physicalNotNullProof
	CheckConstraints     []checkConstraint
	Policies             []policyDefinition
	Rules                rulesPayload
	RLSEnabled           bool
	RLSForced            bool
	UniqueConstraints    []uniqueConstraint
	ExclusionConstraints []exclusionConstraint
	ForeignKeys          []foreignKeyConstraint
	Defaults             map[int32]columnDefault
	Generations          map[int32]columnGeneration
	Identities           map[int32]columnIdentity
	Inheritance          *tableInheritance
	RelationKind         relationKind
	HasIndexes           bool                // Sticky publication history; removing an index does not clear it.
	HasSubclasses        bool                // Sticky inheritance/table/index partition publication history.
	SubclassPredecessor  *relationDescriptor // Exact first-subclass marker publication only.
	SubclassPublication  ids.DefinitionRevision
	OnCommit             parser.OnCommitAction
	Sequence             *sequenceOptions
	View                 viewPayload
	Populated            bool                // Independent of stored row count, including populated-empty MVs.
	AutoExtensions       []ids.ObjectAddress // Immutable whole-object AUTO_EXTENSION authority.
	VirtualView          bool
	SQLIndex             *indexDefinition
	Partition            *partitionDefinition
	Membership           *partitionMembership
	RowType              sqltype.ID
	OfType               ids.ObjectAddress
	Foreign              *foreignTableDefinition
}

func (d *relationDescriptor) Address() ids.ObjectAddress       { return d.address }
func (d *relationDescriptor) Revision() ids.DefinitionRevision { return d.revision }
func (d *relationDescriptor) Namespace() ids.ObjectAddress     { return d.namespace }
func (d *relationDescriptor) Name() string                     { return d.name }
func (*relationDescriptor) Kind() ids.ObjectKind               { return ids.RelationObject }

func (d *relationDescriptor) hasAttribute(sub int32) bool {
	for _, live := range d.Attributes {
		if live == sub {
			return true
		}
	}
	return false
}

type notNullConstraint struct {
	OID            ids.OID
	Name           string
	Attribute      int32
	NotValid       bool
	NoInherit      bool
	PartitionLocal bool // Explicit NN declaration merged with an inherited definition.
}

type catalogState struct {
	epoch               ids.CatalogEpoch
	namespaceGeneration uint64
	descriptors         map[ids.ObjectAddress]Descriptor
	names               map[ids.NameKey]ids.ObjectAddress
	operatorNames       *operatorNameIndex
	defaultACLs         map[defaultACLKey]*defaultACLRecord
	// Sparse replay identity provenance for collision-remapped declarations.
	// Absent entries use their current OID; dependencies remain the order authority.
	dumpPriorities map[ids.ObjectAddress]ids.OID
	// Replacement-candidate-only translation, cleared before publication.
	// Keys are exact source codecs, never numeric identity coincidences.
	scalarImports map[*sqltype.Descriptor]*sqltype.Descriptor
	aclItemImport func(sqltype.Value) (sqltype.Value, error)
}
type CatalogRead struct {
	owner *catalogOwner
	state *catalogState
}

// Domain returns the provenance domain of the view's owner, or nil for a
// zero view.
func (r CatalogRead) Domain() *ids.Domain {
	if r.owner == nil {
		return nil
	}
	return r.owner.Domain
}

func (r CatalogRead) Epoch() ids.CatalogEpoch {
	if r.state == nil {
		return 0
	}
	return r.state.epoch
}
func (r CatalogRead) Descriptor(a ids.ObjectAddress) (Descriptor, bool) {
	if r.state == nil {
		return nil, false
	}
	d, ok := r.Find(a)
	return d, ok
}
func (r CatalogRead) Lookup(k ids.NameKey) []ids.ObjectAddress {
	if r.state == nil {
		return nil
	}
	a, ok := r.LookupName(k)
	if !ok {
		return nil
	}
	return []ids.ObjectAddress{a}
}

type CatalogProvider interface {
	ClassID() ids.OID
	Validate(CatalogRead, Descriptor) error
}
type namespaceProvider struct{}

func (namespaceProvider) ClassID() ids.OID { return ids.NamespaceClassID }
func (namespaceProvider) Validate(v CatalogRead, d Descriptor) error {
	ns, ok := d.(*namespaceDescriptor)
	if !ok || ns == nil || ns.TemporaryToast && ns.Temporary == (ids.SessionToken{}) || v.state == nil || v.Get(ns.address) != ns {
		return fmt.Errorf("stuntgres: invalid namespace descriptor authority")
	}
	if ns.Temporary != (ids.SessionToken{}) && (ns.Temporary.Domain() != v.Domain() || ns.Temporary.ID() == 0 || privateSessionOf(ns.Temporary) != nil) {
		return fmt.Errorf("stuntgres: invalid temporary namespace provenance")
	}
	a := d.Address()
	key := descriptorNameKey(d)
	found := v.Lookup(key)
	if d.Kind() != ids.NamespaceObject || a.ClassID != ids.NamespaceClassID || a.ObjectID == 0 || a.SubID != 0 || d.Revision() == 0 || d.Namespace() != (ids.ObjectAddress{}) || len(found) != 1 || found[0] != a {
		return fmt.Errorf("stuntgres: invalid namespace descriptor")
	}
	return nil
}

type relationProvider struct{}

func (relationProvider) ClassID() ids.OID { return ids.RelationClassID }
func (relationProvider) Validate(v CatalogRead, d Descriptor) error {
	r, ok := d.(*relationDescriptor)
	if !ok || d.Kind() != ids.RelationObject || d.Address().ClassID != ids.RelationClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 {
		return fmt.Errorf("stuntgres: invalid relation descriptor")
	}
	ns, ok := v.Descriptor(r.namespace)
	if !ok || ns.Kind() != ids.NamespaceObject {
		return fmt.Errorf("stuntgres: relation namespace is missing")
	}
	if r.OnCommit > parser.OnCommitDrop || r.OnCommit != parser.OnCommitUnspecified && (r.RelationKind != tableRelation || ns.(*namespaceDescriptor).Temporary == (ids.SessionToken{})) {
		return fmt.Errorf("stuntgres: invalid temporary table commit action")
	}
	found := v.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: r.namespace, Name: r.name})
	if len(found) != 1 || found[0] != r.address || len(r.Columns) != len(r.Attributes) || len(r.Index) != len(r.Columns) {
		return fmt.Errorf("stuntgres: invalid relation identity")
	}
	if r.VirtualView && r.RelationKind != catalogRelation {
		return fmt.Errorf("stuntgres: virtual view requires a catalog source")
	}
	if r.Populated && r.RelationKind != materializedViewRelation || r.View != nil && r.RelationKind != viewRelation && r.RelationKind != materializedViewRelation {
		return fmt.Errorf("stuntgres: invalid stored definition kind")
	}
	if len(r.AutoExtensions) != 0 && r.RelationKind != materializedViewRelation {
		return fmt.Errorf("stuntgres: extension dependency requires a materialized view")
	}
	for i, address := range r.AutoExtensions {
		if address.ClassID != ids.ExtensionClassID || address.ObjectID == 0 || address.SubID != 0 || i > 0 && r.AutoExtensions[i-1].ObjectID >= address.ObjectID {
			return fmt.Errorf("stuntgres: invalid materialized extension dependency")
		}
		extension, ok := v.Extension(address)
		if !ok {
			return fmt.Errorf("stuntgres: missing materialized extension provider")
		}
		if err := (extensionCatalogProvider{class: ids.ExtensionClassID}).Validate(v, extension); err != nil {
			return err
		}
	}
	if r.RelationKind.stored() || r.RelationKind == partitionedRelation || r.RelationKind == viewRelation || r.RelationKind == compositeRelation || r.RelationKind == foreignRelation {
		types := v.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: r.namespace, Name: r.name})
		if len(types) != 0 {
			row, ok := v.Type(types[0])
			if len(types) != 1 || !ok || row.OwningRelation != r.address || row.SQLType.ID() != r.RowType {
				return CatalogConflict{}
			}
		}
	}
	if r.RelationKind == foreignRelation {
		if r.Foreign == nil {
			return fmt.Errorf("stuntgres: missing foreign relation definition")
		}
		server, ok := v.Foreign(r.Foreign.Server)
		if !ok || server.address.ClassID != ids.ForeignServerClassID {
			return fmt.Errorf("stuntgres: missing foreign server identity")
		}
	} else if r.Foreign != nil {
		return fmt.Errorf("stuntgres: foreign definition on a local relation")
	}
	if r.AttributeHighWater < 0 {
		return fmt.Errorf("stuntgres: invalid relation attribute high-water")
	}
	if r.RelationKind == catalogRelation && len(r.CatalogAttributes) != len(r.Columns) {
		return fmt.Errorf("stuntgres: invalid catalog attribute metadata")
	}
	for number, history := range r.DroppedAttributes {
		if number <= 0 || number > r.AttributeHighWater || history.Number != number || !history.Dropped || history.TypeID != 0 || history.NotNull || history.HasDefault || history.Generated != "" || history.Dimensions < 0 || r.indexForAttribute(number) >= 0 {
			return fmt.Errorf("stuntgres: invalid dropped attribute metadata")
		}
	}
	for i, c := range r.Columns {
		offset, exists := r.Index[c.Name]
		ordered := i == 0 || r.Attributes[i] > r.Attributes[i-1]
		if r.RelationKind == catalogRelation {
			ordered = r.Attributes[i] == catalogAttributeNumber(r.address.ObjectID, c.Name, int32(i+1))
		}
		if r.Attributes[i] <= 0 || r.Attributes[i] > r.AttributeHighWater || !ordered || !exists || offset != i {
			return fmt.Errorf("stuntgres: invalid relation column identity")
		}
		if c.Usage.Type == nil || c.DeclaredDimensions < 0 {
			return fmt.Errorf("stuntgres: invalid relation column type")
		}
		typ, exists := v.LookupType(c.Usage.Type.ID())
		catalogNull := r.RelationKind == catalogRelation && r.address.ObjectID == pgAttributeOID && r.Attributes[i] == 25 && c.Name == "attmissingval" && c.Usage.CatalogNullUsage()
		if !exists || typ != c.Usage.Type || !catalogNull && (!typ.Concrete() || !c.Usage.Valid()) {
			return fmt.Errorf("stuntgres: invalid relation column type")
		}
		if err := validateUsageCollation(v, c.Usage); err != nil {
			return err
		}
	}
	if (r.RLSEnabled || r.RLSForced) && r.RelationKind != tableRelation && r.RelationKind != partitionedRelation {
		return fmt.Errorf("stuntgres: row-security flags require a table")
	}
	if err := validatePolicies(v, r); err != nil {
		return err
	}
	if err := validateRules(v, r); err != nil {
		return err
	}
	if err := validateTypedAttachment(v, r); err != nil {
		return err
	}
	if err := validateInheritance(v, r); err != nil {
		return err
	}
	if r.RelationKind == compositeRelation {
		if r.RowType == 0 || r.Sequence != nil || r.View != nil || r.SQLIndex != nil || len(r.Defaults) != 0 || len(r.Identities) != 0 || len(r.NotNullConstraints) != 0 || len(r.CheckConstraints) != 0 || len(r.Generations) != 0 || len(r.UniqueConstraints) != 0 || len(r.ExclusionConstraints) != 0 || len(r.ForeignKeys) != 0 {
			return fmt.Errorf("stuntgres: composite backing relation has storage behavior")
		}
		for _, c := range r.Columns {
			if !c.Nullable {
				return fmt.Errorf("stuntgres: composite member has table constraints")
			}
		}
		return nil
	}
	if r.SQLIndex != nil && r.RelationKind != indexRelation {
		return fmt.Errorf("stuntgres: index definition requires an index relation")
	}
	if r.RelationKind == indexRelation {
		return validateIndexDefinition(v, r)
	}
	if r.RelationKind != tableRelation && r.RelationKind != partitionedRelation && r.RelationKind != foreignRelation && len(r.CheckConstraints) != 0 {
		return fmt.Errorf("stuntgres: CHECK requires a table descriptor")
	}
	if err := validatePartitionDescriptor(v, r); err != nil {
		return err
	}
	if r.RelationKind == sequenceRelation {
		if r.SequencePlanStamp == 0 || r.Sequence == nil || len(r.Columns) != 3 || r.Columns[0].Name != "last_value" || r.Columns[1].Name != "log_cnt" || r.Columns[2].Name != "is_called" || r.Columns[0].Usage.Type.ID() != sqltype.Int8 || r.Columns[1].Usage.Type.ID() != sqltype.Int8 || r.Columns[2].Usage.Type.ID() != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid sequence descriptor")
		}
		if _, _, err := bindSequenceOptions(r.Sequence, sequenceGenerator{last: r.Sequence.Start}, nil); err != nil {
			return err
		}
		if r.Sequence.Identity && r.Sequence.Owned == (ids.ObjectAddress{}) {
			return fmt.Errorf("stuntgres: identity sequence lacks ownership")
		}
		if owned := r.Sequence.Owned; owned != (ids.ObjectAddress{}) {
			tableAddress := owned
			tableAddress.SubID = 0
			table, ok := v.Relation(tableAddress)
			if !ok || table.RelationKind != tableRelation && table.RelationKind != partitionedRelation || !table.hasAttribute(owned.SubID) || table.Owner != r.Owner || table.namespace != r.namespace {
				return fmt.Errorf("stuntgres: invalid sequence ownership dependency")
			}
			if r.Sequence.Identity {
				identity, exists := table.Identities[owned.SubID]
				if !exists || identity.Sequence != r.address {
					return fmt.Errorf("stuntgres: inconsistent identity sequence ownership")
				}
			}
		}
		return nil
	}
	if r.RelationKind == viewRelation || r.RelationKind == materializedViewRelation {
		return validateViewDefinition(v, r)
	}
	names := make(map[string]bool, len(r.NotNullConstraints))
	attrs := make(map[int32]bool, len(r.NotNullConstraints))
	for _, c := range r.NotNullConstraints {
		if c.OID == 0 || c.Name == "" || len(c.Name) > 63 || !utf8.ValidString(c.Name) || strings.IndexByte(c.Name, 0) >= 0 || names[c.Name] || attrs[c.Attribute] || !r.hasAttribute(c.Attribute) {
			return fmt.Errorf("stuntgres: invalid not-null constraint identity")
		}
		names[c.Name], attrs[c.Attribute] = true, true
	}
	if err := validatePhysicalNotNull(v, r, attrs); err != nil {
		return err
	}
	for i, c := range r.Columns {
		// Virtual sources declare result nullability without physical table
		// constraint identities. Existing constraint records still validate above.
		_, physical := r.PhysicalNotNull[r.Attributes[i]]
		if r.RelationKind != catalogRelation && (attrs[r.Attributes[i]] || physical) != (c.NullabilityKnown && !c.Nullable) {
			return fmt.Errorf("stuntgres: inconsistent not-null projection")
		}
	}
	if err := validateChecks(v, r, names); err != nil {
		return err
	}
	if err := validateUniques(v, r, names); err != nil {
		return err
	}
	if err := validateExclusions(v, r, names); err != nil {
		return err
	}
	if err := validateForeignKeys(v, r, names); err != nil {
		return err
	}
	for attribute, identity := range r.Identities {
		slot := -1
		for i, live := range r.Attributes {
			if live == attribute {
				slot = i
				break
			}
		}
		if slot < 0 || identity.Mode != parser.IdentityAlways && identity.Mode != parser.IdentityByDefault {
			return fmt.Errorf("stuntgres: invalid column identity")
		}
		column := r.Columns[slot]
		id := column.Usage.Type.ID()
		if column.Nullable || !column.NullabilityKnown || id != sqltype.Int2 && id != sqltype.Int4 && id != sqltype.Int8 {
			return fmt.Errorf("stuntgres: invalid identity column type or nullability")
		}
		if _, exists := r.Defaults[attribute]; exists {
			return fmt.Errorf("stuntgres: identity column has default")
		}
		sequence, exists := v.Relation(identity.Sequence)
		owned := r.address
		owned.SubID = attribute
		if !exists || sequence.Sequence == nil || !sequence.Sequence.Identity || sequence.Sequence.Owned != owned || sequence.Owner != r.Owner || sequence.namespace != r.namespace {
			return fmt.Errorf("stuntgres: invalid column identity sequence")
		}
	}
	for attribute, definition := range r.Defaults {
		slot := -1
		for i, live := range r.Attributes {
			if live == attribute {
				slot = i
				break
			}
		}
		if definition.OID == 0 || slot < 0 || definition.Expression == nil || definition.Expression.ResultUsage() != r.Columns[slot].Usage {
			return fmt.Errorf("stuntgres: invalid column default destination")
		}
		var dependencies []ids.ObjectRef
		catalogSnapshot := false
		_, err := (scalarRenderer{validation: true, provenance: r, capturedManifest: definition.Dependencies, dependencies: &dependencies, commentSnapshot: &catalogSnapshot}).sql(context.Background(), v, definition.Expression)
		if err != nil || !slices.Equal(dependencies, definition.Dependencies) || catalogSnapshot != definition.CatalogSnapshot || structuralNull(definition.Expression) {
			return fmt.Errorf("stuntgres: invalid column default expression")
		}
	}
	for attribute, definition := range r.Generations {
		slot := -1
		for i, live := range r.Attributes {
			if live == attribute {
				slot = i
				break
			}
		}
		_, hasDefault := r.Defaults[attribute]
		_, hasIdentity := r.Identities[attribute]
		if definition.OID == 0 || slot < 0 || definition.Expression == nil || definition.Expression.ResultUsage() != r.Columns[slot].Usage || definition.Planned == nil || definition.Planned.ResultUsage() != r.Columns[slot].Usage || hasDefault || hasIdentity || !generationImmutable(definition.Planned) {
			return fmt.Errorf("stuntgres: invalid column generation destination")
		}
		if definition.Kind != generationStored && definition.Kind != generationVirtual {
			return fmt.Errorf("stuntgres: invalid column generation kind")
		}
		if definition.Kind == generationVirtual {
			if err := virtualDestination(r.Columns[slot]); err != nil {
				return err
			}
			if err := virtualGenerationSecurity(definition.Expression); err != nil {
				return err
			}
			if err := virtualGenerationSecurity(definition.Planned); err != nil {
				return err
			}
		}
		if err := validateGenerationPlan(v, r, definition.Planned); err != nil {
			return err
		}
		var dependencies []ids.ObjectRef
		catalogSnapshot := false
		_, err := (scalarRenderer{validation: true, owner: r, capturedManifest: definition.Dependencies, dependencies: &dependencies, commentSnapshot: &catalogSnapshot}).sql(context.Background(), v, definition.Expression)
		if err != nil || !slices.Equal(dependencies, definition.Dependencies) || catalogSnapshot != definition.CatalogSnapshot {
			return fmt.Errorf("stuntgres: invalid column generation expression")
		}
	}
	if err := validateGenerationProviders(v, r); err != nil {
		return err
	}
	for sub, text := range r.ColumnComments {
		if !r.hasAttribute(sub) || text == "" {
			return fmt.Errorf("stuntgres: invalid relation column comment")
		}
	}
	return nil
}

type PublicationToken struct{ Epoch ids.CatalogEpoch }
type ChangeSet struct {
	Definitions []ids.ObjectAddress
	Statistics  []ids.ObjectAddress
	Namespaces  []ids.ObjectAddress
	Replacement bool
}
type CatalogConflict struct{}

func (CatalogConflict) Error() string { return "stuntgres: catalog publication conflict" }

type catalogLifecycle uint8

const (
	catalogOpen catalogLifecycle = iota
	catalogValidated
	catalogPublished
	catalogDiscarded
)

type catalogRemoval struct {
	ref        ids.ObjectRef
	descriptor Descriptor
}

type catalogRowShape struct {
	original       *relationStorage
	root           []rowVersion
	replacement    *relationStorage
	observedLength int
	observedEnds   *deletionRoot
	generation     uint64
}

type CatalogStage struct {
	owner                *Engine
	base                 CatalogRead
	descriptors          map[ids.ObjectAddress]Descriptor
	names                map[ids.NameKey]ids.ObjectAddress
	defaultACLs          map[defaultACLKey]*defaultACLRecord
	defaultReads         map[defaultACLKey]*defaultACLRecord
	touched              map[ids.NameKey]ids.ObjectAddress
	removals             map[ids.ObjectAddress]catalogRemoval
	rowShapes            map[ids.ObjectAddress]catalogRowShape
	partitionReads       []partitionReadFence
	indexRebuilds        map[ids.ObjectAddress]*relationDescriptor
	indexCandidates      map[ids.ObjectAddress]indexRootCandidate
	indexLayouts         map[ids.ObjectAddress]indexLayoutLineage
	indexParents         map[ids.ObjectAddress]*relationDescriptor
	indexSets            map[ids.ObjectAddress]int
	indexOutput          sqltype.OutputEnvironment
	oldNames             map[ids.NameKey]bool
	generators           map[ids.ObjectAddress]*sequenceGenerator
	identityOriginals    map[ids.ObjectAddress]Descriptor
	identityDependencies []catalogDependencyObservation
	typedSource          ids.ObjectAddress
	typedObserved        map[ids.ObjectAddress]Descriptor
	lifecycle            catalogLifecycle
	invalid              error
	authSession          *Session
	authRevision         uint64
	roleRevision         uint64
	extensionNamespace   ids.ObjectAddress
	databasePredecessor  Descriptor
	databaseAuthority    Descriptor
	// Optional DROP OWNED membership deletions join this publication, not a
	// separate role installation. Every other catalog caller leaves it nil.
	roleCandidate      *roleCatalog
	schemaCascadeNames []string
	viewCascadeNames   []string
	dependencyPath     []ids.ObjectAddress
	domainTargets      []ids.ObjectAddress
	domainBases        []Descriptor
	domainValidation   *domainValidationFence
	domainAlter        context.Context
	domainIndexRoots   map[ids.ObjectAddress]*logicalIndexRoot
	// Physical retirement alone may saturate dependent descriptor/store/index
	// counters; fresh identities and exact predecessor fences remain mandatory.
	terminalCleanup bool
	// New query-derived tables carry private initial storage, never replacements
	// of live storage. Publication installs the definition and data together.
	initialTables           map[ids.ObjectAddress]*relationStorage
	initialTx               ids.TxRef
	materializedReplacement *materializedReplacement
	querySources            []Descriptor
	materializedSourceView  *visibilityData
	likeSources             []Descriptor // CREATE-only exact descriptor captures, not dependencies.
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

func (e *Engine) BeginCatalogStage(base CatalogRead) *CatalogStage {
	s := &CatalogStage{owner: e, base: base, descriptors: make(map[ids.ObjectAddress]Descriptor), names: make(map[ids.NameKey]ids.ObjectAddress), touched: make(map[ids.NameKey]ids.ObjectAddress)}
	if base.owner != e.catalog.owner() || base.state == nil {
		s.invalid = fmt.Errorf("stuntgres: foreign catalog view")
	}
	return s
}

// Retain selected private authority separately from the committed predecessor
// already captured by authorizationLocked; building a stage needs no live read.
func (s *CatalogStage) authorizeLocked(auth AuthorizationView, session *Session) {
	s.authSession, s.authRevision, s.roleRevision = session, auth.revision, auth.roleRecord.revision
	s.databaseAuthority = auth.catalog.Get(currentDatabaseAddress)
	s.databasePredecessor = auth.databasePredecessor
}

// lookupName resolves the private candidate, including names vacated by a
// replacement. The original name remains in touched for publication validation.
func (s *CatalogStage) lookupName(k ids.NameKey) ids.ObjectAddress {
	if address := s.names[k]; address != (ids.ObjectAddress{}) {
		if _, removed := s.removals[address]; !removed {
			return address
		}
	}
	address := s.base.NameAddress(k)
	if _, removed := s.removals[address]; removed {
		return ids.ObjectAddress{}
	}
	if replacement := s.descriptors[address]; replacement != nil &&
		descriptorNameKey(replacement) != k {
		return ids.ObjectAddress{}
	}
	return address
}

func (s *CatalogStage) Create(d Descriptor) error {
	if s == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	if d == nil {
		return fmt.Errorf("stuntgres: nil catalog descriptor")
	}
	a := d.Address()
	if a.ObjectID == 0 || a.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid catalog object address")
	}
	if _, ok := s.base.Find(a); ok {
		return CatalogConflict{}
	}
	if _, ok := s.descriptors[a]; ok {
		return CatalogConflict{}
	}
	k := descriptorNameKey(d)
	// One live-name check includes staged replacements and excludes removed
	// generations, whose private name slots can be reused by a new identity.
	if s.lookupName(k) != (ids.ObjectAddress{}) {
		return CatalogConflict{}
	}
	s.touched[k] = s.base.NameAddress(k)
	s.descriptors[a] = d
	s.names[k] = a
	return nil
}
func (s *CatalogStage) Replace(d Descriptor) error {
	return s.replaceDescriptor(d, true)
}

// A fully rebuilt immutable type closure is already reciprocal. Installing its
// members must not synthesize a second projection based on map iteration order.
func (s *CatalogStage) replaceDescriptor(d Descriptor, synchronize bool) error {
	if s == nil || s.lifecycle != catalogOpen || s.invalid != nil || d == nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	old, ok := s.base.Descriptor(d.Address())
	if !ok {
		return CatalogConflict{}
	}
	if previous, ok := old.(*relationDescriptor); ok && previous.HasIndexes {
		if next, ok := d.(*relationDescriptor); !ok || !next.HasIndexes {
			return fmt.Errorf("stuntgres: relation replacement clears index publication history")
		}
	}
	if previous, ok := old.(*relationDescriptor); ok && previous.HasSubclasses {
		if next, ok := d.(*relationDescriptor); !ok || !next.HasSubclasses {
			return fmt.Errorf("stuntgres: relation replacement clears subclass publication history")
		}
	}
	oldKey := descriptorNameKey(old)
	newKey := descriptorNameKey(d)
	if a := s.lookupName(newKey); a != (ids.ObjectAddress{}) && a != d.Address() {
		return CatalogConflict{}
	}
	if previous := s.descriptors[d.Address()]; previous != nil {
		delete(s.names, descriptorNameKey(previous))
	}
	if s.oldNames == nil {
		s.oldNames = make(map[ids.NameKey]bool)
	}
	s.oldNames[oldKey] = true
	s.touched[oldKey] = s.base.NameAddress(oldKey)
	s.touched[newKey] = s.base.NameAddress(newKey)
	s.descriptors[d.Address()] = d
	s.names[newKey] = d.Address()
	if relation, ok := d.(*relationDescriptor); ok && synchronize {
		return s.synchronizeRowType(relation)
	}
	return nil
}

// alterRelation couples a same-identity revision with an immutable storage root.
func (s *CatalogStage) alterRelation(ctx context.Context, ref ids.ObjectRef, d *relationDescriptor, original, replacement *relationStorage, observedLength int, observedGeneration uint64, observedEnds *deletionRoot) error {
	if s == nil || s.owner == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	e := s.owner
	e.mu.Lock()
	if err := e.catalog.validateObjectRefLocked(s.base, ref); err != nil {
		e.mu.Unlock()
		return err
	}
	old, ok := s.base.Relation(ref.Address)
	validRevision := ok && d != nil && old.revision != ids.DefinitionRevision(^uint64(0)) && d.revision == old.revision+1
	if s.terminalCleanup && ok && old.revision == ids.DefinitionRevision(^uint64(0)) {
		validRevision = d != nil && d != old && d.revision == old.revision
	}
	if !ok || ref.Address.SubID != 0 || d == nil || d.address != old.address || d.namespace != old.namespace || d.name != old.name || !validRevision || original == nil || replacement == nil || e.stageStorageLocked(s, ref.Address) != original {
		e.mu.Unlock()
		return CatalogConflict{}
	}
	if len(original.versions) != observedLength || len(replacement.versions) != observedLength || original.generation != observedGeneration || original.ends != observedEnds || replacement.ends != observedEnds {
		e.mu.Unlock()
		return CatalogConflict{}
	}
	if original == replacement && !slices.Equal(d.Attributes, old.Attributes) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: invalid metadata-only row shape")
	}
	beforeRows := original.versions
	e.mu.Unlock()
	if original != replacement {
		for i, row := range replacement.versions {
			if err := ctx.Err(); err != nil {
				return err
			}
			before := beforeRows[i]
			if len(row.values) != len(d.Columns) || row.row != before.row || row.version != before.version || row.born != before.born {
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
	case *relationDescriptor:
		if ref.Address.ClassID != ids.RelationClassID {
			return fmt.Errorf("stuntgres: only user relations can be removed")
		}
	case *namespaceDescriptor:
		if !namespace || ref.Address.ClassID != ids.NamespaceClassID {
			return fmt.Errorf("stuntgres: only user relations can be removed")
		}
	case *typeDescriptor:
		t := d.(*typeDescriptor)
		if t.OwningRelation == (ids.ObjectAddress{}) && !t.SQLType.IsUserDefined() && !(t.SQLType.IsArray() && t.SQLType.Element().IsUserDefined()) {
			return fmt.Errorf("stuntgres: builtin types cannot be removed")
		}
	case *collationDescriptor:
		if d.(*collationDescriptor).seeded() {
			return sqlError("2BP01", "cannot drop "+dependencyObject(provider.base, d.Address(), provider.path)+" because it is required by the database system")
		}
	case *conversionDescriptor:
		if d.(*conversionDescriptor).seeded() {
			return sqlError("2BP01", "cannot drop "+dependencyObject(provider.base, d.Address(), provider.path)+" because it is required by the database system")
		}
	case *operatorDescriptor:
		if d.(*operatorDescriptor).seeded() {
			return sqlError("2BP01", "cannot drop "+dependencyObject(provider.base, d.Address(), provider.path)+" because it is required by the database system")
		}
	case *foreignDescriptor:
		if err := provider.owner.catalog.provider(ref.Address.ClassID).Validate(provider.base, d); err != nil {
			return err
		}
	case *extensionDescriptor, *procedureDescriptor, *tsTemplateDescriptor, *tsDictionaryDescriptor:
		if procedure, ok := d.(*procedureDescriptor); ok && procedure.seeded() {
			return sqlError("2BP01", "cannot drop "+dependencyObject(provider.base, d.Address(), provider.path)+" because it is required by the database system")
		}
		objectProvider := provider.owner.catalog.provider(ref.Address.ClassID)
		if objectProvider == nil {
			return fmt.Errorf("stuntgres: missing installed object provider")
		}
		if err := objectProvider.Validate(provider.base, d); err != nil {
			return err
		}
	case *parameterDescriptor:
		if err := (parameterProvider{provider.owner.settingsDefinitions}).Validate(provider.base, d); err != nil {
			return err
		}
	case *tablespaceDescriptor:
		return sqlError("2BP01", "cannot drop tablespace "+dependencyIdentifier(d.Name())+" because it is required by the database system")
	default:
		return fmt.Errorf("stuntgres: only user relations can be removed")
	}
	// Provider/ref validation above belongs to the authentic predecessor.
	// AUTO retirement below follows the actual metadata entering this phase.
	d = provider.retirementDescriptor(ref.Address)
	if provider.removed(ref.Address) {
		return nil
	}
	if err := provider.recordRemoval(ref, namespace); err != nil {
		return err
	}
	if operator, ok := d.(*operatorDescriptor); ok {
		if err := provider.clearOperatorLinks(operator); err != nil {
			return err
		}
	}
	if typ, ok := d.(*typeDescriptor); ok && typ.SQLType.IsComposite() {
		if backing, ok := provider.retirementDescriptor(typ.OwningRelation).(*relationDescriptor); ok && backing.RelationKind == compositeRelation {
			if err := provider.removeObjectLocked(provider.owner.catalog.Domain.Ref(provider.base.Epoch(), backing.address), false); err != nil {
				return err
			}
		}
	}
	if relation, ok := d.(*relationDescriptor); ok {
		if err := provider.removePartitionHierarchyLocked(relation); err != nil {
			return err
		}
	}
	if r, ok := d.(*relationDescriptor); ok && r.RowType != 0 {
		row, ok := provider.retirementDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.RowType)}).(*typeDescriptor)
		if !ok || row.OwningRelation != r.address {
			return CatalogConflict{}
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
func (e *Engine) stageRead(s *CatalogStage) CatalogRead {
	ds := make(map[ids.ObjectAddress]Descriptor, s.base.DescriptorCount()+len(s.descriptors))
	ns := make(map[ids.NameKey]ids.ObjectAddress, s.base.NameCount()+len(s.names))
	operators := operatorIndexEdit{index: s.base.state.operatorNames}
	for k, v := range s.base.Descriptors() {
		ds[k] = v
	}
	for k, v := range s.base.Names() {
		ns[k] = v
	}
	for a, removal := range s.removals {
		d := removal.descriptor
		operators.replace(s.base.Get(a), s.descriptors[a])
		delete(ds, a)
		delete(ns, descriptorNameKey(d))
	}
	for k := range s.oldNames {
		delete(ns, k)
	}
	for k, v := range s.descriptors {
		if _, removed := s.removals[k]; !removed {
			operators.replace(s.base.Get(k), v)
		}
		ds[k] = v
	}
	for k, v := range s.names {
		ns[k] = v
	}
	priorities := s.base.state.dumpPriorities
	if len(priorities) != 0 && len(s.removals) != 0 {
		priorities = make(map[ids.ObjectAddress]ids.OID, len(s.base.state.dumpPriorities))
		for address, priority := range s.base.state.dumpPriorities {
			if _, exists := ds[address]; exists {
				priorities[address] = priority
			}
		}
	}
	return e.catalog.read(&catalogState{epoch: s.base.Epoch(), namespaceGeneration: s.base.state.namespaceGeneration, descriptors: ds, names: ns, operatorNames: operators.index, defaultACLs: overlayDefaultACLs(s.base.state.defaultACLs, s.defaultACLs), dumpPriorities: priorities})
}
func (e *Engine) ValidateCatalogStage(ctx context.Context, s *CatalogStage) (*PreparedCatalogPublication, error) {
	if s == nil || s.owner != e || s.lifecycle != catalogOpen || s.invalid != nil {
		return nil, fmt.Errorf("stuntgres: invalid catalog stage")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.validateInitialTables(); err != nil {
		return nil, err
	}
	if err := s.validateMaterializedReplacement(); err != nil {
		return nil, err
	}
	e.mu.Lock()
	if err := s.validateRoleCandidate(e.roles); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	for address, descriptor := range s.descriptors {
		typ, ok := descriptor.(*typeDescriptor)
		if !ok || !typ.SQLType.IsComposite() && !typ.SQLType.IsDomain() {
			continue
		}
		old, _ := s.base.Type(address)
		if old == nil || old.SQLType != typ.SQLType {
			if err := e.reownStagedTypeGenerationsLocked(s); err != nil {
				e.mu.Unlock()
				return nil, err
			}
			break
		}
	}
	if !e.partitionReadsValidLocked(s) {
		e.mu.Unlock()
		return nil, CatalogConflict{}
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
		if e.stageStorageLocked(s, address) != shape.original || shape.original.generation != shape.generation || len(shape.original.versions) != shape.observedLength {
			e.mu.Unlock()
			return nil, CatalogConflict{}
		}
	}
	if r := s.materializedReplacement; r != nil {
		if err := s.materializedReplacementCurrent(e.stageCatalogBaseLocked(s), e.stageStorageLocked(s, r.original.address)); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}
	e.mu.Unlock()
	var view CatalogRead
	if len(s.descriptors) != 0 {
		view = e.stageRead(s)
	}
	if view.state != nil {
		if err := validateDefinitionOIDs(view); err != nil {
			return nil, err
		}
	}
	for _, d := range s.descriptors {
		p := e.catalog.provider(d.Address().ClassID)
		if p == nil {
			return nil, fmt.Errorf("stuntgres: unregistered catalog class %d", d.Address().ClassID)
		}
		if err := p.Validate(view, d); err != nil {
			return nil, err
		}
		e.mu.Lock()
		roles := e.sessionRolesLocked(s.authSession)
		authErr := validateACL(s.roleRead(roles, view), d)
		e.mu.Unlock()
		if authErr != nil {
			return nil, authErr
		}
	}
	s.lifecycle = catalogValidated
	return &PreparedCatalogPublication{stage: s, epoch: s.base.Epoch(), ctx: ctx}, nil
}
func (e *Engine) PublishCatalog(p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	return e.publishCatalog(nil, nil, p, token)
}
func (e *Engine) publishCatalogOperation(op *Operation, scope *StatementScope, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	if op != nil && op.session.transactions.retainsTransaction() {
		return e.stageMessageCatalog(op, p, token)
	}
	if scope != nil && scope.owner.state == TxActive {
		if scope.op == nil || scope.op != op || p == nil || p.stage == nil || p.stage.owner != e || p.stage.lifecycle != catalogValidated {
			return ChangeSet{}, fmt.Errorf("stuntgres: invalid catalog publication")
		}
		if token.Epoch != p.epoch {
			return ChangeSet{}, ids.ErrCatalogReplaced
		}
		if err := e.stageCatalogOverlay(scope, p.stage, cloneCatalogChanges(scope.owner.catalog)); err != nil {
			return ChangeSet{}, err
		}
		return catalogStageChanges(p.stage), nil
	}
	return e.publishCatalog(op, scope, p, token)
}
func (e *Engine) publishCatalog(op *Operation, scope *StatementScope, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
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
		currentStorage := e.storage
		if p.epoch != current.epoch || token.Epoch != current.epoch {
			e.mu.Unlock()
			return ChangeSet{}, ids.ErrCatalogReplaced
		}
		if err := s.validateIndexMaintenance(current); err != nil {
			e.mu.Unlock()
			return ChangeSet{}, err
		}
		if !e.domainIndexRootsCurrent(s) {
			e.mu.Unlock()
			return ChangeSet{}, CatalogConflict{}
		}
		if s.domainValidation != nil && !s.domainValidation.current(e.catalog.read(current), currentStorage) {
			e.mu.Unlock()
			return ChangeSet{}, CatalogConflict{}
		}
		if !typedObservationValid(s, e.catalog.read(current)) {
			e.mu.Unlock()
			return ChangeSet{}, CatalogConflict{}
		}
		for _, base := range s.domainBases {
			observed := base
			if s.descriptors[base.Address()] == base {
				observed = s.base.Get(base.Address())
			}
			if current.Get(base.Address()) != observed {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
			if ns, ok := base.(*namespaceDescriptor); ok && s.terminalCleanup && ns.Temporary != (ids.SessionToken{}) {
				for address, d := range current.Descriptors() {
					if _, removed := s.removals[address]; d.Namespace() == ns.address && (!removed || d != s.base.Get(address)) {
						e.mu.Unlock()
						return ChangeSet{}, CatalogConflict{}
					}
				}
			}
		}
		if err := s.validateLikeSources(e.catalog.read(current)); err != nil {
			e.mu.Unlock()
			return ChangeSet{}, err
		}
		for _, source := range s.querySources {
			if !materializedSourceCurrent(s.materializedSourceView, source, current, currentStorage[source.Address()]) {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for _, d := range s.descriptors {
			ns := d.Namespace()
			if ns != (ids.ObjectAddress{}) && s.descriptors[ns] == nil && current.Get(ns) != s.base.Get(ns) {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
			// A namespace move still relies on source lookup authority. Fence
			// that original namespace as well as the destination, without
			// republishing it or observing unrelated namespace changes.
			if old := s.base.Get(d.Address()); old != nil {
				source := old.Namespace()
				if source != ns && source != (ids.ObjectAddress{}) && s.descriptors[source] == nil && current.Get(source) != s.base.Get(source) {
					e.mu.Unlock()
					return ChangeSet{}, CatalogConflict{}
				}
			}
		}
		for a := range s.removals {
			if a.ClassID == ids.NamespaceClassID {
				for address, d := range current.Descriptors() {
					if _, removed := s.removals[address]; d.Namespace() == a && (!removed || d != s.base.Get(address)) {
						e.mu.Unlock()
						return ChangeSet{}, CatalogConflict{}
					}
				}
			}
			// A newly attached MV was not in the validated retirement closure.
			// Recheck its live immutable x authority at the final publication fence.
			if a.ClassID == ids.ExtensionClassID {
				for address, descriptor := range current.Descriptors() {
					if descriptor == s.base.Get(address) {
						continue
					}
					if relation, ok := descriptor.(*relationDescriptor); ok && materializedExtensionDepends(relation, a) {
						e.mu.Unlock()
						return ChangeSet{}, CatalogConflict{}
					}
				}
			}
		}
		for a := range s.descriptors {
			if current.Get(a) != s.base.Get(a) {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for a := range s.removals {
			if current.Get(a) != s.base.Get(a) {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for k, observed := range s.touched {
			if current.NameAddress(k) != observed {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for a, removal := range s.removals {
			observed := removal.descriptor
			d := current.Get(a)
			if removal.ref.Domain() != e.catalog.Domain || removal.ref.Epoch != current.epoch || removal.ref.Address != a || d == nil || d != observed || d.Revision() != observed.Revision() {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		if !e.partitionReadsValidLocked(s) {
			e.mu.Unlock()
			return ChangeSet{}, CatalogConflict{}
		}
		for a, shape := range s.rowShapes {
			if currentStorage[a] != shape.original || len(shape.original.versions) != shape.observedLength || shape.original.generation != shape.generation || shape.original.ends != shape.observedEnds {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		if r := s.materializedReplacement; r != nil {
			if err := s.materializedReplacementCurrent(current, currentStorage[r.original.address]); err != nil {
				e.mu.Unlock()
				return ChangeSet{}, err
			}
		}
		for address, candidate := range s.indexCandidates {
			index, ok := s.descriptors[address].(*relationDescriptor)
			if !ok {
				index, ok = current.Relation(address)
			}
			if !ok || index.SQLIndex == nil {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
			store := currentStorage[index.SQLIndex.Parent]
			if shape, changed := s.rowShapes[index.SQLIndex.Parent]; changed {
				store = shape.replacement
			} else if initial := s.initialTables[index.SQLIndex.Parent]; initial != nil {
				store = initial
			}
			if !candidate.matchesIndex(index, store) {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		indexStores := captureIndexStores(currentStorage, current)
		for _, d := range s.descriptors {
			index, ok := d.(*relationDescriptor)
			if !ok || index.SQLIndex == nil {
				continue
			}
			if indexStores == nil {
				indexStores = make(map[ids.ObjectAddress]*relationStorage)
			}
			a := index.SQLIndex.Parent
			if indexStores[a] == nil && currentStorage[a] != nil {
				copy := *currentStorage[a]
				copy.indexSource = currentStorage[a]
				indexStores[a] = &copy
			}
		}
		indexPredecessors := e.indexRoots
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
		next := cloneCatalog(current)
		next.defaultACLs = overlayDefaultACLs(current.defaultACLs, s.defaultACLs)
		operators := operatorIndexEdit{index: current.operatorNames}
		storage := make(map[ids.ObjectAddress]*relationStorage, len(currentStorage)+len(s.descriptors))
		for a, v := range currentStorage {
			storage[a] = v
		}
		for a, removal := range s.removals {
			d := removal.descriptor
			operators.replace(current.Get(a), s.descriptors[a])
			next.removeDescriptor(a)
			delete(next.dumpPriorities, a)
			next.unbindName(descriptorNameKey(d))
			delete(storage, a)
		}
		for k := range s.oldNames {
			next.unbindName(k)
		}
		for a, d := range s.descriptors {
			if _, removed := s.removals[a]; !removed {
				operators.replace(current.Get(a), d)
			}
			next.setDescriptor(a, d)
			if relation, ok := d.(*relationDescriptor); ok && relation.RelationKind.stored() && storage[a] == nil {
				if initial := s.initialTables[a]; initial != nil {
					storage[a] = initial
				} else {
					storage[a] = &relationStorage{}
				}
			}
		}
		for a, shape := range s.rowShapes {
			storage[a] = shape.replacement
		}
		if replacement := s.materializedReplacement; replacement != nil {
			storage[replacement.original.address] = replacement.storage
		}
		for k, a := range s.names {
			next.bindName(k, a)
		}
		next.operatorNames = operators.index
		if err := catalogDependencyPreflight(current, next, s.identityDependencies, s.identityOriginals); err != nil {
			return ChangeSet{}, err
		}
		buildStores := indexStores
		candidateStores := false
		var rebuildIndexes map[ids.ObjectAddress]bool
		if len(indexStores) > 0 && (len(s.rowShapes) > 0 || s.materializedReplacement != nil) {
			buildStores = make(map[ids.ObjectAddress]*relationStorage, len(indexStores))
			for a, store := range indexStores {
				buildStores[a] = store
			}
			candidateStores = true
			rebuildIndexes = make(map[ids.ObjectAddress]bool, len(s.rowShapes))
			for a, shape := range s.rowShapes {
				buildStores[a] = shape.replacement
				rebuildIndexes[a] = true
			}
			if replacement := s.materializedReplacement; replacement != nil {
				buildStores[replacement.original.address] = replacement.storage
				rebuildIndexes[replacement.original.address] = true
			}
		}
		// New parents and their backing indices publish together. Build from
		// the exact candidate store, but keep committed absence in the fence.
		for _, d := range s.descriptors {
			index, ok := d.(*relationDescriptor)
			if !ok || index.SQLIndex == nil {
				continue
			}
			a := index.SQLIndex.Parent
			parent, staged := s.descriptors[a].(*relationDescriptor)
			if !staged || (parent.RelationKind != tableRelation && parent.RelationKind != materializedViewRelation) || current.Get(a) != nil {
				continue
			}
			if !candidateStores {
				buildStores = make(map[ids.ObjectAddress]*relationStorage, len(indexStores)+1)
				for address, store := range indexStores {
					buildStores[address] = store
				}
				candidateStores = true
			}
			buildStores[a] = storage[a]
		}
		sourceRoots := indexPredecessors
		if len(s.indexCandidates) != 0 {
			sourceRoots = make(map[ids.ObjectAddress]*logicalIndexRoot, len(indexPredecessors)+len(s.indexCandidates))
			for a, root := range indexPredecessors {
				sourceRoots[a] = root
			}
			for a, candidate := range s.indexCandidates {
				sourceRoots[a] = candidate.root
			}
		}
		var runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef
		if r := s.materializedReplacement; r != nil && r.concurrent {
			runtimeOwners = map[ids.ObjectAddress]authorization.PrincipalRef{r.original.address: r.original.Owner}
		}
		evaluation := EvalContext{Context: p.ctx, Scope: 1, Frame: 1, statementScope: scope}
		if scope != nil {
			evaluation.Input, evaluation.Output = scope.input, scope.output
		} else if op != nil {
			evaluation.Input, evaluation.Output = sessionPresentation(op.session)
		}
		indexRoots, indexErr := e.prepareIndexCatalog(&evaluation, current, next, buildStores, sourceRoots, rebuildIndexes, s.indexRebuilds, s.indexOutput, disclosure, runtimeOwners, s.indexCandidates)
		if indexErr != nil {
			return ChangeSet{}, indexErr
		}
		domainChanged := len(s.domainTargets) != 0
		for _, d := range s.descriptors {
			if t, ok := d.(*typeDescriptor); ok && t.SQLType.IsUserDefined() {
				domainChanged = true
				break
			}
		}
		for _, descriptor := range next.Descriptors() {
			if relation, ok := descriptor.(*relationDescriptor); ok && relation.Inheritance != nil {
				if err := validateInheritance(e.catalog.read(next), relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if domainChanged || s.descriptors[descriptor.Address()] != nil {
				if relation, ok := descriptor.(*relationDescriptor); ok {
					if err := validateTypedAttachment(e.catalog.read(next), relation); err != nil {
						return ChangeSet{}, err
					}
				}
				if err := validateTypeProviders(e.catalog.read(next), descriptor); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*relationDescriptor); ok && len(relation.Policies) != 0 {
				if err := validatePolicies(e.catalog.read(next), relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*relationDescriptor); ok && len(rulesOf(relation)) != 0 {
				if err := validateRules(e.catalog.read(next), relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if view, ok := descriptor.(*relationDescriptor); ok && view.View != nil {
				if err := validateViewDefinition(e.catalog.read(next), view); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*relationDescriptor); ok && len(relation.CheckConstraints) != 0 {
				if err := validateCheckProviders(e.catalog.read(next), relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*relationDescriptor); ok && len(relation.Defaults) != 0 {
				if err := validateDefaultProviders(e.catalog.read(next), relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*relationDescriptor); ok && len(relation.Generations) != 0 {
				if err := validateGenerationProviders(e.catalog.read(next), relation); err != nil {
					return ChangeSet{}, err
				}
			}
		}
		e.mu.Lock()
		if op != nil {
			op.session.mu.Lock()
		}
		if e.catalog.state != current {
			if op != nil {
				op.session.mu.Unlock()
			}
			e.mu.Unlock()
			continue
		}
		if !indexStoresUnchanged(e.storage, indexStores, current, next, indexRoots, indexPredecessors) {
			if op != nil {
				op.session.mu.Unlock()
			}
			e.mu.Unlock()
			continue
		}
		if !e.partitionReadsValidLocked(s) {
			if op != nil {
				op.session.mu.Unlock()
			}
			e.mu.Unlock()
			return ChangeSet{}, CatalogConflict{}
		}
		for a, shape := range s.rowShapes {
			if e.storage[a] != shape.original || len(shape.original.versions) != shape.observedLength || shape.original.generation != shape.generation || shape.original.ends != shape.observedEnds {
				if op != nil {
					op.session.mu.Unlock()
				}
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		if r := s.materializedReplacement; r != nil {
			if err := s.materializedReplacementCurrent(e.catalog.state, e.storage[r.original.address]); err != nil {
				if op != nil {
					op.session.mu.Unlock()
				}
				e.mu.Unlock()
				return ChangeSet{}, err
			}
		}
		err := p.ctx.Err()
		if err == nil && !e.domainIndexRootsCurrent(s) {
			err = CatalogConflict{}
		}
		if err == nil && s.domainValidation != nil && !s.domainValidation.current(e.catalog.read(e.catalog.state), e.storage) {
			err = CatalogConflict{}
		}
		if err == nil && s.databasePredecessor != nil && e.catalog.state.Get(currentDatabaseAddress) != s.databasePredecessor {
			err = ids.ErrCatalogReplaced
		}
		if err == nil {
			err = catalogDependencyPreflight(e.catalog.state, next, s.identityDependencies, s.identityOriginals)
		}
		if err == nil {
			err = s.validateRoleCandidate(e.roles)
		}
		if err == nil && op != nil {
			err = publicationOperationLocked(e, op)
		}
		if err == nil {
			err = validateDefaultACLs(s.roleRead(e.roles, e.catalog.read(next)), e.catalog.read(next))
		}
		if err == nil {
			for _, d := range s.descriptors {
				if err = validateACL(s.roleRead(e.roles, e.catalog.read(next)), d); err != nil {
					break
				}
			}
		}
		if err == nil && s.authSession != nil {
			// SQL stages use the operation's session, already locked above.
			if op == nil || s.authSession != op.session {
				s.authSession.mu.Lock()
			}
			auth := s.authSession.authorization
			valid := auth != nil && auth.revision == s.authRevision && e.roles.revision == s.roleRevision
			if op == nil || s.authSession != op.session {
				s.authSession.mu.Unlock()
			}
			if !valid {
				err = fmt.Errorf("stuntgres: authorization changed before publication")
			}
		}
		if err == nil && s.materializedReplacement != nil && s.authSession != nil {
			if op == nil || s.authSession != op.session {
				s.authSession.mu.Lock()
			}
			auth := AuthorizationView{engine: e, pairedCatalog: e.catalog.read(e.catalog.state), roleRecord: e.roles, catalog: e.catalog.read(e.catalog.state), roles: e.roleRead(e.catalog.read(e.catalog.state), e.roles), state: s.authSession.authorization.state}
			err = auth.Check(AccessRequirement{Object: e.catalog.Domain.Ref(e.catalog.state.epoch, s.materializedReplacement.original.address), Privileges: authorization.MaintainPrivilege})
			if op == nil || s.authSession != op.session {
				s.authSession.mu.Unlock()
			}
		}
		if err == nil {
			err = p.ctx.Err()
		}
		if err == nil {
			namespaceChanged := false
			for a := range s.descriptors {
				namespaceChanged = namespaceChanged || a.ClassID == ids.NamespaceClassID
			}
			for a := range s.removals {
				namespaceChanged = namespaceChanged || a.ClassID == ids.NamespaceClassID
			}
			if namespaceChanged {
				err = next.advanceNamespaceGeneration()
			}
		}
		if err == nil && (len(s.initialTables) != 0 || s.materializedReplacement != nil) && e.commitSeq == ids.CommitSeq(math.MaxUint64) {
			err = fmt.Errorf("stuntgres: commit sequence exhausted")
		}
		if err == nil && s.initialTx != (ids.TxRef{}) {
			err = e.serializablePrecommitLocked(s.initialTx)
		}
		commitRef := s.initialTx
		if scope != nil {
			commitRef = scope.txRef
		}
		if err == nil && commitRef != (ids.TxRef{}) {
			err = e.assignTransactionXIDLocked(commitRef)
		}
		if err == nil {
			// Staged replacements may advance revisions without reserving from
			// the allocator. Commit its frontier with the successful publication.
			nextRevision := e.catalog.nextRevision
			for _, d := range s.descriptors {
				if revision := descriptorRevisionFrontier(d); revision >= nextRevision {
					nextRevision = revision
					if nextRevision != ids.DefinitionRevision(math.MaxUint64) {
						nextRevision++
					}
				}
			}
			if len(s.initialTables) != 0 || s.materializedReplacement != nil {
				frontier := e.commitSeq + 1
				for _, initial := range s.initialTables {
					for i := range initial.versions {
						initial.versions[i].born = frontier
					}
					initial.generation = 1
				}
				if replacement := s.materializedReplacement; replacement != nil {
					for i := range replacement.storage.versions {
						replacement.storage.versions[i].born = frontier
					}
					if replacement.concurrent {
						lineage := replacement.predecessor.materializedLineage
						if lineage == nil {
							lineage = &materializedLineage{marker: 1}
						}
						replacement.predecessor.materializedLineage = lineage
						replacement.storage.materializedLineage = lineage
					}
				}
				e.commitSeq = frontier
			}
			if s.initialTx != (ids.TxRef{}) {
				e.serializableCommittedLocked(s.initialTx, e.commitSeq)
			}
			e.catalog.state = next
			e.storage = storage
			preserveUnchangedIndexRoots(indexRoots, indexPredecessors, e.indexRoots)
			e.indexRoots = indexRoots
			for a := range s.removals {
				delete(e.sequences, a)
			}
			for a, generator := range s.generators {
				e.sequences[a] = generator
			}
			e.catalog.nextRevision = nextRevision
			if s.roleCandidate != nil {
				e.roles = s.roleCandidate
			}
			if commitRef != (ids.TxRef{}) {
				e.recordCommitTimestampLocked(commitRef)
			}
			s.lifecycle = catalogPublished
		}
		if op != nil {
			op.session.mu.Unlock()
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
func (e *Engine) ValidateObjectRef(view CatalogRead, ref ids.ObjectRef) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.catalog.validateObjectRefLocked(view, ref)
}

// ReplaceCatalog installs replacement, a catalog view that source owns, with
// source's committed rows, sequences and roles. source may be e.
func (e *Engine) ReplaceCatalog(ctx context.Context, source *Engine, replacement CatalogRead) (ChangeSet, error) {
	return e.replaceCatalog(ctx, source, replacement, nil)
}

// Restore retains its genuine private operation through owner-cell validation.
func (e *Engine) replaceCatalog(ctx context.Context, source *Engine, replacement CatalogRead, restoreOp *Operation) (ChangeSet, error) {
	var installed ids.CatalogEpoch
	defer func() {
		if installed != 0 {
			e.locks.Fence(installed)
		}
	}()
	if replacement.state == nil || replacement.owner == nil || source == nil || replacement.owner != source.catalog.owner() {
		return ChangeSet{}, fmt.Errorf("stuntgres: invalid replacement catalog")
	}
	if err := ctx.Err(); err != nil {
		return ChangeSet{}, err
	}
	var err error
	replacement, err = persistentReplacementCatalog(replacement)
	if err != nil {
		return ChangeSet{}, err
	}
	if err := normalizeReplacementIndexHistory(ctx, replacement); err != nil {
		return ChangeSet{}, err
	}
	if err := normalizeReplacementSubclassHistory(ctx, replacement); err != nil {
		return ChangeSet{}, err
	}
	targetRoles := e.Roles()
	if err := validateDefinitionOIDs(replacement); err != nil {
		return ChangeSet{}, err
	}
	for _, d := range replacement.Descriptors() {
		p := e.catalog.provider(d.Address().ClassID)
		if p == nil {
			return ChangeSet{}, fmt.Errorf("stuntgres: unregistered catalog class %d", d.Address().ClassID)
		}
		if err := p.Validate(replacement, d); err != nil {
			return ChangeSet{}, err
		}
	}
	var next *catalogState
	relationAddresses := make([]ids.ObjectAddress, 0, replacement.DescriptorCount())
	hasHierarchy := false
	for a, d := range replacement.Descriptors() {
		if relation, ok := d.(*relationDescriptor); ok && hierarchyRelation(relation) {
			hasHierarchy = true
		}
		if r, ok := d.(*relationDescriptor); ok && r.RelationKind.stored() {
			relationAddresses = append(relationAddresses, a)
		}
	}
	storage := make(map[ids.ObjectAddress]*relationStorage, len(relationAddresses))
	var sequences map[ids.ObjectAddress]*sequenceGenerator
	var indexes *indexSnapshot
	copyRows := func(owner *Engine) error {
		source, frontier := owner.storage, owner.commitSeq
		for _, a := range relationAddresses {
			if source[a] == nil {
				return fmt.Errorf("stuntgres: relation storage is missing")
			}
			selected, err := selectQuerySource(ctx, &querySource{committed: source[a].versions}, frontier, source[a].ends, nil)
			if err != nil {
				return err
			}
			versions := make([]rowVersion, selected.count())
			for i := range versions {
				versions[i] = selected.versionAt(selected.rawIndex(i))
			}
			selected.close()
			storage[a] = &relationStorage{versions: versions}
		}
		var err error
		indexes, err = owner.captureIndexSnapshot(ctx, replacement, source, owner.indexRoots, frontier)
		return err
	}
	if replacement.owner == e.catalog.owner() {
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
		if replacement.Epoch() != e.catalog.state.epoch {
			return ChangeSet{}, ids.ErrCatalogReplaced
		}
		next = cloneCatalog(replacement.state)
		if err := copyRows(e); err != nil {
			return ChangeSet{}, err
		}
		var sequenceErr error
		sequences, sequenceErr = copySequenceState(e, replacement)
		if sequenceErr != nil {
			return ChangeSet{}, sequenceErr
		}
		if err := e.preserveTemporaryLocked(ctx, next, storage, sequences, restoreOp, indexes); err != nil {
			return ChangeSet{}, err
		}
		if err := validateDefaultACLs(e.roleRead(e.catalog.read(next), e.roles), e.catalog.read(next)); err != nil {
			return ChangeSet{}, err
		}
		nextRevision := e.catalog.nextRevision
		for _, d := range next.Descriptors() {
			if err := validateACL(e.roleRead(e.catalog.read(next), e.roles), d); err != nil {
				return ChangeSet{}, err
			}
			if revision := descriptorRevisionFrontier(d); revision >= nextRevision {
				nextRevision = revision
				if nextRevision != ids.DefinitionRevision(math.MaxUint64) {
					nextRevision++
				}
			}
		}
		if e.catalog.state.epoch == ids.CatalogEpoch(math.MaxUint64) {
			return ChangeSet{}, fmt.Errorf("stuntgres: catalog epoch exhausted")
		}
		var imported uint64
		for _, relation := range storage {
			imported += uint64(len(relation.versions))
		}
		if imported > ^uint64(0)-e.nextRow {
			return ChangeSet{}, fmt.Errorf("stuntgres: row identity exhausted")
		}
		if imported > ^uint64(0)-e.nextVersion {
			return ChangeSet{}, fmt.Errorf("stuntgres: version identity exhausted")
		}
		if e.commitSeq == ids.CommitSeq(^uint64(0)) {
			return ChangeSet{}, fmt.Errorf("stuntgres: commit sequence exhausted")
		}
		if hasHierarchy {
			candidate := e.catalog.read(next)
			for _, descriptor := range next.Descriptors() {
				if relation, ok := descriptor.(*relationDescriptor); ok && hierarchyRelation(relation) {
					if err := validatePartitionDescriptor(candidate, relation); err != nil {
						return ChangeSet{}, err
					}
				}
			}
		}
		roots, nextRow, nextVersion, err := e.prepareRemintedStorage(ctx, next, storage, indexes)
		if err != nil {
			return ChangeSet{}, err
		}
		if err := ctx.Err(); err != nil {
			return ChangeSet{}, err
		}
		e.nextRow, e.nextVersion = nextRow, nextVersion
		e.indexRoots = roots
		e.commitSeq++
		next.epoch = e.catalog.state.epoch + 1
		e.catalog.state = next
		e.storage = storage
		e.exclusionPending = nil
		e.exclusionGeneration++
		e.sequences = sequences
		e.clearPersistentSequenceMemoryLocked()
		e.catalog.nextRevision = nextRevision
		e.fenceSerializableLocked()
		e.retireCommitTimestampsLocked()
		installed = next.epoch
		return ChangeSet{Replacement: true}, nil
	}
	source.mu.Lock()
	if replacement.Epoch() != source.catalog.state.epoch {
		source.mu.Unlock()
		return ChangeSet{}, ids.ErrCatalogReplaced
	}
	err = copyRows(source)
	if err == nil {
		sequences, err = copySequenceState(source, replacement)
	}
	sourceRoles := source.roleRead(replacement, source.roles)
	foreignNamespaces := source.foreignNamespaces
	source.mu.Unlock()
	if err != nil {
		return ChangeSet{}, err
	}
	var imported uint64
	for _, relation := range storage {
		imported += uint64(len(relation.versions))
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
	if e.catalog.state.epoch == ids.CatalogEpoch(math.MaxUint64) {
		return ChangeSet{}, fmt.Errorf("stuntgres: catalog epoch exhausted")
	}
	if imported > ^uint64(0)-e.nextRow {
		return ChangeSet{}, fmt.Errorf("stuntgres: row identity exhausted")
	}
	if imported > ^uint64(0)-e.nextVersion {
		return ChangeSet{}, fmt.Errorf("stuntgres: version identity exhausted")
	}
	if e.commitSeq == ids.CommitSeq(^uint64(0)) {
		return ChangeSet{}, fmt.Errorf("stuntgres: commit sequence exhausted")
	}
	replacement.state.aclItemImport = e.aclItemImporter(source, sourceRoles, targetRoles, source.importTargets)
	defer func() { replacement.state.aclItemImport = nil }()
	next, err = e.remapPreparedCatalog(replacement.state, sourceRoles, targetRoles, source.importTargets)
	if err != nil {
		return ChangeSet{}, err
	}
	if err = e.preserveTemporaryLocked(ctx, next, storage, sequences, restoreOp, indexes); err != nil {
		return ChangeSet{}, err
	}
	if err := validateDefaultACLs(e.roleRead(e.catalog.read(next), e.roles), e.catalog.read(next)); err != nil {
		return ChangeSet{}, err
	}
	nextRevision := e.catalog.nextRevision
	for _, d := range next.Descriptors() {
		if err := validateACL(e.roleRead(e.catalog.read(next), e.roles), d); err != nil {
			return ChangeSet{}, err
		}
		if revision := descriptorRevisionFrontier(d); revision >= nextRevision {
			nextRevision = revision
			if nextRevision != ids.DefinitionRevision(math.MaxUint64) {
				nextRevision++
			}
		}
	}
	prepared, err := e.prepareForeignIndexStorage(ctx, next, storage, indexes)
	if err != nil {
		return ChangeSet{}, err
	}
	if err := e.installForeignCatalogLocked(ctx, next, storage, sequences, nextRevision, prepared); err != nil {
		return ChangeSet{}, err
	}
	e.foreignNamespaces = foreignNamespaces
	installed = next.epoch
	return ChangeSet{Replacement: true}, nil
}

// installForeignCatalogLocked owns the final cancellation boundary and the
// indivisible installation of a prepared foreign generation. The caller holds
// replacementMu and mu and has validated capacity, descriptors and authority.
// Storage vectors and sequence generator scalars are private copies; payload
// values remain immutable. All prepared containers publish at the same boundary.
func (e *Engine) installForeignCatalogLocked(ctx context.Context, next *catalogState, storage map[ids.ObjectAddress]*relationStorage, sequences map[ids.ObjectAddress]*sequenceGenerator, nextRevision ids.DefinitionRevision, prepared remintedIndexStorage) error {
	if prepared.catalog != next || prepared.baseCatalog != e.catalog.state || prepared.baseRow != e.nextRow || prepared.baseVersion != e.nextVersion || prepared.frontier != e.commitSeq {
		return ids.ErrCatalogReplaced
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.nextRow, e.nextVersion = prepared.nextRow, prepared.nextVersion
	e.indexRoots = prepared.roots
	e.commitSeq++
	next.epoch = e.catalog.state.epoch + 1
	e.catalog.state = next
	e.storage = storage
	e.exclusionPending = nil
	e.exclusionGeneration++
	e.sequences = sequences
	e.clearPersistentSequenceMemoryLocked()
	e.catalog.nextRevision = nextRevision
	e.fenceSerializableLocked()
	e.retireCommitTimestampsLocked()
	return nil
}

func cloneCatalog(s *catalogState) *catalogState {
	n := &catalogState{epoch: s.epoch, namespaceGeneration: s.namespaceGeneration, descriptors: make(map[ids.ObjectAddress]Descriptor, s.DescriptorCount()), names: make(map[ids.NameKey]ids.ObjectAddress, s.NameCount())}
	n.defaultACLs = s.defaultACLs
	n.operatorNames = s.operatorNames
	for k, v := range s.Descriptors() {
		n.setDescriptor(k, v)
	}
	for k, v := range s.Names() {
		n.bindName(k, v)
	}
	if len(s.dumpPriorities) != 0 {
		n.dumpPriorities = make(map[ids.ObjectAddress]ids.OID, len(s.dumpPriorities))
		for address, priority := range s.dumpPriorities {
			if _, exists := n.Find(address); exists {
				n.dumpPriorities[address] = priority
			}
		}
	}
	return n
}

// Namespace cache invalidation is independent of descriptor revision allocation.
// Advance only the unpublished catalog so exhaustion cannot publish any effects.
func (s *catalogState) advanceNamespaceGeneration() error {
	if s.namespaceGeneration == math.MaxUint64 {
		return fmt.Errorf("stuntgres: namespace invalidation generation exhausted")
	}
	s.namespaceGeneration++
	return nil
}
func (e *Engine) RegisterCatalogProvider(p CatalogProvider) error {
	if p == nil {
		return fmt.Errorf("stuntgres: nil catalog provider")
	}
	id := p.ClassID()
	if id == 0 {
		return fmt.Errorf("stuntgres: zero catalog class")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.catalog.registerProviderLocked(id, p)
}

func descriptorRevisionFrontier(d Descriptor) ids.DefinitionRevision {
	revision := d.Revision()
	if relation, ok := d.(*relationDescriptor); ok && relation.SequencePlanStamp > revision {
		revision = relation.SequencePlanStamp
	}
	return revision
}
