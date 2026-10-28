package index

import (
	"context"
	"maps"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// These immutable projections are visibility, not lock grants. Engine.mu owns
// their ticket membership; all providers and completion waits run unlocked.
type ExclusionEntry struct {
	Row      storage.RowVersion
	Keys     []sqltype.Value
	Admitted bool
}

type ExclusionProjection struct {
	Entries map[ids.ObjectAddress][]ExclusionEntry
	Ended   map[ids.ObjectAddress]map[ids.RowID]ids.VersionID
}

// ConstraintEvent is one index conflict obligation or one FK trigger event.
// FK payloads own immutable accepted row images and original catalog references.
type ConstraintEvent struct {
	Parent, Index ids.ObjectRef
	Constraint    ids.OID
	Row           ids.RowID
	Version       ids.VersionID
	ForeignKey    *ForeignKeyEvent
}

type ForeignKeyEvent struct {
	Child, Parent, Index ids.ObjectRef
	Old, Next            storage.RowVersion
	Owner                authorization.PrincipalRef
	ParentEvent          bool
	Definition           catalog.ForeignKeyConstraint
}

type ConstraintModes struct {
	AllSet, AllDeferred bool
	ByConstraint        map[ids.OID]bool
	Events              []ConstraintEvent
}

// Zero reports whether m is exactly the zero value: no flags, and a nil map
// and event list (an empty non-nil one is not zero).
func (m ConstraintModes) Zero() bool {
	return !m.AllSet && !m.AllDeferred && m.ByConstraint == nil && m.Events == nil
}

func (m ConstraintModes) Deferred(oid ids.OID, deferrable, initiallyDeferred bool) bool {
	if !deferrable {
		return false
	}
	if value, ok := m.ByConstraint[oid]; ok {
		return value
	}
	if m.AllSet {
		return m.AllDeferred
	}
	return initiallyDeferred
}

func ExclusionDefinition(parent *catalog.Relation, index *catalog.Relation) *catalog.ExclusionConstraint {
	for i := range parent.Constraints.Exclusion {
		c := &parent.Constraints.Exclusion[i]
		if c.Index == index.Address() && c.OID == index.SQLIndex.Constraint.OID {
			return c
		}
	}
	return nil
}

func UniqueDefinition(parent, idx *catalog.Relation) *catalog.UniqueConstraint {
	if idx.SQLIndex == nil || idx.SQLIndex.Constraint == nil {
		return nil
	}
	for i := range parent.Constraints.Unique {
		c := &parent.Constraints.Unique[i]
		if c.Index == idx.Address() && c.OID == idx.SQLIndex.Constraint.OID {
			return c
		}
	}
	return nil
}

func IndexConstraintTiming(parent, idx *catalog.Relation) (ids.OID, bool, bool, bool) {
	if idx.SQLIndex.IsExclusion() {
		if c := ExclusionDefinition(parent, idx); c != nil {
			return c.OID, c.Deferrable, c.InitiallyDeferred, true
		}
	} else if c := UniqueDefinition(parent, idx); c != nil {
		return c.OID, c.Deferrable, c.InitiallyDeferred, true
	}
	return 0, false, false, false
}

// ConstraintProjectionRequired identifies the constraints whose writes reserve
// their physical row/version identity during dirty-index admission.
func ConstraintProjectionRequired(parent *catalog.Relation) bool {
	return len(parent.Constraints.Exclusion) != 0 || slices.ContainsFunc(parent.Constraints.Unique, func(c catalog.UniqueConstraint) bool { return c.Deferrable })
}

func BindExclusionOrdering(view catalog.Read, parent *catalog.Relation, d *catalog.IndexDefinition) (Ordering, error) {
	if d.Method == catalog.GistMethod || d.Method == catalog.SpgistMethod {
		return bindLogicalKeys(view, parent, d)
	}
	if d.Method == catalog.BtreeMethod {
		return BindIndexOrdering(view, parent, d)
	}
	binding, err := BindHashIndex(view, parent, d)
	if err != nil {
		return Ordering{}, err
	}
	o := Ordering{Slots: []int{binding.Slot}, Comparisons: []sqltype.IndexComparison{binding.Provider.Comparison()}, Keys: d.Keys}
	if binding.Expression != nil {
		o.Derived = []expr.Expr{binding.Expression}
	}
	return o, nil
}

func ExclusionKeysMatch(ctx context.Context, d *catalog.IndexDefinition, left, right []sqltype.Value) (bool, error) {
	for i, operator := range d.Exclusion {
		match, err := operator.Matches(ctx, left[i], right[i])
		if err != nil || !match {
			return false, err
		}
	}
	return true, nil
}

func cloneExclusionProjection(old *ExclusionProjection) *ExclusionProjection {
	p := &ExclusionProjection{Entries: make(map[ids.ObjectAddress][]ExclusionEntry), Ended: make(map[ids.ObjectAddress]map[ids.RowID]ids.VersionID)}
	if old != nil {
		p.Entries = maps.Clone(old.Entries)
		p.Ended = maps.Clone(old.Ended)
	}
	return p
}

// Apply replaces row's entry on e.Index by its new one (none when
// it is not a member) and ends the rows e excludes. Entries of other indexes
// stay, so a later index insertion keeps the earlier projections.
func (e ExclusionEdit) Apply(p *ExclusionProjection, parent ids.ObjectAddress, row storage.RowVersion) {
	entries := p.Entries[e.Index]
	next := make([]ExclusionEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.Row.Row != row.Row {
			next = append(next, entry)
		}
	}
	if e.Member {
		next = append(next, ExclusionEntry{Row: row, Keys: e.Keys, Admitted: e.Admitted})
	}
	p.Entries[e.Index] = next
	if len(e.Excluded) != 0 {
		ends := maps.Clone(p.Ended[parent])
		if ends == nil {
			ends = make(map[ids.RowID]ids.VersionID)
		}
		for _, old := range e.Excluded {
			ends[old.Row] = old.Version
			if old.Base != 0 {
				ends[old.Row] = old.Base
			}
		}
		p.Ended[parent] = ends
	}
}

// DiscardVersion drops every entry of version.
func (p *ExclusionProjection) DiscardVersion(version ids.VersionID) {
	for idx, entries := range p.Entries {
		if !slices.ContainsFunc(entries, func(e ExclusionEntry) bool { return e.Row.Version == version }) {
			continue
		}
		next := make([]ExclusionEntry, 0, len(entries)-1)
		for _, entry := range entries {
			if entry.Row.Version != version {
				next = append(next, entry)
			}
		}
		p.Entries[idx] = next
	}
}

// EndRow ends a globally allocated row identity on every projected index.
func (p *ExclusionProjection) EndRow(address ids.ObjectAddress, row ids.RowID, version ids.VersionID) {
	ends := maps.Clone(p.Ended[address])
	if ends == nil {
		ends = make(map[ids.RowID]ids.VersionID)
	}
	ends[row] = version
	p.Ended[address] = ends
	for idx, entries := range p.Entries {
		if !slices.ContainsFunc(entries, func(e ExclusionEntry) bool { return e.Row.Row == row }) {
			continue
		}
		next := make([]ExclusionEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.Row.Row != row {
				next = append(next, entry)
			}
		}
		p.Entries[idx] = next
	}
}

// Admit marks version's entries on idx admitted.
func (p *ExclusionProjection) Admit(idx ids.ObjectAddress, version ids.VersionID) {
	entries := slices.Clone(p.Entries[idx])
	for at := range entries {
		if entries[at].Row.Version == version {
			entries[at].Admitted = true
		}
	}
	p.Entries[idx] = entries
}
