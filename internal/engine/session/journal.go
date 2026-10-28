package session

import (
	"maps"

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
	ParameterWritten bool
	ParameterIncarnation uint64
	Originals        map[ids.ObjectAddress]catalog.Descriptor
	Descriptors      map[ids.ObjectAddress]catalog.Descriptor
	Names            map[ids.NameKey]ids.ObjectAddress
	DefaultACLs      map[catalog.DefaultACLKey]*catalog.DefaultACLRecord
	DefaultOriginals map[catalog.DefaultACLKey]*catalog.DefaultACLRecord
	DefaultReads     map[catalog.DefaultACLKey]*catalog.DefaultACLRecord
	Generators       map[ids.ObjectAddress]*storage.SequenceGenerator
	Storage          map[ids.ObjectAddress]StorageChange
	Dependencies     []DependencyObservation
	CachedBase       *catalog.State
	Cached           catalog.Read
	Enum             EnumSlot
	RolesBase        *access.RoleRecord
	Roles            *access.RoleRecord
	RetiredRoles     map[authorization.PrincipalRef]struct{}
	Fences           []Fence // Feature commit preconditions, in recording order.
	Domain           DomainSlot
	DomainChanged    bool
	Indexes          IndexSlot
	Statistics       map[ids.ObjectAddress]StatisticsChange
}

// StorageChange is a relation's private storage in the journal.
type StorageChange struct {
	Original    *storage.Relation
	Root        []storage.RowVersion
	Ends        *storage.DeletionRoot
	Generation  uint64
	Replacement *storage.Relation
	Relation    *catalog.Relation
	Snapshot    []storage.RowVersion
	HasSnapshot bool
	Rewritten   bool
	Truncated   bool
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

// NewJournal returns an empty journal.
func NewJournal() *Journal {
	return &Journal{Originals: map[ids.ObjectAddress]catalog.Descriptor{}, Descriptors: map[ids.ObjectAddress]catalog.Descriptor{}, Names: map[ids.NameKey]ids.ObjectAddress{}, Generators: map[ids.ObjectAddress]*storage.SequenceGenerator{}, Storage: map[ids.ObjectAddress]StorageChange{}}
}

// Clone copies c for a new statement candidate; a nil journal clones to an
// empty one. Maps are copied, slots are cloned, and fences are shared.
func (c *Journal) Clone() *Journal {
	n := NewJournal()
	if c == nil {
		return n
	}
	n.ParameterWritten, n.ParameterIncarnation = c.ParameterWritten, c.ParameterIncarnation
	cloneDefaults(c, n)
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
	return n
}

func cloneDefaults(source *Journal, next *Journal) {
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
