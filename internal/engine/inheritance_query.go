package engine

import (
	"context"
	"errors"
	"sort"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type inheritanceQuerySegment struct {
	leaf    queryLeaf
	prefix  *querySource
	layout  []int // Nil is an identity layout: borrow the physical tuple.
	scratch []sqltype.Value
}

// Graph validation establishes name/origin identity; live row layout still has
// to be translated, because earlier parents may precede this named parent.
func inheritanceQueryLayout(parent, child *relationDescriptor) ([]int, error) {
	identity := len(parent.columns) == len(child.columns)
	for i, column := range parent.columns {
		slot, ok := child.index[column.Name]
		if !ok || !samePartitionColumnUsage(child.columns[slot].Usage, column.Usage) {
			return nil, ErrCatalogReplaced
		}
		identity = identity && slot == i
	}
	if identity {
		return nil, nil
	}
	layout := make([]int, len(parent.columns))
	for i, column := range parent.columns {
		layout[i] = child.index[column.Name]
	}
	return layout, nil
}

func captureInheritanceQueryPrefix(catalog CatalogRead, view *VisibilityView, leaf queryLeaf, token ids.SessionToken) (*querySource, error) {
	if leaf.only || !inheritanceHasChildren(catalog, leaf.ref.Address) {
		return captureQueryPrefix(view, leaf.ref)
	}
	pin := view.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	source := &querySource{ref: leaf.ref, view: pin, inheritance: make([]inheritanceQuerySegment, 0)}
	err := inheritanceQueryClosure(catalog, leaf.relation, token, func(physical *relationDescriptor) error {
		child := leaf
		child.ref.Address, child.relation = physical.address, physical
		child.storage = view.read().generationAt(physical.address)
		layout, err := inheritanceQueryLayout(leaf.relation, physical)
		if err != nil {
			return err
		}
		prefix, err := captureQueryPrefix(view, child.ref)
		if err != nil {
			return err
		}
		segment := inheritanceQuerySegment{leaf: child, prefix: prefix, layout: layout}
		if layout != nil {
			segment.scratch = make([]sqltype.Value, len(layout))
		}
		source.inheritance = append(source.inheritance, segment)
		return nil
	})
	if err != nil {
		source.close()
		return nil, err
	}
	// Foreign TEMP branches may leave just the original physical relation.
	if len(source.inheritance) == 1 {
		prefix := source.inheritance[0].prefix
		source.inheritance = nil
		source.close()
		return prefix, nil
	}
	source.inheritanceEnds = make([]int, len(source.inheritance))
	total := 0
	for i, segment := range source.inheritance {
		total += segment.prefix.count()
		source.inheritanceEnds[i] = total
	}
	return source, nil
}

func (s *querySource) selectInheritancePrefixes(ctx context.Context) error {
	total := 0
	for i, segment := range s.inheritance {
		data := segment.prefix.view.read()
		if _, err := selectQuerySource(ctx, segment.prefix, data.frontierAt(segment.leaf.ref.Address), data.ends[segment.leaf.ref.Address], data.deleted[segment.leaf.ref.Address]); err != nil {
			return err
		}
		total += segment.prefix.count()
		s.inheritanceEnds[i] = total
	}
	return ctx.Err()
}

func (s *querySource) inheritanceSegment(index int) (*inheritanceQuerySegment, int) {
	at := sort.SearchInts(s.inheritanceEnds, index+1)
	if at > 0 {
		index -= s.inheritanceEnds[at-1]
	}
	return &s.inheritance[at], index
}

func (segment *inheritanceQuerySegment) mapValues(values []sqltype.Value) []sqltype.Value {
	if segment.layout == nil {
		return values
	}
	for i, slot := range segment.layout {
		segment.scratch[i] = values[slot]
	}
	return segment.scratch
}

// A locator preserves physical authority and raw storage hint independently of
// the named-parent layout and the currently cached candidate row version.
// Its segment is capture-owned, so refreshed values reuse mapping scratch.
// Provenance retains the original visible index through every existing adapter;
// caches retain physical versions, never a reconstructed parent rowVersion.
type queryPhysicalRow struct {
	leaf    queryLeaf
	version rowVersion
	hint    int
	segment *inheritanceQuerySegment
}

func (s *querySource) physicalRow(index int, logical queryLeaf, candidate rowVersion) queryPhysicalRow {
	prefix := s
	var segment *inheritanceQuerySegment
	if s.inheritance != nil {
		segment, index = s.inheritanceSegment(index)
		prefix = segment.prefix
		logical = segment.leaf
	}
	raw := prefix.rawIndex(index)
	hint := raw
	if raw >= len(prefix.committed) {
		hint -= len(prefix.committed)
	}
	return queryPhysicalRow{leaf: logical, version: candidate, hint: hint, segment: segment}
}

func (row queryPhysicalRow) values(version rowVersion) []sqltype.Value {
	if row.segment != nil {
		return row.segment.mapValues(version.values)
	}
	return version.values
}

func (s *querySource) physicalRef(index int) ids.ObjectRef {
	if s.inheritance != nil {
		segment, _ := s.inheritanceSegment(index)
		return segment.leaf.ref
	}
	return s.ref
}
