package session

import (
	"maps"
	"sync"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Journal is a transaction's private catalog overlay: transactional
// descriptors, row shapes and generators. Original descriptors and name
// bindings are commit preconditions, not undo data.
//
// The transaction owner holds the journal. It is copy-on-write: a statement
// writes a Clone and installs it, and savepoint marks keep older journals.
// The root engine validates and publishes it under Engine.mu.
type Journal struct {
	ParameterWritten     bool
	ParameterIncarnation uint64
	Originals            map[ids.ObjectAddress]catalog.Descriptor
	Descriptors          map[ids.ObjectAddress]catalog.Descriptor
	Names                map[ids.NameKey]ids.ObjectAddress
	DefaultACLs          map[catalog.DefaultACLKey]*catalog.DefaultACLRecord
	DefaultOriginals     map[catalog.DefaultACLKey]*catalog.DefaultACLRecord
	DefaultReads         map[catalog.DefaultACLKey]*catalog.DefaultACLRecord
	InitialPrivileges    map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord
	InitialOriginals     map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord
	InitialReads         map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord
	SecurityLabels       map[catalog.SecurityLabelKey]*catalog.SecurityLabelRecord
	LabelOriginals       map[catalog.SecurityLabelKey]*catalog.SecurityLabelRecord
	LabelReads           map[catalog.SecurityLabelKey]*catalog.SecurityLabelRecord
	Generators           map[ids.ObjectAddress]*storage.SequenceGenerator
	Storage              map[ids.ObjectAddress]StorageChange
	// Deferred genuine tuple debris retains its exact heap incarnation until
	// owner disposal, without invalidating this journal's own storage fences.
	DiscardedHeaps []HeapDiscard
	Dependencies   []DependencyObservation
	// Engine.mu protects direct cache access. cacheMu also serializes cache
	// updates with detached Clone, without covering any slot Clone callback.
	CachedBase    *catalog.State
	Cached        catalog.Read
	cacheMu       *sync.Mutex
	Enum          EnumSlot
	RolesBase     *access.RoleRecord
	Roles         *access.RoleRecord
	RetiredRoles  map[authorization.PrincipalRef]struct{}
	Fences        []Fence // Feature commit preconditions, in recording order.
	Domain        DomainSlot
	DomainChanged bool
	Indexes       IndexSlot
	Statistics    map[ids.ObjectAddress]StatisticsChange

	// Immutable descriptor-edit history: one shared node per RecordDescriptor
	// write and the number of distinct descriptor addresses it covers.
	descriptorEdits    *descriptorEdit
	descriptorEditKeys int
}

// StorageChange is a relation's private storage in the journal.
type StorageChange struct {
	Original *storage.Relation
	Root     []storage.RowVersion
	Ends     *storage.DeletionRoot
	// First actual preserving-copy VM observation. Earlier same-store metadata
	// is not a VM snapshot; later copies/savepoints never refresh this authority.
	ObservedVisibility *storage.VisibilityRoot
	ObservedAuxiliary  storage.AuxiliaryForks
	// PreservesVisibility records continuous codec-only transport from Original.
	// A metadata-only same-pointer step keeps continuity; a destructive copy ends it.
	PreservesVisibility bool
	Generation          uint64
	Replacement         *storage.Relation
	Relation            *catalog.Relation
	Snapshot            []storage.RowVersion
	HasSnapshot         bool
	Rewritten           bool
	Truncated           bool
	// Every actual TRUNCATE resets scan authority, independently of whether
	// Truncated can certify uninterrupted original-to-final heap lineage.
	TruncateScanReset bool
	Frontier          ids.CommitSeq
	Fills             map[int32]sqltype.Value
	// Latest replacement's diagnostic mode is distinct from uninterrupted
	// original-to-final concurrent provenance. A plain rewrite breaks the latter.
	MaterializedConcurrent bool
	MaterializedContinuous bool
}

// HeapDiscard owns immutable created versions lost by a private stage/root.
// Storage, not Address alone, decides whether disposal may retain the slots.
type HeapDiscard struct {
	Address  ids.ObjectAddress
	Storage  *storage.Relation
	Versions *storage.HeapPredecessor
}

// MergeFills merges the added-column fills into the change and drops the
// fills of attributes that next no longer has. It replaces, never mutates, the
// change's fill map, which earlier journals may share.
func (c *StorageChange) MergeFills(fills map[int32]sqltype.Value, next *catalog.Relation) {
	if len(fills) == 0 && len(c.Fills) == 0 {
		return
	}
	merged := maps.Clone(c.Fills)
	if merged == nil {
		merged = make(map[int32]sqltype.Value, len(fills))
	}
	maps.Copy(merged, fills)
	maps.DeleteFunc(merged, func(attribute int32, _ sqltype.Value) bool { return !next.HasAttribute(attribute) })
	c.Fills = merged
}

// DependencyObservation records the dependency targets a statement observed
// in a catalog state, checked again at commit.
type DependencyObservation struct {
	Base    *catalog.State
	Targets []ids.ObjectAddress
}

// NewJournal returns an empty shareable journal. Its leaf cache mutex shares
// the journal's allocation; header-only copies retain that same mutex pointer.
// A zero-value journal is only for unshared, externally serialized scratch use.
func NewJournal() *Journal {
	n := &struct {
		Journal
		cache sync.Mutex
	}{Journal: Journal{Originals: map[ids.ObjectAddress]catalog.Descriptor{}, Descriptors: map[ids.ObjectAddress]catalog.Descriptor{}, Names: map[ids.NameKey]ids.ObjectAddress{}, Generators: map[ids.ObjectAddress]*storage.SequenceGenerator{}, Storage: map[ids.ObjectAddress]StorageChange{}}}
	n.Journal.cacheMu = &n.cache
	return &n.Journal
}

// Clone copies c for a new statement candidate; a nil journal clones to an
// empty one. Maps and the fence slice are copied, slots are cloned, and immutable
// fence payloads and the cached private catalog State are shared.
// The source's maps and slots must be stable under their existing owner
// contract. Cache capture alone uses its leaf mutex: foreign catalog readers
// may refresh the cache under Engine.mu while this clone runs detached.
func (c *Journal) Clone() *Journal {
	n := NewJournal()
	if c == nil {
		return n
	}
	n.ParameterWritten, n.ParameterIncarnation = c.ParameterWritten, c.ParameterIncarnation
	n.DiscardedHeaps = c.DiscardedHeaps[:len(c.DiscardedHeaps):len(c.DiscardedHeaps)]
	cloneDefaults(c, n)
	n.SecurityLabels = maps.Clone(c.SecurityLabels)
	n.LabelOriginals = maps.Clone(c.LabelOriginals)
	n.LabelReads = maps.Clone(c.LabelReads)
	if c.Enum != nil {
		n.Enum = c.Enum.Clone()
	}
	for a, d := range c.Originals {
		n.Originals[a] = d
	}
	for a, d := range c.Descriptors {
		n.Descriptors[a] = d
	}
	for k, a := range c.Names {
		n.Names[k] = a
	}
	for a, g := range c.Generators {
		n.Generators[a] = g
	}
	for a, shape := range c.Storage {
		n.Storage[a] = shape
	}
	for a, change := range c.Statistics {
		if n.Statistics == nil {
			n.Statistics = make(map[ids.ObjectAddress]StatisticsChange, len(c.Statistics))
		}
		n.Statistics[a] = change
	}
	if c.Indexes != nil {
		n.Indexes = c.Indexes.Clone()
	}
	n.Dependencies = append(n.Dependencies, c.Dependencies...)
	n.RolesBase, n.Roles = c.RolesBase, c.Roles
	if len(c.RetiredRoles) != 0 {
		n.RetiredRoles = make(map[authorization.PrincipalRef]struct{}, len(c.RetiredRoles))
		for ref := range c.RetiredRoles {
			n.RetiredRoles[ref] = struct{}{}
		}
	}
	n.DomainChanged = c.DomainChanged
	n.Fences = append(n.Fences, c.Fences...)
	if c.Domain != nil {
		n.Domain = c.Domain.Clone()
	}
	c.lockPrivateRead()
	n.CachedBase, n.Cached = c.CachedBase, c.Cached
	c.unlockPrivateRead()
	n.descriptorEdits, n.descriptorEditKeys = c.descriptorEdits, c.descriptorEditKeys
	return n
}

// The cache lock is a leaf: never acquire an owner/engine lock or invoke a
// journal slot callback while holding it. Scratch zero-value journals remain
// externally serialized; every published journal is constructed by NewJournal.
func (c *Journal) lockPrivateRead() {
	if c.cacheMu != nil {
		c.cacheMu.Lock()
	}
}

func (c *Journal) unlockPrivateRead() {
	if c.cacheMu != nil {
		c.cacheMu.Unlock()
	}
}

func cloneDefaults(source *Journal, next *Journal) {
	if len(source.InitialPrivileges) != 0 {
		next.InitialPrivileges = catalog.CloneInitialPrivileges(source.InitialPrivileges)
		next.InitialOriginals = catalog.CloneInitialPrivileges(source.InitialOriginals)
	}
	if len(source.InitialReads) != 0 {
		next.InitialReads = catalog.CloneInitialPrivileges(source.InitialReads)
	}
	if len(source.DefaultACLs) != 0 {
		next.DefaultACLs = catalog.CloneDefaultACLs(source.DefaultACLs)
		next.DefaultOriginals = catalog.CloneDefaultACLs(source.DefaultOriginals)
	}
	if len(source.DefaultReads) != 0 {
		next.DefaultReads = catalog.CloneDefaultACLs(source.DefaultReads)
	}
}

// AddFence records f after the fences already recorded.
func (c *Journal) AddFence(f Fence) {
	c.Fences = append(c.Fences, f)
}
