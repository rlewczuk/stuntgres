package catalog

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

type Descriptor interface {
	Address() ids.ObjectAddress
	Revision() ids.DefinitionRevision
	Namespace() ids.ObjectAddress
	Name() string
	Kind() ids.ObjectKind
}

type Namespace struct {
	ObjectAddress  ids.ObjectAddress
	Extension      ids.ObjectAddress
	ObjectName     string
	ObjectRevision ids.DefinitionRevision
	Owner          authorization.PrincipalRef
	ACL            authorization.ACLValue
	Comment        string
	Temporary      ids.SessionToken
	TemporaryToast bool
}

func (d *Namespace) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *Namespace) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (*Namespace) Namespace() ids.ObjectAddress { return ids.ObjectAddress{} }

func (d *Namespace) Name() string { return d.ObjectName }

func (*Namespace) Kind() ids.ObjectKind { return ids.NamespaceObject }

// Relation is a pg_class entry: a table, partitioned table, view,
// materialized view, index, sequence, composite type, foreign table or
// catalog relation. Its sub-records are values,
// so copying a Relation shares their slices and maps exactly as it shares
// its top-level ones (copy-on-write).
type Relation struct {
	ObjectAddress, ObjectNamespace ids.ObjectAddress
	Extension                      ids.ObjectAddress
	ObjectName                     string
	ObjectRevision                 ids.DefinitionRevision
	RelationKind                   RelationKind
	Persistence                    string  // Empty is logged; TEMP provenance remains namespace-owned.
	TableMethod                    ids.OID // Exact table AM; partition roots alone may have no explicit default (zero).
	Owner                          authorization.PrincipalRef
	Comment                        string
	AutoExtensions                 []ids.ObjectAddress // Immutable whole-object AUTO_EXTENSION authority.

	Columns    []Column
	Index      map[string]int
	Attributes []int32
	RowType    sqltype.ID
	OfType     ids.ObjectAddress

	ColumnMeta      ColumnMetadata
	Constraints     RelationConstraints
	Security        RelationSecurity
	History         PublicationHistory
	StorageOptions  RelationOptions
	ReplicaIdentity parser.ReplicaIdentityMode

	// Kind payloads.
	SQLIndex          *IndexDefinition
	Partition         *PartitionDefinition
	Membership        *PartitionMembership
	Inheritance       *TableInheritance
	Sequence          *SequenceOptions
	SequencePlanStamp ids.DefinitionRevision
	View              ViewPayload
	VirtualView       bool
	CatalogView       bool    // Catalog relation SQL kind, independent of execution dispatch.
	CatalogReturnRule ids.OID // Metadata-only bootstrap _RETURN; never stored SQL authority.
	Populated         bool    // Independent of stored row count, including populated-empty MVs.
	Rules             RulesPayload
	Triggers          TriggersPayload
	Foreign           *ForeignTableDefinition
	OnCommit          parser.OnCommitAction
}

// ColumnMetadata holds per-attribute metadata keyed by attribute number,
// beside the relation's live Columns and Attributes.
type ColumnMetadata struct {
	HighWater         int32 // Highest attribute number ever assigned.
	Dropped           map[int32]AttributeMetadata
	CatalogAttributes []CatalogAttribute
	Comments          map[int32]string
	Defaults          map[int32]ColumnDefault
	Generations       map[int32]ColumnGeneration
	Identities        map[int32]ColumnIdentity
	Storage           map[int32]ColumnStorage
}

// RelationConstraints holds a relation's table constraints. PhysicalNotNull
// records attributes that stay physically NOT NULL without a NOT NULL
// constraint row (after TYPE cleanup or its import counterpart).
type RelationConstraints struct {
	NotNull         []NotNullConstraint
	PhysicalNotNull map[int32]PhysicalNotNullProof
	Check           []CheckConstraint
	Unique          []UniqueConstraint
	Exclusion       []ExclusionConstraint
	ForeignKeys     []ForeignKeyConstraint
}

// RelationSecurity holds a relation's access control: its ACL, column ACLs
// and row-level security.
type RelationSecurity struct {
	ACL        authorization.ACLValue
	ColumnACL  map[int32]authorization.ACLValue
	Policies   []PolicyDefinition
	RLSEnabled bool
	RLSForced  bool
}

// PublicationHistory retains lazy catalog facts. Ordinary descriptor replacement
// preserves them; applicable VACUUM alone may clear HasIndexes after the final
// attached index is gone.
type PublicationHistory struct {
	HasIndexes          bool      // DROP retains it until applicable VACUUM.
	HasSubclasses       bool      // Inheritance, table or index partition publication.
	SubclassPredecessor *Relation // Exact first-subclass marker publication only.
	SubclassPublication ids.DefinitionRevision
	// Exact one-step pending-detach/CHECK publication proof. The revision stamp
	// makes later ordinary clones fail even when they retain this history.
	DetachPredecessor *Relation
	DetachPublication ids.DefinitionRevision
	DetachRoot        ids.ObjectAddress
}

func (d *Relation) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *Relation) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (d *Relation) Namespace() ids.ObjectAddress { return d.ObjectNamespace }

func (d *Relation) Name() string { return d.ObjectName }

func (*Relation) Kind() ids.ObjectKind { return ids.RelationObject }

func (d *Relation) HasAttribute(sub int32) bool {
	for _, live := range d.Attributes {
		if live == sub {
			return true
		}
	}
	return false
}

type NotNullConstraint struct {
	OID             ids.OID
	Name            string
	Attribute       int32
	NotValid        bool
	NoInherit       bool
	PartitionOrigin *InheritedConstraint
}

