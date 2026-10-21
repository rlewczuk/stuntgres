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
	row      storage.RowVersion
	keys     []sqltype.Value
	admitted bool
}

type exclusionProjection struct {
	entries map[ids.ObjectAddress][]exclusionEntry
	ended   map[ids.ObjectAddress]map[ids.RowID]ids.VersionID
}

type exclusionEvent struct {
	parent, index ids.ObjectRef
	constraint    ids.OID
	row           ids.RowID
	version       ids.VersionID
}

type exclusionModes struct {
	allSet, allDeferred bool
	byConstraint        map[ids.OID]bool
	events              []exclusionEvent
}

// zero reports whether m is exactly the zero value: no flags, and a nil map
// and event list (an empty non-nil one is not zero).
func (m exclusionModes) zero() bool {
	return !m.allSet && !m.allDeferred && m.byConstraint == nil && m.events == nil
}

func (m exclusionModes) deferred(c *catalog.ExclusionConstraint) bool {
	if !c.Deferrable {
		return false
	}
	if value, ok := m.byConstraint[c.OID]; ok {
		return value
	}
	if m.allSet {
		return m.allDeferred
	}
	return c.InitiallyDeferred
}

func exclusionDefinition(parent *catalog.Relation, index *catalog.Relation) *catalog.ExclusionConstraint {
	for i := range parent.Constraints.Exclusion {
		c := &parent.Constraints.Exclusion[i]
		if c.Index == index.Address() && c.OID == index.SQLIndex.Constraint.OID {
			return c
		}
	}
	return nil
}

func bindExclusionOrdering(parent *catalog.Relation, d *catalog.IndexDefinition) (indexOrdering, error) {
	if d.Method == catalog.GistMethod {
		return bindLogicalExclusionKeys(parent, d)
	}
	if d.Method == catalog.BtreeMethod {
		return bindIndexOrdering(parent, d)
	}
	// The supported HASH equality has the same concrete scalar comparison as
	// its default Btree class. This is a comparator binding, not an index or a
	// UNIQUE owner, and HASH storage continues to use its actual provider.
	copy := *d
	copy.Method = catalog.BtreeMethod
	copy.Keys = slices.Clone(d.Keys)
	for i, key := range copy.Keys {
		u, err := indexKeyUsage(parent, key)
		if err != nil {
			return indexOrdering{}, err
		}
		class, ok := sqltype.DefaultIndexClass(u.Type)
		if !ok {
			return indexOrdering{}, fmt.Errorf("stuntgres: missing exclusion equality comparator")
		}
		copy.Keys[i].Class = class
	}
	return bindIndexOrdering(parent, &copy)
}

func exclusionKeysMatch(ctx context.Context, d *catalog.IndexDefinition, left, right []sqltype.Value) (bool, error) {
	for i, operator := range d.Exclusion {
		match, err := operator.Matches(ctx, left[i], right[i])
		if err != nil || !match {
			return false, err
		}
	}
	return true, nil
}

func cloneExclusionProjection(old *exclusionProjection) *exclusionProjection {
	p := &exclusionProjection{entries: make(map[ids.ObjectAddress][]exclusionEntry), ended: make(map[ids.ObjectAddress]map[ids.RowID]ids.VersionID)}
	if old != nil {
		p.entries = maps.Clone(old.entries)
		p.ended = maps.Clone(old.ended)
	}
	return p
}
