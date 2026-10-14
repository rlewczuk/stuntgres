package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

type OID uint32
type CatalogEpoch uint64
type DefinitionRevision uint64
type ObjectAddress struct {
	ClassID  OID
	ObjectID OID
	SubID    int32
}

// catalogOwner has nonzero size so distinct live engines have distinct tokens.
type catalogOwner struct{ marker byte }

type ObjectRef struct {
	owner   *catalogOwner
	Epoch   CatalogEpoch
	Address ObjectAddress
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

const (
	NamespaceClassID      OID = 2615
	RelationClassID       OID = 1259
	TypeClassID           OID = 1247
	CollationClassID      OID = 3456
	ConversionClassID     OID = 2607
	PolicyClassID         OID = 3256
	PGCatalogNamespaceOID OID = 11
	PublicNamespaceOID    OID = 2200
)

type ObjectKind uint8

const (
	NamespaceObject ObjectKind = iota + 1
	RelationObject
	TypeObject
	CollationObject
	ConversionObject
	ExtensionObject
	ProcedureObject
	TSTemplateObject
	TSDictionaryObject
	DatabaseObject
	OperatorObject
)

type Descriptor interface {
	Address() ObjectAddress
	Revision() DefinitionRevision
	Namespace() ObjectAddress
	Name() string
	Kind() ObjectKind
}
type namespaceDescriptor struct {
	address   ObjectAddress
	name      string
	revision  DefinitionRevision
	owner     authorization.PrincipalRef
	acl       authorization.ACLValue
	comment   string
	temporary SessionToken
}

func (d *namespaceDescriptor) Address() ObjectAddress       { return d.address }
func (d *namespaceDescriptor) Revision() DefinitionRevision { return d.revision }
func (*namespaceDescriptor) Namespace() ObjectAddress       { return ObjectAddress{} }
func (d *namespaceDescriptor) Name() string                 { return d.name }
func (*namespaceDescriptor) Kind() ObjectKind               { return NamespaceObject }

type relationDescriptor struct {
	address, namespace   ObjectAddress
	name                 string
	revision             DefinitionRevision
	sequencePlanStamp    DefinitionRevision
	columns              []ColumnDescriptor
	index                map[string]int
	attributes           []int32
	attributeHighWater   int32
	owner                authorization.PrincipalRef
	acl                  authorization.ACLValue
	columnACL            map[int32]authorization.ACLValue
	comment              string
	columnComments       map[int32]string
	notNullConstraints   []notNullConstraint
	checkConstraints     []checkConstraint
	policies             []policyDefinition
	rlsEnabled           bool
	rlsForced            bool
	uniqueConstraints    []uniqueConstraint
	exclusionConstraints []exclusionConstraint
	foreignKeys          []foreignKeyConstraint
	defaults             map[int32]columnDefault
	generations          map[int32]columnGeneration
	identities           map[int32]columnIdentity
	inheritance          *tableInheritance
	relationKind         relationKind
	hasIndexes           bool // Sticky publication history; removing an index does not clear it.
	hasSubclasses        bool // Sticky inheritance/table/index partition publication history.
	onCommit             parser.OnCommitAction
	sequence             *sequenceOptions
	view                 *viewDefinition
	populated            bool            // Independent of stored row count, including populated-empty MVs.
	autoExtensions       []ObjectAddress // Immutable whole-object AUTO_EXTENSION authority.
	virtualView          bool
	sqlIndex             *indexDefinition
	partition            *partitionDefinition
	membership           *partitionMembership
	rowType              sqltype.ID
	ofType               ObjectAddress
}

func (d *relationDescriptor) Address() ObjectAddress       { return d.address }
func (d *relationDescriptor) Revision() DefinitionRevision { return d.revision }
func (d *relationDescriptor) Namespace() ObjectAddress     { return d.namespace }
func (d *relationDescriptor) Name() string                 { return d.name }
func (*relationDescriptor) Kind() ObjectKind               { return RelationObject }

func (d *relationDescriptor) hasAttribute(sub int32) bool {
	for _, live := range d.attributes {
		if live == sub {
			return true
		}
	}
	return false
}

type notNullConstraint struct {
	OID            OID
	Name           string
	Attribute      int32
	NotValid       bool
	partitionLocal bool // Explicit NN declaration merged with an inherited definition.
}

type NameKey struct {
	ClassID   OID
	Namespace ObjectAddress
	Name      string
	Signature string
}
type catalogState struct {
	epoch               CatalogEpoch
	namespaceGeneration uint64
	descriptors         map[ObjectAddress]Descriptor
	names               map[NameKey]ObjectAddress
	defaultACLs         map[defaultACLKey]*defaultACLRecord
	// Sparse replay identity provenance for collision-remapped declarations.
	// Absent entries use their current OID; dependencies remain the order authority.
	dumpPriorities map[ObjectAddress]OID
	// Replacement-candidate-only translation, cleared before publication.
	// Keys are exact source codecs, never numeric identity coincidences.
	enumImports   map[*sqltype.Descriptor]*sqltype.Descriptor
	aclItemImport func(sqltype.Value) (sqltype.Value, error)
}
type CatalogRead struct {
	owner *Engine
	state *catalogState
}

func (r CatalogRead) Epoch() CatalogEpoch {
	if r.state == nil {
		return 0
	}
	return r.state.epoch
}
func (r CatalogRead) Descriptor(a ObjectAddress) (Descriptor, bool) {
	if r.state == nil {
		return nil, false
	}
	d, ok := r.state.descriptors[a]
	return d, ok
}
func (r CatalogRead) Lookup(k NameKey) []ObjectAddress {
	if r.state == nil {
		return nil
	}
	a, ok := r.state.names[k]
	if !ok {
		return nil
	}
	return []ObjectAddress{a}
}

type CatalogProvider interface {
	ClassID() OID
	Validate(CatalogRead, Descriptor) error
}
type namespaceProvider struct{}

func (namespaceProvider) ClassID() OID { return NamespaceClassID }
func (namespaceProvider) Validate(v CatalogRead, d Descriptor) error {
	a := d.Address()
	key := descriptorNameKey(d)
	found := v.Lookup(key)
	if d.Kind() != NamespaceObject || a.ClassID != NamespaceClassID || a.ObjectID == 0 || a.SubID != 0 || d.Revision() == 0 || d.Namespace() != (ObjectAddress{}) || len(found) != 1 || found[0] != a {
		return fmt.Errorf("stuntgres: invalid namespace descriptor")
	}
	return nil
}

type relationProvider struct{}

func (relationProvider) ClassID() OID { return RelationClassID }
func (relationProvider) Validate(v CatalogRead, d Descriptor) error {
	r, ok := d.(*relationDescriptor)
	if !ok || d.Kind() != RelationObject || d.Address().ClassID != RelationClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 {
		return fmt.Errorf("stuntgres: invalid relation descriptor")
	}
	ns, ok := v.Descriptor(r.namespace)
	if !ok || ns.Kind() != NamespaceObject {
		return fmt.Errorf("stuntgres: relation namespace is missing")
	}
	if r.onCommit > parser.OnCommitDrop || r.onCommit != parser.OnCommitUnspecified && (r.relationKind != tableRelation || ns.(*namespaceDescriptor).temporary == (SessionToken{})) {
		return fmt.Errorf("stuntgres: invalid temporary table commit action")
	}
	found := v.Lookup(NameKey{ClassID: RelationClassID, Namespace: r.namespace, Name: r.name})
	if len(found) != 1 || found[0] != r.address || len(r.columns) != len(r.attributes) || len(r.index) != len(r.columns) {
		return fmt.Errorf("stuntgres: invalid relation identity")
	}
	if r.virtualView && r.relationKind != catalogRelation {
		return fmt.Errorf("stuntgres: virtual view requires a catalog source")
	}
	if r.populated && r.relationKind != materializedViewRelation || r.view != nil && r.relationKind != viewRelation && r.relationKind != materializedViewRelation {
		return fmt.Errorf("stuntgres: invalid stored definition kind")
	}
	if len(r.autoExtensions) != 0 && r.relationKind != materializedViewRelation {
		return fmt.Errorf("stuntgres: extension dependency requires a materialized view")
	}
	for i, address := range r.autoExtensions {
		if address.ClassID != ExtensionClassID || address.ObjectID == 0 || address.SubID != 0 || i > 0 && r.autoExtensions[i-1].ObjectID >= address.ObjectID {
			return fmt.Errorf("stuntgres: invalid materialized extension dependency")
		}
		extension, ok := v.state.descriptors[address].(*extensionDescriptor)
		if !ok {
			return fmt.Errorf("stuntgres: missing materialized extension provider")
		}
		if err := (extensionCatalogProvider{class: ExtensionClassID}).Validate(v, extension); err != nil {
			return err
		}
	}
	if r.relationKind.stored() || r.relationKind == partitionedRelation || r.relationKind == viewRelation || r.relationKind == compositeRelation {
		types := v.Lookup(NameKey{ClassID: TypeClassID, Namespace: r.namespace, Name: r.name})
		if len(types) != 0 {
			row, ok := v.state.descriptors[types[0]].(*typeDescriptor)
			if len(types) != 1 || !ok || row.owningRelation != r.address || row.typ.ID() != r.rowType {
				return CatalogConflict{}
			}
		}
	}
	if r.attributeHighWater < 0 {
		return fmt.Errorf("stuntgres: invalid relation attribute high-water")
	}
	for i, c := range r.columns {
		offset, exists := r.index[c.Name]
		if r.attributes[i] <= 0 || r.attributes[i] > r.attributeHighWater || i > 0 && r.attributes[i] <= r.attributes[i-1] || !exists || offset != i {
			return fmt.Errorf("stuntgres: invalid relation column identity")
		}
		if !c.Usage.Type.Concrete() {
			return fmt.Errorf("stuntgres: invalid relation column type")
		}
		typ, exists := v.LookupType(c.Usage.Type.ID())
		if !exists || typ != c.Usage.Type || !typ.Concrete() || !c.Usage.Valid() {
			return fmt.Errorf("stuntgres: invalid relation column type")
		}
		if err := validateUsageCollation(v, c.Usage); err != nil {
			return err
		}
	}
	if (r.rlsEnabled || r.rlsForced) && r.relationKind != tableRelation && r.relationKind != partitionedRelation {
		return fmt.Errorf("stuntgres: row-security flags require a table")
	}
	if err := validatePolicies(v, r); err != nil {
		return err
	}
	if err := validateTypedAttachment(v, r); err != nil {
		return err
	}
	if err := validateInheritance(v, r); err != nil {
		return err
	}
	if r.relationKind == compositeRelation {
		if r.rowType == 0 || r.sequence != nil || r.view != nil || r.sqlIndex != nil || len(r.defaults) != 0 || len(r.identities) != 0 || len(r.notNullConstraints) != 0 || len(r.checkConstraints) != 0 || len(r.generations) != 0 || len(r.uniqueConstraints) != 0 || len(r.exclusionConstraints) != 0 || len(r.foreignKeys) != 0 {
			return fmt.Errorf("stuntgres: composite backing relation has storage behavior")
		}
		for _, c := range r.columns {
			if !c.Nullable {
				return fmt.Errorf("stuntgres: composite member has table constraints")
			}
		}
		return nil
	}
	if r.sqlIndex != nil && r.relationKind != indexRelation {
		return fmt.Errorf("stuntgres: index definition requires an index relation")
	}
	if r.relationKind == indexRelation {
		return validateIndexDefinition(v, r)
	}
	if r.relationKind != tableRelation && r.relationKind != partitionedRelation && len(r.checkConstraints) != 0 {
		return fmt.Errorf("stuntgres: CHECK requires a table descriptor")
	}
	if err := validatePartitionDescriptor(v, r); err != nil {
		return err
	}
	if r.relationKind == sequenceRelation {
		if r.sequencePlanStamp == 0 || r.sequence == nil || len(r.columns) != 3 || r.columns[0].Name != "last_value" || r.columns[1].Name != "log_cnt" || r.columns[2].Name != "is_called" || r.columns[0].Usage.Type.ID() != sqltype.Int8 || r.columns[1].Usage.Type.ID() != sqltype.Int8 || r.columns[2].Usage.Type.ID() != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid sequence descriptor")
		}
		if _, _, err := bindSequenceOptions(r.sequence, sequenceGenerator{last: r.sequence.start}, nil); err != nil {
			return err
		}
		if r.sequence.identity && r.sequence.owned == (ObjectAddress{}) {
			return fmt.Errorf("stuntgres: identity sequence lacks ownership")
		}
		if owned := r.sequence.owned; owned != (ObjectAddress{}) {
			tableAddress := owned
			tableAddress.SubID = 0
			table, ok := v.state.descriptors[tableAddress].(*relationDescriptor)
			if !ok || table.relationKind != tableRelation && table.relationKind != partitionedRelation || !table.hasAttribute(owned.SubID) || table.owner != r.owner || table.namespace != r.namespace {
				return fmt.Errorf("stuntgres: invalid sequence ownership dependency")
			}
			if r.sequence.identity {
				identity, exists := table.identities[owned.SubID]
				if !exists || identity.sequence != r.address {
					return fmt.Errorf("stuntgres: inconsistent identity sequence ownership")
				}
			}
		}
		return nil
	}
	if r.relationKind == viewRelation || r.relationKind == materializedViewRelation {
		return validateViewDefinition(v, r)
	}
	names := make(map[string]bool, len(r.notNullConstraints))
	attrs := make(map[int32]bool, len(r.notNullConstraints))
	for _, c := range r.notNullConstraints {
		if c.OID == 0 || c.Name == "" || len(c.Name) > 63 || !utf8.ValidString(c.Name) || strings.IndexByte(c.Name, 0) >= 0 || names[c.Name] || attrs[c.Attribute] || !r.hasAttribute(c.Attribute) {
			return fmt.Errorf("stuntgres: invalid not-null constraint identity")
		}
		names[c.Name], attrs[c.Attribute] = true, true
	}
	for i, c := range r.columns {
		// Virtual sources declare result nullability without physical table
		// constraint identities. Existing constraint records still validate above.
		if r.relationKind != catalogRelation && attrs[r.attributes[i]] != (c.NullabilityKnown && !c.Nullable) {
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
	for attribute, identity := range r.identities {
		slot := -1
		for i, live := range r.attributes {
			if live == attribute {
				slot = i
				break
			}
		}
		if slot < 0 || identity.mode != parser.IdentityAlways && identity.mode != parser.IdentityByDefault {
			return fmt.Errorf("stuntgres: invalid column identity")
		}
		column := r.columns[slot]
		id := column.Usage.Type.ID()
		if column.Nullable || !column.NullabilityKnown || id != sqltype.Int2 && id != sqltype.Int4 && id != sqltype.Int8 {
			return fmt.Errorf("stuntgres: invalid identity column type or nullability")
		}
		if _, exists := r.defaults[attribute]; exists {
			return fmt.Errorf("stuntgres: identity column has default")
		}
		sequence, exists := v.state.descriptors[identity.sequence].(*relationDescriptor)
		owned := r.address
		owned.SubID = attribute
		if !exists || sequence.sequence == nil || !sequence.sequence.identity || sequence.sequence.owned != owned || sequence.owner != r.owner || sequence.namespace != r.namespace {
			return fmt.Errorf("stuntgres: invalid column identity sequence")
		}
	}
	for attribute, definition := range r.defaults {
		slot := -1
		for i, live := range r.attributes {
			if live == attribute {
				slot = i
				break
			}
		}
		if definition.OID == 0 || slot < 0 || definition.expression == nil || definition.expression.ResultUsage() != r.columns[slot].Usage {
			return fmt.Errorf("stuntgres: invalid column default destination")
		}
		var dependencies []ObjectRef
		catalogSnapshot := false
		_, err := (scalarRenderer{validation: true, provenance: r, capturedManifest: definition.dependencies, dependencies: &dependencies, commentSnapshot: &catalogSnapshot}).sql(context.Background(), v, definition.expression)
		if err != nil || !slices.Equal(dependencies, definition.dependencies) || catalogSnapshot != definition.catalogSnapshot || structuralNull(definition.expression) {
			return fmt.Errorf("stuntgres: invalid column default expression")
		}
	}
	for attribute, definition := range r.generations {
		slot := -1
		for i, live := range r.attributes {
			if live == attribute {
				slot = i
				break
			}
		}
		_, hasDefault := r.defaults[attribute]
		_, hasIdentity := r.identities[attribute]
		if definition.OID == 0 || slot < 0 || definition.expression == nil || definition.expression.ResultUsage() != r.columns[slot].Usage || definition.planned == nil || definition.planned.ResultUsage() != r.columns[slot].Usage || hasDefault || hasIdentity || !generationImmutable(definition.planned) {
			return fmt.Errorf("stuntgres: invalid column generation destination")
		}
		if definition.kind != generationStored && definition.kind != generationVirtual {
			return fmt.Errorf("stuntgres: invalid column generation kind")
		}
		if definition.kind == generationVirtual {
			if err := virtualDestination(r.columns[slot]); err != nil {
				return err
			}
			if err := virtualGenerationSecurity(definition.expression); err != nil {
				return err
			}
			if err := virtualGenerationSecurity(definition.planned); err != nil {
				return err
			}
		}
		if err := validateGenerationPlan(v, r, definition.planned); err != nil {
			return err
		}
		var dependencies []ObjectRef
		catalogSnapshot := false
		_, err := (scalarRenderer{validation: true, owner: r, capturedManifest: definition.dependencies, dependencies: &dependencies, commentSnapshot: &catalogSnapshot}).sql(context.Background(), v, definition.expression)
		if err != nil || !slices.Equal(dependencies, definition.dependencies) || catalogSnapshot != definition.catalogSnapshot {
			return fmt.Errorf("stuntgres: invalid column generation expression")
		}
	}
	if err := validateGenerationProviders(v, r); err != nil {
		return err
	}
	for sub, text := range r.columnComments {
		if !r.hasAttribute(sub) || text == "" {
			return fmt.Errorf("stuntgres: invalid relation column comment")
		}
	}
	return nil
}

var ErrCatalogReplaced = errors.New("stuntgres: catalog replaced before publication")

type PublicationToken struct{ Epoch CatalogEpoch }
type ChangeSet struct {
	Definitions []ObjectAddress
	Statistics  []ObjectAddress
	Namespaces  []ObjectAddress
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
	ref        ObjectRef
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
	descriptors          map[ObjectAddress]Descriptor
	names                map[NameKey]ObjectAddress
	defaultACLs          map[defaultACLKey]*defaultACLRecord
	defaultReads         map[defaultACLKey]*defaultACLRecord
	touched              map[NameKey]ObjectAddress
	removals             map[ObjectAddress]catalogRemoval
	rowShapes            map[ObjectAddress]catalogRowShape
	partitionReads       []partitionReadFence
	indexRebuilds        map[ObjectAddress]*relationDescriptor
	indexCandidates      map[ObjectAddress]indexRootCandidate
	indexParents         map[ObjectAddress]*relationDescriptor
	indexSets            map[ObjectAddress]int
	indexOutput          sqltype.OutputEnvironment
	oldNames             map[NameKey]bool
	generators           map[ObjectAddress]*sequenceGenerator
	identityOriginals    map[ObjectAddress]Descriptor
	identityDependencies []catalogDependencyObservation
	typedSource          ObjectAddress
	typedObserved        map[ObjectAddress]Descriptor
	lifecycle            catalogLifecycle
	invalid              error
	authSession          *Session
	authRevision         uint64
	roleRevision         uint64
	extensionNamespace   ObjectAddress
	databasePredecessor  Descriptor
	databaseAuthority    Descriptor
	// Optional DROP OWNED membership deletions join this publication, not a
	// separate role installation. Every other catalog caller leaves it nil.
	roleCandidate      *roleCatalog
	schemaCascadeNames []string
	viewCascadeNames   []string
	dependencyPath     []ObjectAddress
	domainTargets      []ObjectAddress
	domainBases        []Descriptor
	// Physical retirement alone may saturate dependent descriptor/store/index
	// counters; fresh identities and exact predecessor fences remain mandatory.
	terminalCleanup bool
	// New query-derived tables carry private initial storage, never replacements
	// of live storage. Publication installs the definition and data together.
	initialTables           map[ObjectAddress]*relationStorage
	initialTx               TxRef
	materializedReplacement *materializedReplacement
	querySources            []Descriptor
	materializedSourceView  *visibilityData
	likeSources             []Descriptor // CREATE-only exact descriptor captures, not dependencies.
}

// Only terminal dependency removal can saturate. Other DDL retains its ordinary
// overflow boundary; terminal replacements still have fresh identity.
func (s *CatalogStage) cascadeMetadataRevision(revision DefinitionRevision) (DefinitionRevision, error) {
	if revision == DefinitionRevision(^uint64(0)) {
		if s.terminalCleanup {
			return revision, nil
		}
		return 0, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	return revision + 1, nil
}

type PreparedCatalogPublication struct {
	stage *CatalogStage
	epoch CatalogEpoch
	ctx   context.Context
}

func (e *Engine) BeginCatalogStage(base CatalogRead) *CatalogStage {
	s := &CatalogStage{owner: e, base: base, descriptors: make(map[ObjectAddress]Descriptor), names: make(map[NameKey]ObjectAddress), touched: make(map[NameKey]ObjectAddress)}
	if base.owner != e || base.state == nil {
		s.invalid = fmt.Errorf("stuntgres: foreign catalog view")
	}
	return s
}

// Retain selected private authority separately from the committed predecessor
// already captured by authorizationLocked; building a stage needs no live read.
func (s *CatalogStage) authorizeLocked(auth AuthorizationView, session *Session) {
	s.authSession, s.authRevision, s.roleRevision = session, auth.revision, auth.roleRecord.revision
	s.databaseAuthority = auth.catalog.state.descriptors[currentDatabaseAddress]
	s.databasePredecessor = auth.databasePredecessor
}

// lookupName resolves the private candidate, including names vacated by a
// replacement. The original name remains in touched for publication validation.
func (s *CatalogStage) lookupName(k NameKey) ObjectAddress {
	if address := s.names[k]; address != (ObjectAddress{}) {
		if _, removed := s.removals[address]; !removed {
			return address
		}
	}
	address := s.base.state.names[k]
	if _, removed := s.removals[address]; removed {
		return ObjectAddress{}
	}
	if replacement := s.descriptors[address]; replacement != nil &&
		descriptorNameKey(replacement) != k {
		return ObjectAddress{}
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
	if _, ok := s.base.state.descriptors[a]; ok {
		return CatalogConflict{}
	}
	if _, ok := s.descriptors[a]; ok {
		return CatalogConflict{}
	}
	k := descriptorNameKey(d)
	// One live-name check includes staged replacements and excludes removed
	// generations, whose private name slots can be reused by a new identity.
	if s.lookupName(k) != (ObjectAddress{}) {
		return CatalogConflict{}
	}
	s.touched[k] = s.base.state.names[k]
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
	if previous, ok := old.(*relationDescriptor); ok && previous.hasIndexes {
		if next, ok := d.(*relationDescriptor); !ok || !next.hasIndexes {
			return fmt.Errorf("stuntgres: relation replacement clears index publication history")
		}
	}
	if previous, ok := old.(*relationDescriptor); ok && previous.hasSubclasses {
		if next, ok := d.(*relationDescriptor); !ok || !next.hasSubclasses {
			return fmt.Errorf("stuntgres: relation replacement clears subclass publication history")
		}
	}
	oldKey := descriptorNameKey(old)
	newKey := descriptorNameKey(d)
	if a := s.lookupName(newKey); a != (ObjectAddress{}) && a != d.Address() {
		return CatalogConflict{}
	}
	if previous := s.descriptors[d.Address()]; previous != nil {
		delete(s.names, descriptorNameKey(previous))
	}
	if s.oldNames == nil {
		s.oldNames = make(map[NameKey]bool)
	}
	s.oldNames[oldKey] = true
	s.touched[oldKey] = s.base.state.names[oldKey]
	s.touched[newKey] = s.base.state.names[newKey]
	s.descriptors[d.Address()] = d
	s.names[newKey] = d.Address()
	if relation, ok := d.(*relationDescriptor); ok && synchronize {
		return s.synchronizeRowType(relation)
	}
	return nil
}

// alterRelation couples a same-identity revision with an immutable storage root.
func (s *CatalogStage) alterRelation(ctx context.Context, ref ObjectRef, d *relationDescriptor, original, replacement *relationStorage, observedLength int, observedGeneration uint64, observedEnds *deletionRoot) error {
	if s == nil || s.owner == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	e := s.owner
	e.mu.Lock()
	if err := e.validateObjectRefLocked(s.base, ref); err != nil {
		e.mu.Unlock()
		return err
	}
	old, ok := s.base.state.descriptors[ref.Address].(*relationDescriptor)
	validRevision := ok && d != nil && old.revision != DefinitionRevision(^uint64(0)) && d.revision == old.revision+1
	if s.terminalCleanup && ok && old.revision == DefinitionRevision(^uint64(0)) {
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
	if original == replacement && !slices.Equal(d.attributes, old.attributes) {
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
			if len(row.values) != len(d.columns) || row.row != before.row || row.version != before.version || row.born != before.born {
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
		s.rowShapes = make(map[ObjectAddress]catalogRowShape)
	}
	s.rowShapes[ref.Address] = catalogRowShape{original: original, root: beforeRows, replacement: replacement, observedLength: observedLength, observedEnds: observedEnds, generation: observedGeneration}
	return nil
}

// Remove stages a whole user relation with its coherent observed identity.
func (s *CatalogStage) Remove(ref ObjectRef) error {
	return s.removeObject(ref, false)
}

// Schema closure uses the same removal authority for namespaces and relations.
func (s *CatalogStage) removeObject(ref ObjectRef, namespace bool) error {
	if s == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	return s.removeObjectLocked(ref, namespace)
}

// removeObjectLocked requires the owning engine mutex, including validation.
func (s *CatalogStage) removeObjectLocked(ref ObjectRef, namespace bool) error {
	if s == nil || s.lifecycle != catalogOpen || s.invalid != nil {
		return fmt.Errorf("stuntgres: invalid catalog stage")
	}
	if ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: whole object required")
	}
	if err := s.owner.validateObjectRefLocked(s.base, ref); err != nil {
		return err
	}
	d := s.base.state.descriptors[ref.Address]
	switch d.(type) {
	case *relationDescriptor:
		if ref.Address.ClassID != RelationClassID {
			return fmt.Errorf("stuntgres: only user relations can be removed")
		}
	case *namespaceDescriptor:
		if !namespace || ref.Address.ClassID != NamespaceClassID {
			return fmt.Errorf("stuntgres: only user relations can be removed")
		}
	case *typeDescriptor:
		t := d.(*typeDescriptor)
		if t.owningRelation == (ObjectAddress{}) && !t.typ.IsUserDefined() && !(t.typ.IsArray() && t.typ.Element().IsUserDefined()) {
			return fmt.Errorf("stuntgres: builtin types cannot be removed")
		}
	case *collationDescriptor:
		if d.(*collationDescriptor).seeded() {
			return sqlError("2BP01", "cannot drop "+dependencyObject(s.base, d.Address(), s.dependencyPath)+" because it is required by the database system")
		}
	case *conversionDescriptor:
		if d.(*conversionDescriptor).seeded() {
			return sqlError("2BP01", "cannot drop "+dependencyObject(s.base, d.Address(), s.dependencyPath)+" because it is required by the database system")
		}
	case *extensionDescriptor, *procedureDescriptor, *tsTemplateDescriptor, *tsDictionaryDescriptor:
		if procedure, ok := d.(*procedureDescriptor); ok && procedure.conversion.Name() != "" {
			return sqlError("2BP01", "cannot drop "+dependencyObject(s.base, d.Address(), s.dependencyPath)+" because it is required by the database system")
		}
		provider := s.owner.providers[ref.Address.ClassID]
		if provider == nil {
			return fmt.Errorf("stuntgres: missing installed object provider")
		}
		if err := provider.Validate(s.base, d); err != nil {
			return err
		}
	default:
		return fmt.Errorf("stuntgres: only user relations can be removed")
	}
	if _, exists := s.removals[ref.Address]; exists {
		return nil
	}
	key := descriptorNameKey(d)
	if s.base.state.names[key] != ref.Address {
		return CatalogConflict{}
	}
	if s.removals == nil {
		s.removals = make(map[ObjectAddress]catalogRemoval)
	}
	s.removals[ref.Address] = catalogRemoval{ref: ref, descriptor: d}
	if namespace {
		s.removeSchemaDefaults(ref.Address)
	}
	// A removal retires any rebound private candidate as well as its base
	// generation. Preserve the original removal/name observations, but do not
	// resurrect or validate that candidate beside a same-named replacement.
	if candidate := s.descriptors[ref.Address]; candidate != nil {
		stagedKey := descriptorNameKey(candidate)
		if s.names[stagedKey] == ref.Address {
			delete(s.names, stagedKey)
		}
	}
	delete(s.descriptors, ref.Address)
	if typ, ok := d.(*typeDescriptor); ok && typ.typ.IsComposite() {
		if backing, ok := s.base.state.descriptors[typ.owningRelation].(*relationDescriptor); ok && backing.relationKind == compositeRelation {
			if err := s.removeObjectLocked(ObjectRef{owner: s.owner.owner, Epoch: s.base.Epoch(), Address: backing.address}, false); err != nil {
				return err
			}
		}
	}
	if relation, ok := d.(*relationDescriptor); ok {
		if err := s.removePartitionHierarchyLocked(relation); err != nil {
			return err
		}
	}
	if r, ok := d.(*relationDescriptor); ok && r.rowType != 0 {
		row, ok := s.base.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(r.rowType)}].(*typeDescriptor)
		if !ok || row.owningRelation != r.address {
			return CatalogConflict{}
		}
		s.domainTargets = append(s.domainTargets, row.Address())
		for _, id := range []sqltype.ID{r.rowType, row.array} {
			if id != 0 {
				if err := s.removeObjectLocked(ObjectRef{owner: s.owner.owner, Epoch: s.base.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(id)}}, false); err != nil {
					return err
				}
			}
		}
	}
	s.touched[key] = ref.Address
	return nil
}
func (e *Engine) stageRead(s *CatalogStage) CatalogRead {
	ds := make(map[ObjectAddress]Descriptor, len(s.base.state.descriptors)+len(s.descriptors))
	ns := make(map[NameKey]ObjectAddress, len(s.base.state.names)+len(s.names))
	for k, v := range s.base.state.descriptors {
		ds[k] = v
	}
	for k, v := range s.base.state.names {
		ns[k] = v
	}
	for a, removal := range s.removals {
		d := removal.descriptor
		delete(ds, a)
		delete(ns, descriptorNameKey(d))
	}
	for k := range s.oldNames {
		delete(ns, k)
	}
	for k, v := range s.descriptors {
		ds[k] = v
	}
	for k, v := range s.names {
		ns[k] = v
	}
	priorities := s.base.state.dumpPriorities
	if len(priorities) != 0 && len(s.removals) != 0 {
		priorities = make(map[ObjectAddress]OID, len(s.base.state.dumpPriorities))
		for address, priority := range s.base.state.dumpPriorities {
			if _, exists := ds[address]; exists {
				priorities[address] = priority
			}
		}
	}
	return CatalogRead{owner: e, state: &catalogState{epoch: s.base.Epoch(), namespaceGeneration: s.base.state.namespaceGeneration, descriptors: ds, names: ns, defaultACLs: overlayDefaultACLs(s.base.state.defaultACLs, s.defaultACLs), dumpPriorities: priorities}}
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
		if !ok || !typ.typ.IsComposite() {
			continue
		}
		old, _ := s.base.state.descriptors[address].(*typeDescriptor)
		if old == nil || old.typ != typ.typ {
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
	if err := e.evolveStageIndexesLocked(s, ObjectAddress{}, nil); err != nil {
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
		p := e.providers[d.Address().ClassID]
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
	return e.publishCatalog(nil, p, token)
}
func (e *Engine) publishCatalogOperation(op *Operation, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
	if op != nil && op.session.transactions.retainedMessage {
		return e.stageMessageCatalog(op, p, token)
	}
	return e.publishCatalog(op, p, token)
}
func (e *Engine) publishCatalog(op *Operation, p *PreparedCatalogPublication, token PublicationToken) (ChangeSet, error) {
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
		current := e.state
		if err := s.defaultPreflight(current); err != nil {
			e.mu.Unlock()
			return ChangeSet{}, err
		}
		currentStorage := e.storage
		if p.epoch != current.epoch || token.Epoch != current.epoch {
			e.mu.Unlock()
			return ChangeSet{}, ErrCatalogReplaced
		}
		if err := s.validateIndexMaintenance(current); err != nil {
			e.mu.Unlock()
			return ChangeSet{}, err
		}
		if !typedObservationValid(s, CatalogRead{owner: e, state: current}) {
			e.mu.Unlock()
			return ChangeSet{}, CatalogConflict{}
		}
		for _, base := range s.domainBases {
			observed := base
			if s.descriptors[base.Address()] == base {
				observed = s.base.state.descriptors[base.Address()]
			}
			if current.descriptors[base.Address()] != observed {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		if err := s.validateLikeSources(CatalogRead{owner: e, state: current}); err != nil {
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
			if ns != (ObjectAddress{}) && s.descriptors[ns] == nil && current.descriptors[ns] != s.base.state.descriptors[ns] {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
			// A namespace move still relies on source lookup authority. Fence
			// that original namespace as well as the destination, without
			// republishing it or observing unrelated namespace changes.
			if old := s.base.state.descriptors[d.Address()]; old != nil {
				source := old.Namespace()
				if source != ns && source != (ObjectAddress{}) && s.descriptors[source] == nil && current.descriptors[source] != s.base.state.descriptors[source] {
					e.mu.Unlock()
					return ChangeSet{}, CatalogConflict{}
				}
			}
		}
		for a := range s.removals {
			if a.ClassID == NamespaceClassID {
				for address, d := range current.descriptors {
					if _, removed := s.removals[address]; d.Namespace() == a && (!removed || d != s.base.state.descriptors[address]) {
						e.mu.Unlock()
						return ChangeSet{}, CatalogConflict{}
					}
				}
			}
			// A newly attached MV was not in the validated retirement closure.
			// Recheck its live immutable x authority at the final publication fence.
			if a.ClassID == ExtensionClassID {
				for address, descriptor := range current.descriptors {
					if descriptor == s.base.state.descriptors[address] {
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
			if current.descriptors[a] != s.base.state.descriptors[a] {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for a := range s.removals {
			if current.descriptors[a] != s.base.state.descriptors[a] {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for k, observed := range s.touched {
			if current.names[k] != observed {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		for a, removal := range s.removals {
			observed := removal.descriptor
			d := current.descriptors[a]
			if removal.ref.owner != e.owner || removal.ref.Epoch != current.epoch || removal.ref.Address != a || d == nil || d != observed || d.Revision() != observed.Revision() {
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
			if !ok || index.sqlIndex == nil || !candidate.matches(currentStorage[index.sqlIndex.parent]) {
				e.mu.Unlock()
				return ChangeSet{}, CatalogConflict{}
			}
		}
		indexStores := captureIndexStores(currentStorage, current)
		for _, d := range s.descriptors {
			index, ok := d.(*relationDescriptor)
			if !ok || index.sqlIndex == nil {
				continue
			}
			if indexStores == nil {
				indexStores = make(map[ObjectAddress]*relationStorage)
			}
			a := index.sqlIndex.parent
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
		storage := make(map[ObjectAddress]*relationStorage, len(currentStorage)+len(s.descriptors))
		for a, v := range currentStorage {
			storage[a] = v
		}
		for a, removal := range s.removals {
			d := removal.descriptor
			delete(next.descriptors, a)
			delete(next.dumpPriorities, a)
			delete(next.names, descriptorNameKey(d))
			delete(storage, a)
		}
		for k := range s.oldNames {
			delete(next.names, k)
		}
		for a, d := range s.descriptors {
			next.descriptors[a] = d
			if relation, ok := d.(*relationDescriptor); ok && relation.relationKind.stored() && storage[a] == nil {
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
			next.names[k] = a
		}
		buildStores := indexStores
		candidateStores := false
		var rebuildIndexes map[ObjectAddress]bool
		if len(indexStores) > 0 && (len(s.rowShapes) > 0 || s.materializedReplacement != nil) {
			buildStores = make(map[ObjectAddress]*relationStorage, len(indexStores))
			for a, store := range indexStores {
				buildStores[a] = store
			}
			candidateStores = true
			rebuildIndexes = make(map[ObjectAddress]bool, len(s.rowShapes))
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
			if !ok || index.sqlIndex == nil {
				continue
			}
			a := index.sqlIndex.parent
			parent, staged := s.descriptors[a].(*relationDescriptor)
			if !staged || (parent.relationKind != tableRelation && parent.relationKind != materializedViewRelation) || current.descriptors[a] != nil {
				continue
			}
			if !candidateStores {
				buildStores = make(map[ObjectAddress]*relationStorage, len(indexStores)+1)
				for address, store := range indexStores {
					buildStores[address] = store
				}
				candidateStores = true
			}
			buildStores[a] = storage[a]
		}
		sourceRoots := indexPredecessors
		if len(s.indexCandidates) != 0 {
			sourceRoots = make(map[ObjectAddress]*logicalIndexRoot, len(indexPredecessors)+len(s.indexCandidates))
			for a, root := range indexPredecessors {
				sourceRoots[a] = root
			}
			for a, candidate := range s.indexCandidates {
				sourceRoots[a] = candidate.root
			}
		}
		var runtimeOwners map[ObjectAddress]authorization.PrincipalRef
		if r := s.materializedReplacement; r != nil && r.concurrent {
			runtimeOwners = map[ObjectAddress]authorization.PrincipalRef{r.original.address: r.original.owner}
		}
		indexRoots, indexErr := e.prepareIndexCatalog(p.ctx, current, next, buildStores, sourceRoots, rebuildIndexes, s.indexRebuilds, s.indexOutput, disclosure, runtimeOwners)
		if indexErr != nil {
			return ChangeSet{}, indexErr
		}
		domainChanged := len(s.domainTargets) != 0
		for _, d := range s.descriptors {
			if t, ok := d.(*typeDescriptor); ok && t.typ.IsUserDefined() {
				domainChanged = true
				break
			}
		}
		for _, descriptor := range next.descriptors {
			if relation, ok := descriptor.(*relationDescriptor); ok && relation.inheritance != nil {
				if err := validateInheritance(CatalogRead{owner: e, state: next}, relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if domainChanged || s.descriptors[descriptor.Address()] != nil {
				if relation, ok := descriptor.(*relationDescriptor); ok {
					if err := validateTypedAttachment(CatalogRead{owner: e, state: next}, relation); err != nil {
						return ChangeSet{}, err
					}
				}
				if err := validateTypeProviders(CatalogRead{owner: e, state: next}, descriptor); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*relationDescriptor); ok && len(relation.policies) != 0 {
				if err := validatePolicies(CatalogRead{owner: e, state: next}, relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if view, ok := descriptor.(*relationDescriptor); ok && view.view != nil {
				if err := validateViewDefinition(CatalogRead{owner: e, state: next}, view); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*relationDescriptor); ok && len(relation.checkConstraints) != 0 {
				if err := validateCheckProviders(CatalogRead{owner: e, state: next}, relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*relationDescriptor); ok && len(relation.defaults) != 0 {
				if err := validateDefaultProviders(CatalogRead{owner: e, state: next}, relation); err != nil {
					return ChangeSet{}, err
				}
			}
			if relation, ok := descriptor.(*relationDescriptor); ok && len(relation.generations) != 0 {
				if err := validateGenerationProviders(CatalogRead{owner: e, state: next}, relation); err != nil {
					return ChangeSet{}, err
				}
			}
		}
		e.mu.Lock()
		if op != nil {
			op.session.mu.Lock()
		}
		if e.state != current {
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
			if err := s.materializedReplacementCurrent(e.state, e.storage[r.original.address]); err != nil {
				if op != nil {
					op.session.mu.Unlock()
				}
				e.mu.Unlock()
				return ChangeSet{}, err
			}
		}
		err := p.ctx.Err()
		if err == nil && s.databasePredecessor != nil && e.state.descriptors[currentDatabaseAddress] != s.databasePredecessor {
			err = ErrCatalogReplaced
		}
		if err == nil {
			err = s.validateRoleCandidate(e.roles)
		}
		if err == nil && op != nil {
			err = publicationOperationLocked(e, op)
		}
		if err == nil {
			err = validateDefaultACLs(s.roleRead(e.roles, CatalogRead{e, next}), CatalogRead{e, next})
		}
		if err == nil {
			for _, d := range s.descriptors {
				if err = validateACL(s.roleRead(e.roles, CatalogRead{e, next}), d); err != nil {
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
			auth := AuthorizationView{engine: e, pairedCatalog: CatalogRead{e, e.state}, roleRecord: e.roles, catalog: CatalogRead{e, e.state}, roles: e.roleRead(CatalogRead{e, e.state}, e.roles), state: s.authSession.authorization.state}
			err = auth.Check(AccessRequirement{Object: ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: s.materializedReplacement.original.address}, Privileges: authorization.MaintainPrivilege})
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
				namespaceChanged = namespaceChanged || a.ClassID == NamespaceClassID
			}
			for a := range s.removals {
				namespaceChanged = namespaceChanged || a.ClassID == NamespaceClassID
			}
			if namespaceChanged {
				err = next.advanceNamespaceGeneration()
			}
		}
		if err == nil && (len(s.initialTables) != 0 || s.materializedReplacement != nil) && e.commitSeq == CommitSeq(math.MaxUint64) {
			err = fmt.Errorf("stuntgres: commit sequence exhausted")
		}
		if err == nil && s.initialTx != (TxRef{}) {
			err = e.serializablePrecommitLocked(s.initialTx)
		}
		if err == nil {
			// Staged replacements may advance revisions without reserving from
			// the allocator. Commit its frontier with the successful publication.
			nextRevision := e.nextRevision
			for _, d := range s.descriptors {
				if revision := descriptorRevisionFrontier(d); revision >= nextRevision {
					nextRevision = revision
					if nextRevision != DefinitionRevision(math.MaxUint64) {
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
			if s.initialTx != (TxRef{}) {
				e.serializableCommittedLocked(s.initialTx, e.commitSeq)
			}
			e.state = next
			e.storage = storage
			preserveUnchangedIndexRoots(indexRoots, indexPredecessors, e.indexRoots)
			e.indexRoots = indexRoots
			for a := range s.removals {
				delete(e.sequences, a)
			}
			for a, generator := range s.generators {
				e.sequences[a] = generator
			}
			e.nextRevision = nextRevision
			if s.roleCandidate != nil {
				e.roles = s.roleCandidate
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
func (e *Engine) ValidateObjectRef(view CatalogRead, ref ObjectRef) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.validateObjectRefLocked(view, ref)
}

// validateObjectRefLocked preserves live epoch and provenance checks without
// reacquiring synchronization when called from catalog mutation preflight.
func (e *Engine) validateObjectRefLocked(view CatalogRead, ref ObjectRef) error {
	if e.owner == nil || view.owner != e || view.state == nil {
		return fmt.Errorf("stuntgres: foreign catalog view")
	}
	if ref.owner == nil || ref.owner != e.owner {
		return fmt.Errorf("stuntgres: foreign catalog reference")
	}
	if view.state.epoch != e.state.epoch || ref.Epoch != e.state.epoch {
		return ErrCatalogReplaced
	}
	d, ok := view.state.descriptors[ObjectAddress{ClassID: ref.Address.ClassID, ObjectID: ref.Address.ObjectID}]
	if !ok {
		return fmt.Errorf("stuntgres: catalog object is missing")
	}
	if ref.Address.SubID < 0 {
		return fmt.Errorf("stuntgres: invalid catalog subobject")
	}
	if ref.Address.SubID > 0 {
		r, ok := d.(*relationDescriptor)
		if !ok || !r.hasAttribute(ref.Address.SubID) {
			return fmt.Errorf("stuntgres: invalid catalog subobject")
		}
	}
	return nil
}
func (e *Engine) ReplaceCatalog(ctx context.Context, replacement CatalogRead) (ChangeSet, error) {
	return e.replaceCatalog(ctx, replacement, nil)
}

// Restore retains its genuine private operation through owner-cell validation.
func (e *Engine) replaceCatalog(ctx context.Context, replacement CatalogRead, restoreOp *Operation) (ChangeSet, error) {
	var installed CatalogEpoch
	defer func() {
		if installed != 0 {
			e.locks.fence(installed)
		}
	}()
	if replacement.state == nil || replacement.owner == nil {
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
	for _, d := range replacement.state.descriptors {
		p := e.providers[d.Address().ClassID]
		if p == nil {
			return ChangeSet{}, fmt.Errorf("stuntgres: unregistered catalog class %d", d.Address().ClassID)
		}
		if err := p.Validate(replacement, d); err != nil {
			return ChangeSet{}, err
		}
	}
	var next *catalogState
	relationAddresses := make([]ObjectAddress, 0, len(replacement.state.descriptors))
	hasHierarchy := false
	for a, d := range replacement.state.descriptors {
		if relation, ok := d.(*relationDescriptor); ok && hierarchyRelation(relation) {
			hasHierarchy = true
		}
		if r, ok := d.(*relationDescriptor); ok && r.relationKind.stored() {
			relationAddresses = append(relationAddresses, a)
		}
	}
	storage := make(map[ObjectAddress]*relationStorage, len(relationAddresses))
	var sequences map[ObjectAddress]*sequenceGenerator
	copyRows := func(source map[ObjectAddress]*relationStorage, frontier CommitSeq) error {
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
		return nil
	}
	if replacement.owner == e {
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
		if replacement.Epoch() != e.state.epoch {
			return ChangeSet{}, ErrCatalogReplaced
		}
		next = cloneCatalog(replacement.state)
		if err := copyRows(e.storage, e.commitSeq); err != nil {
			return ChangeSet{}, err
		}
		var sequenceErr error
		sequences, sequenceErr = copySequenceState(e, replacement)
		if sequenceErr != nil {
			return ChangeSet{}, sequenceErr
		}
		if err := e.preserveTemporaryLocked(ctx, next, storage, sequences, restoreOp); err != nil {
			return ChangeSet{}, err
		}
		if err := validateDefaultACLs(e.roleRead(CatalogRead{e, next}, e.roles), CatalogRead{e, next}); err != nil {
			return ChangeSet{}, err
		}
		nextRevision := e.nextRevision
		for _, d := range next.descriptors {
			if err := validateACL(e.roleRead(CatalogRead{e, next}, e.roles), d); err != nil {
				return ChangeSet{}, err
			}
			if revision := descriptorRevisionFrontier(d); revision >= nextRevision {
				nextRevision = revision
				if nextRevision != DefinitionRevision(math.MaxUint64) {
					nextRevision++
				}
			}
		}
		if e.state.epoch == CatalogEpoch(math.MaxUint64) {
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
		if e.commitSeq == CommitSeq(^uint64(0)) {
			return ChangeSet{}, fmt.Errorf("stuntgres: commit sequence exhausted")
		}
		if hasHierarchy {
			candidate := CatalogRead{e, next}
			for _, descriptor := range next.descriptors {
				if relation, ok := descriptor.(*relationDescriptor); ok && hierarchyRelation(relation) {
					if err := validatePartitionDescriptor(candidate, relation); err != nil {
						return ChangeSet{}, err
					}
				}
			}
		}
		roots, nextRow, nextVersion, err := e.prepareRemintedStorage(ctx, next, storage)
		if err != nil {
			return ChangeSet{}, err
		}
		if err := ctx.Err(); err != nil {
			return ChangeSet{}, err
		}
		e.nextRow, e.nextVersion = nextRow, nextVersion
		e.indexRoots = roots
		e.commitSeq++
		next.epoch = e.state.epoch + 1
		e.state = next
		e.storage = storage
		e.exclusionPending = nil
		e.exclusionGeneration++
		e.sequences = sequences
		e.clearPersistentSequenceMemoryLocked()
		e.nextRevision = nextRevision
		e.fenceSerializableLocked()
		installed = next.epoch
		return ChangeSet{Replacement: true}, nil
	}
	replacement.owner.mu.Lock()
	if replacement.Epoch() != replacement.owner.state.epoch {
		replacement.owner.mu.Unlock()
		return ChangeSet{}, ErrCatalogReplaced
	}
	err = copyRows(replacement.owner.storage, replacement.owner.commitSeq)
	if err == nil {
		sequences, err = copySequenceState(replacement.owner, replacement)
	}
	sourceRoles := replacement.owner.roleRead(replacement, replacement.owner.roles)
	replacement.owner.mu.Unlock()
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
	if e.state.epoch == CatalogEpoch(math.MaxUint64) {
		return ChangeSet{}, fmt.Errorf("stuntgres: catalog epoch exhausted")
	}
	if imported > ^uint64(0)-e.nextRow {
		return ChangeSet{}, fmt.Errorf("stuntgres: row identity exhausted")
	}
	if imported > ^uint64(0)-e.nextVersion {
		return ChangeSet{}, fmt.Errorf("stuntgres: version identity exhausted")
	}
	if e.commitSeq == CommitSeq(^uint64(0)) {
		return ChangeSet{}, fmt.Errorf("stuntgres: commit sequence exhausted")
	}
	replacement.state.aclItemImport = e.aclItemImporter(replacement.owner, sourceRoles, targetRoles, replacement.owner.importTargets)
	defer func() { replacement.state.aclItemImport = nil }()
	next, err = e.remapPreparedCatalog(replacement.state, sourceRoles, targetRoles, replacement.owner.importTargets)
	if err != nil {
		return ChangeSet{}, err
	}
	if err = e.preserveTemporaryLocked(ctx, next, storage, sequences, restoreOp); err != nil {
		return ChangeSet{}, err
	}
	if err := validateDefaultACLs(e.roleRead(CatalogRead{e, next}, e.roles), CatalogRead{e, next}); err != nil {
		return ChangeSet{}, err
	}
	nextRevision := e.nextRevision
	for _, d := range next.descriptors {
		if err := validateACL(e.roleRead(CatalogRead{e, next}, e.roles), d); err != nil {
			return ChangeSet{}, err
		}
		if revision := descriptorRevisionFrontier(d); revision >= nextRevision {
			nextRevision = revision
			if nextRevision != DefinitionRevision(math.MaxUint64) {
				nextRevision++
			}
		}
	}
	next.aclItemImport = nil
	if err := e.installForeignCatalogLocked(ctx, next, storage, sequences, nextRevision); err != nil {
		return ChangeSet{}, err
	}
	installed = next.epoch
	return ChangeSet{Replacement: true}, nil
}

// installForeignCatalogLocked owns the final cancellation boundary and the
// indivisible installation of a prepared foreign generation. The caller holds
// replacementMu and mu and has validated capacity, descriptors and authority.
// Storage vectors and sequence generator scalars are private copies; payload
// values remain immutable. All prepared containers publish at the same boundary.
func (e *Engine) installForeignCatalogLocked(ctx context.Context, next *catalogState, storage map[ObjectAddress]*relationStorage, sequences map[ObjectAddress]*sequenceGenerator, nextRevision DefinitionRevision) error {
	// Fresh engine-local identities/frontier for this replacement. Old snapshots
	// retain source versions; no source payload or published entry is rewritten.
	roots, nextRow, nextVersion, err := e.prepareRemintedStorage(ctx, next, storage)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.nextRow, e.nextVersion = nextRow, nextVersion
	e.indexRoots = roots
	e.commitSeq++
	next.epoch = e.state.epoch + 1
	e.state = next
	e.storage = storage
	e.exclusionPending = nil
	e.exclusionGeneration++
	e.sequences = sequences
	e.clearPersistentSequenceMemoryLocked()
	e.nextRevision = nextRevision
	e.fenceSerializableLocked()
	return nil
}

func cloneCatalog(s *catalogState) *catalogState {
	n := &catalogState{epoch: s.epoch, namespaceGeneration: s.namespaceGeneration, descriptors: make(map[ObjectAddress]Descriptor, len(s.descriptors)), names: make(map[NameKey]ObjectAddress, len(s.names))}
	n.defaultACLs = s.defaultACLs
	for k, v := range s.descriptors {
		n.descriptors[k] = v
	}
	for k, v := range s.names {
		n.names[k] = v
	}
	if len(s.dumpPriorities) != 0 {
		n.dumpPriorities = make(map[ObjectAddress]OID, len(s.dumpPriorities))
		for address, priority := range s.dumpPriorities {
			if _, exists := n.descriptors[address]; exists {
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
	if e.state != nil {
		return fmt.Errorf("stuntgres: catalog providers are already initialized")
	}
	if e.providers == nil {
		return fmt.Errorf("stuntgres: engine is not initialized")
	}
	if _, ok := e.providers[id]; ok {
		return fmt.Errorf("stuntgres: duplicate catalog class")
	}
	e.providers[id] = p
	return nil
}

func descriptorRevisionFrontier(d Descriptor) DefinitionRevision {
	revision := d.Revision()
	if relation, ok := d.(*relationDescriptor); ok && relation.sequencePlanStamp > revision {
		revision = relation.sequencePlanStamp
	}
	return revision
}
