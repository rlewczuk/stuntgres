package engine

import (
	"errors"
	"stuntgres/internal/sqltype"
)

// Partition reads reuse the mapped physical-prefix source used by inheritance.
// Row identities and versions always retain their original physical storage.
func capturePartitionQueryPrefix(catalog CatalogRead, view *VisibilityView, ref ObjectRef, parent *relationDescriptor) (*querySource, error) {
	// The visibility capture retains the statement's admitted topology, not a
	// live descriptor that may have gained children while a leaf lock waited.
	if captured, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor); ok {
		parent = captured
	}
	pin := view.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	source := &querySource{ref: ref, view: pin, inheritance: make([]inheritanceQuerySegment, 0)}
	err := partitionPhysicalDescendants(catalog, parent, func(leaf *relationDescriptor) error {
		childRef := ref.Domain().Ref(ref.Epoch, leaf.address)
		layout, err := inheritanceQueryLayout(parent, leaf)
		if err != nil {
			return err
		}
		child, err := captureQueryPrefix(view, childRef)
		if err != nil {
			return err
		}
		segment := inheritanceQuerySegment{leaf: queryLeaf{ref: childRef, relation: leaf}, prefix: child, layout: layout}
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
	source.inheritanceEnds = make([]int, len(source.inheritance))
	total := 0
	for i, segment := range source.inheritance {
		total += segment.prefix.count()
		source.inheritanceEnds[i] = total
	}
	return source, nil
}
