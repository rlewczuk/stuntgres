package catalog

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
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

type Relation struct {
	ObjectAddress, ObjectNamespace ids.ObjectAddress
	ObjectName                     string
	ObjectRevision                 ids.DefinitionRevision
	SequencePlanStamp              ids.DefinitionRevision
	Columns                        []Column
	Index                          map[string]int
	Attributes                     []int32
	AttributeHighWater             int32
	DroppedAttributes              map[int32]AttributeMetadata
	CatalogAttributes              []CatalogAttribute
	Owner                          authorization.PrincipalRef
	ACL                            authorization.ACLValue
	ColumnACL                      map[int32]authorization.ACLValue
	Comment                        string
	ColumnComments                 map[int32]string
	NotNullConstraints             []NotNullConstraint
	PhysicalNotNull                map[int32]PhysicalNotNullProof
	CheckConstraints               []CheckConstraint
	Policies                       []PolicyDefinition
	Rules                          RulesPayload
	RLSEnabled                     bool
	RLSForced                      bool
	UniqueConstraints              []UniqueConstraint
	ExclusionConstraints           []ExclusionConstraint
	ForeignKeys                    []ForeignKeyConstraint
	Defaults                       map[int32]ColumnDefault
	Generations                    map[int32]ColumnGeneration
	Identities                     map[int32]ColumnIdentity
	Inheritance                    *TableInheritance
	RelationKind                   RelationKind
	HasIndexes                     bool      // Sticky publication history; removing an index does not clear it.
	HasSubclasses                  bool      // Sticky inheritance/table/index partition publication history.
	SubclassPredecessor            *Relation // Exact first-subclass marker publication only.
	SubclassPublication            ids.DefinitionRevision
	OnCommit                       parser.OnCommitAction
	Sequence                       *SequenceOptions
	View                           ViewPayload
	Populated                      bool                // Independent of stored row count, including populated-empty MVs.
	AutoExtensions                 []ids.ObjectAddress // Immutable whole-object AUTO_EXTENSION authority.
	VirtualView                    bool
	SQLIndex                       *IndexDefinition
	Partition                      *PartitionDefinition
	Membership                     *PartitionMembership
	RowType                        sqltype.ID
	OfType                         ids.ObjectAddress
	Foreign                        *ForeignTableDefinition
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
	OID            ids.OID
	Name           string
	Attribute      int32
	NotValid       bool
	NoInherit      bool
	PartitionLocal bool // Explicit NN declaration merged with an inherited definition.
}

type State struct {
	epoch               ids.CatalogEpoch
	namespaceGeneration uint64
	descriptors         map[ids.ObjectAddress]Descriptor
	names               map[ids.NameKey]ids.ObjectAddress
	operatorNames       *OperatorNameIndex
	defaultACLs         map[DefaultACLKey]*DefaultACLRecord
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

func CloneState(s *State) *State {
	n := &State{epoch: s.epoch, namespaceGeneration: s.namespaceGeneration, descriptors: make(map[ids.ObjectAddress]Descriptor, s.DescriptorCount()), names: make(map[ids.NameKey]ids.ObjectAddress, s.NameCount())}
	n.defaultACLs = s.defaultACLs
	n.operatorNames = s.operatorNames
	for k, v := range s.Descriptors() {
		n.SetDescriptor(k, v)
	}
	for k, v := range s.Names() {
		n.BindName(k, v)
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
func (s *State) AdvanceNamespaceGeneration() error {
	if s.namespaceGeneration == math.MaxUint64 {
		return fmt.Errorf("stuntgres: namespace invalidation generation exhausted")
	}
	s.namespaceGeneration++
	return nil
}
