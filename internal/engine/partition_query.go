package engine

import (
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Partition reads reuse the mapped physical-prefix source used by inheritance.
// Row identities and versions always retain their original physical storage.
func capturePartitionQueryPrefix(catalogView catalog.Read, view *visibility.View, ref ids.ObjectRef, parent *catalog.Relation) (*exec.Source, error) {
	// The visibility capture retains the statement's admitted topology, not a
	// live descriptor that may have gained children while a leaf lock waited.
	if captured, ok := catalogView.Relation(ref.Address); ok {
		parent = captured
	}
	pin := view.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	source := &exec.Source{Ref: ref, View: pin, Inheritance: make([]exec.InheritanceQuerySegment, 0)}
	err := partitionPhysicalDescendants(catalogView, parent, func(leaf *catalog.Relation) error {
		childRef := ref.Domain().Ref(ref.Epoch, leaf.Address())
		layout, err := inheritanceQueryLayout(parent, leaf)
		if err != nil {
			return err
		}
		child, err := captureQueryPrefix(view, childRef)
		if err != nil {
			return err
		}
		segment := exec.InheritanceQuerySegment{Leaf: exec.Leaf{Ref: childRef, Relation: leaf}, Prefix: child, Layout: layout}
		if layout != nil {
			segment.Scratch = make([]sqltype.Value, len(layout))
		}
		source.Inheritance = append(source.Inheritance, segment)
		return nil
	})
	if err != nil {
		source.Close()
		return nil, err
	}
	source.InheritanceEnds = make([]int, len(source.Inheritance))
	total := 0
	for i, segment := range source.Inheritance {
		total += segment.Prefix.Count()
		source.InheritanceEnds[i] = total
	}
	return source, nil
}
