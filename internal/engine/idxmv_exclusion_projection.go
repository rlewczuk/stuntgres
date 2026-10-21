package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// These immutable projections are visibility, not lock grants. Engine.mu owns
// their ticket membership; all providers and completion waits run unlocked.
type exclusionEntry struct {
	Row      storage.RowVersion
	Keys     []sqltype.Value
	Admitted bool
}

type exclusionProjection struct {
	Entries map[ids.ObjectAddress][]exclusionEntry
	Ended   map[ids.ObjectAddress]map[ids.RowID]ids.VersionID
}

type exclusionEvent struct {
	Parent, Index ids.ObjectRef
	Constraint    ids.OID
	Row           ids.RowID
	Version       ids.VersionID
}

type exclusionModes struct {
	AllSet, AllDeferred bool
	ByConstraint        map[ids.OID]bool
	Events              []exclusionEvent
}

// zero reports whether m is exactly the zero value: no flags, and a nil map
// and event list (an empty non-nil one is not zero).
func (m exclusionModes) Zero() bool {
	return !m.AllSet && !m.AllDeferred && m.ByConstraint == nil && m.Events == nil
}

func (m exclusionModes) Deferred(c *catalog.ExclusionConstraint) bool {
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

func BindExclusionOrdering(parent *catalog.Relation, d *catalog.IndexDefinition) (indexOrdering, error) {
	if d.Method == catalog.GistMethod {
		return bindLogicalExclusionKeys(parent, d)
	}
	if d.Method == catalog.BtreeMethod {
		return BindIndexOrdering(parent, d)
	}
	// The supported HASH equality has the same concrete scalar comparison as
	// its default Btree class. This is a comparator binding, not an index or a
	// UNIQUE owner, and HASH storage continues to use its actual provider.
	copy := *d
	copy.Method = catalog.BtreeMethod
	copy.Keys = slices.Clone(d.Keys)
	for i, key := range copy.Keys {
		u, err := KeyUsage(parent, key)
		if err != nil {
			return indexOrdering{}, err
		}
		class, ok := sqltype.DefaultIndexClass(u.Type)
		if !ok {
			return indexOrdering{}, fmt.Errorf("stuntgres: missing exclusion equality comparator")
		}
		copy.Keys[i].Class = class
	}
	return BindIndexOrdering(parent, &copy)
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

func cloneExclusionProjection(old *exclusionProjection) *exclusionProjection {
	p := &exclusionProjection{Entries: make(map[ids.ObjectAddress][]exclusionEntry), Ended: make(map[ids.ObjectAddress]map[ids.RowID]ids.VersionID)}
	if old != nil {
		p.Entries = maps.Clone(old.Entries)
		p.Ended = maps.Clone(old.Ended)
	}
	return p
}
