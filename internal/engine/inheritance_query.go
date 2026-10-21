package engine

import (
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

func captureInheritanceQueryPrefix(catalogView catalog.Read, view *visibility.View, leaf exec.Leaf, token ids.SessionToken) (*exec.Source, error) {
	if leaf.Only || !catalog.InheritanceHasChildren(catalogView, leaf.Ref.Address) {
		return captureQueryPrefix(view, leaf.Ref)
	}
	pin := view.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	source := &exec.Source{Ref: leaf.Ref, View: pin, Inheritance: make([]exec.InheritanceQuerySegment, 0)}
	err := inheritanceQueryClosure(catalogView, leaf.Relation, token, func(physical *catalog.Relation) error {
		child := leaf
		child.Ref.Address, child.Relation = physical.Address(), physical
		child.Storage = view.Data().GenerationAt(physical.Address())
		layout, err := catalog.InheritanceQueryLayout(leaf.Relation, physical)
		if err != nil {
			return err
		}
		prefix, err := captureQueryPrefix(view, child.Ref)
		if err != nil {
			return err
		}
		segment := exec.InheritanceQuerySegment{Leaf: child, Prefix: prefix, Layout: layout}
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
	// Foreign TEMP branches may leave just the original physical relation.
	if len(source.Inheritance) == 1 {
		prefix := source.Inheritance[0].Prefix
		source.Inheritance = nil
		source.Close()
		return prefix, nil
	}
	source.InheritanceEnds = make([]int, len(source.Inheritance))
	total := 0
	for i, segment := range source.Inheritance {
		total += segment.Prefix.Count()
		source.InheritanceEnds[i] = total
	}
	return source, nil
}
