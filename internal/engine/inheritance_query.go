package engine

import (
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Graph validation establishes name/origin identity; live row layout still has
// to be translated, because earlier parents may precede this named parent.
func inheritanceQueryLayout(parent, child *catalog.Relation) ([]int, error) {
	identity := len(parent.Columns) == len(child.Columns)
	for i, column := range parent.Columns {
		slot, ok := child.Index[column.Name]
		if !ok || !samePartitionColumnUsage(child.Columns[slot].Usage, column.Usage) {
			return nil, ids.ErrCatalogReplaced
		}
		identity = identity && slot == i
	}
	if identity {
		return nil, nil
	}
	layout := make([]int, len(parent.Columns))
	for i, column := range parent.Columns {
		layout[i] = child.Index[column.Name]
	}
	return layout, nil
}

func captureInheritanceQueryPrefix(catalogView catalog.Read, view *visibility.View, leaf queryLeaf, token ids.SessionToken) (*querySource, error) {
	if leaf.Only || !inheritanceHasChildren(catalogView, leaf.Ref.Address) {
		return captureQueryPrefix(view, leaf.Ref)
	}
	pin := view.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	source := &querySource{Ref: leaf.Ref, View: pin, Inheritance: make([]inheritanceQuerySegment, 0)}
	err := inheritanceQueryClosure(catalogView, leaf.Relation, token, func(physical *catalog.Relation) error {
		child := leaf
		child.Ref.Address, child.Relation = physical.Address(), physical
		child.Storage = view.Data().GenerationAt(physical.Address())
		layout, err := inheritanceQueryLayout(leaf.Relation, physical)
		if err != nil {
			return err
		}
		prefix, err := captureQueryPrefix(view, child.Ref)
		if err != nil {
			return err
		}
		segment := inheritanceQuerySegment{Leaf: child, Prefix: prefix, Layout: layout}
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
