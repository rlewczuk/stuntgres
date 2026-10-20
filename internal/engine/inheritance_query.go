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
	if leaf.only || !inheritanceHasChildren(catalogView, leaf.ref.Address) {
		return captureQueryPrefix(view, leaf.ref)
	}
	pin := view.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	source := &querySource{ref: leaf.ref, view: pin, inheritance: make([]inheritanceQuerySegment, 0)}
	err := inheritanceQueryClosure(catalogView, leaf.relation, token, func(physical *catalog.Relation) error {
		child := leaf
		child.ref.Address, child.relation = physical.Address(), physical
		child.storage = view.Data().GenerationAt(physical.Address())
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