type State struct {
	epoch                ids.CatalogEpoch
	namespaceGeneration  uint64
	parameterIncarnation uint64
	descriptors          util.PersistentMap[ids.ObjectAddress, Descriptor]
	names                util.PersistentMap[ids.NameKey, ids.ObjectAddress]
	relations            util.PersistentMap[ids.ObjectAddress, *Relation]
	bulk                 *stateBulk
	operatorNames        *OperatorNameIndex
	procedureNames       *ProcedureNameIndex
	castPairs            map[CastPair]*Cast
	defaultClasses       util.PersistentMap[defaultClassPair, []ids.OID]
	eventTriggers        util.PersistentMap[ids.ObjectAddress, *EventTrigger]
	subscriptions        util.PersistentMap[ids.ObjectAddress, *Subscription]
	helperOwners         util.PersistentMap[ids.ObjectAddress, *Procedure]
	temporaryNamespaces  util.PersistentMap[ids.ObjectAddress, *Namespace]
	oidCeiling           ids.OID                                           // See oid_ceiling.go.
	unseeded             util.PersistentMap[ids.ObjectAddress, Descriptor] // See unseeded_index.go.
	unseededAll          bool                                              // Every entry is unseeded.
	seedCeiling          ids.OID                                           // oidCeiling when the seed ended.
	defaultACLs          map[DefaultACLKey]*DefaultACLRecord
	initialPrivileges    map[ids.ObjectAddress]*InitialPrivilegeRecord
	// Immutable seed comparison provenance, never live inventory/dependency data.
	initialPrivilegeBaseline map[ids.ObjectAddress]*InitialPrivilegeRecord
	// Sparse replay identity provenance for collision-remapped declarations.
	// Absent entries use their current OID; dependencies remain the order authority.
	dumpPriorities map[ids.ObjectAddress]ids.OID
	// Replacement-candidate-only translation, cleared before publication.
	// Keys are exact source codecs, never numeric identity coincidences.
	scalarImports map[*sqltype.Descriptor]*sqltype.Descriptor
	aclItemImport func(sqltype.Value) (sqltype.Value, error)
}

type Read struct {
	owner *Owner
	state *State
}

// Domain returns the provenance domain of the view's owner, or nil for a
// zero view.
func (r Read) Domain() *ids.Domain {
	if r.owner == nil {
		return nil
	}
	return r.owner.Domain
}

func (r Read) Epoch() ids.CatalogEpoch {
	if r.state == nil {
		return 0
	}
	return r.state.epoch
}

func (r Read) Descriptor(a ids.ObjectAddress) (Descriptor, bool) {
	if r.state == nil {
		return nil, false
	}
	d, ok := r.Find(a)
	return d, ok
}

func (r Read) Lookup(k ids.NameKey) []ids.ObjectAddress {
	if r.state == nil {
		return nil
	}
	a, ok := r.LookupName(k)
	if !ok {
		return nil
	}
	return []ids.ObjectAddress{a}
}

type Provider interface {
	ClassID() ids.OID
	Validate(Read, Descriptor) error
}

type NamespaceProvider struct{}

func (NamespaceProvider) ClassID() ids.OID { return ids.NamespaceClassID }

func (NamespaceProvider) Validate(v Read, d Descriptor) error {
	ns, ok := d.(*Namespace)
	if !ok || ns == nil || ns.TemporaryToast && ns.Temporary == (ids.SessionToken{}) || v.state == nil || v.Get(ns.Address()) != ns {
		return fmt.Errorf("stuntgres: invalid namespace descriptor authority")
	}
	if ns.Temporary != (ids.SessionToken{}) && (ns.Temporary.Domain() != v.Domain() || ns.Temporary.ID() == 0 || ns.Temporary.Private() != nil) {
		return fmt.Errorf("stuntgres: invalid temporary namespace provenance")
	}
	a := d.Address()
	key := DescriptorNameKey(d)
	found := v.Lookup(key)
	if d.Kind() != ids.NamespaceObject || a.ClassID != ids.NamespaceClassID || a.ObjectID == 0 || a.SubID != 0 || d.Revision() == 0 || d.Namespace() != (ids.ObjectAddress{}) || len(found) != 1 || found[0] != a {
		return fmt.Errorf("stuntgres: invalid namespace descriptor")
	}
	return nil
}

type Conflict struct{}

func (Conflict) Error() string { return "stuntgres: catalog publication conflict" }

// CloneState returns a writable copy of s. The copy shares s's descriptor and
// name maps, which are persistent: writing to either state never changes the
// other.
func CloneState(s *State) *State {
	s.mustNotBulkLoad()
	n := &State{epoch: s.epoch, namespaceGeneration: s.namespaceGeneration, descriptors: s.descriptors, names: s.names, relations: s.relations}
	n.parameterIncarnation = s.parameterIncarnation
	n.defaultACLs = s.defaultACLs
	n.initialPrivileges = s.initialPrivileges
	n.initialPrivilegeBaseline = s.initialPrivilegeBaseline
	n.operatorNames = s.operatorNames
	n.procedureNames = s.procedureNames
	n.castPairs = s.castPairs
	n.defaultClasses = s.defaultClasses
	n.eventTriggers = s.eventTriggers
	n.subscriptions = s.subscriptions
	n.helperOwners = s.helperOwners
	n.temporaryNamespaces = s.temporaryNamespaces
	n.oidCeiling = s.oidCeiling
	n.unseeded, n.unseededAll, n.seedCeiling = s.unseeded, s.unseededAll, s.seedCeiling
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
func (s *State) AdvanceNamespaceGeneration() error {
	if s.namespaceGeneration == math.MaxUint64 {
		return fmt.Errorf("stuntgres: namespace invalidation generation exhausted")
	}
	s.namespaceGeneration++
	return nil
}
