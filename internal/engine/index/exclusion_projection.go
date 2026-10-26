package index

import (
	"context"
	"maps"
	"slices"

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

type ExclusionEvent struct {
	Parent, Index ids.ObjectRef
	Constraint    ids.OID
	Row           ids.RowID
	Version       ids.VersionID
}

type ExclusionModes struct {
	AllSet, AllDeferred bool
	ByConstraint        map[ids.OID]bool
	Events              []ExclusionEvent
}

// Zero reports whether m is exactly the zero value: no flags, and a nil map
// and event list (an empty non-nil one is not zero).
func (m ExclusionModes) Zero() bool {
	return !m.AllSet && !m.AllDeferred && m.ByConstraint == nil && m.Events == nil
}

func (m ExclusionModes) Deferred(c *catalog.ExclusionConstraint) bool {
	if !c.Deferrable {
		return false
	}
	if value, ok := m.ByConstraint[c.OID]; ok {
		return value
	}
	if m.AllSet {
		return m.AllDeferred
	}
	return c.InitiallyDeferred
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

// EndRow ends row at version on parent and drops its entries from
// every exclusion index of constraints.
func (p *ExclusionProjection) EndRow(parent ids.ObjectAddress, constraints []catalog.ExclusionConstraint, row ids.RowID, version ids.VersionID) {
	ends := maps.Clone(p.Ended[parent])
	if ends == nil {
		ends = make(map[ids.RowID]ids.VersionID)
	}
	ends[row] = version
	p.Ended[parent] = ends
	for _, c := range constraints {
		entries := p.Entries[c.Index]
		next := make([]ExclusionEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.Row.Row != row {
				next = append(next, entry)
			}
		}
		p.Entries[c.Index] = next
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
