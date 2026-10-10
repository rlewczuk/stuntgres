package engine

import (
	"context"
	"errors"
	"sort"
	"stuntgres/internal/sqltype"
)

// A hierarchy source owns only physical retained prefixes and a compact row
// offset manifest. Rows and row identities remain in their original storage.
func capturePartitionQueryPrefix(catalog CatalogRead, view *VisibilityView, ref ObjectRef, parent *relationDescriptor) (*querySource, error) {
	pin := view.Retain()
	if pin == nil {
		return nil, errors.New("stuntgres: visibility view is released")
	}
	source := &querySource{ref: ref, view: pin, partitions: make([]*querySource, 0)}
	err := partitionPhysicalDescendants(catalog, parent, func(leaf *relationDescriptor) error {
		childRef := ObjectRef{owner: ref.owner, Epoch: ref.Epoch, Address: leaf.address}
		child, err := captureQueryPrefix(view, childRef)
		if err != nil {
			return err
		}
		source.partitions = append(source.partitions, child)
		return nil
	})
	if err != nil {
		source.close()
		return nil, err
	}
	source.partitionEnds = make([]int, len(source.partitions))
	total := 0
	for i, child := range source.partitions {
		total += child.count()
		source.partitionEnds[i] = total
	}
	return source, nil
}

func (s *querySource) selectPartitionPrefixes(ctx context.Context) error {
	total := 0
	for i, child := range s.partitions {
		data := child.view.read()
		if _, err := selectQuerySource(ctx, child, data.frontierAt(child.ref.Address), data.ends[child.ref.Address], data.deleted[child.ref.Address]); err != nil {
			return err
		}
		total += child.count()
		s.partitionEnds[i] = total
	}
	return ctx.Err()
}

func (s *querySource) partitionRow(i int) []sqltype.Value {
	at := sort.SearchInts(s.partitionEnds, i+1)
	if at > 0 {
		i -= s.partitionEnds[at-1]
	}
	return s.partitions[at].row(i)
}
func (s *querySource) partitionVersion(i int) rowVersion {
	at := sort.SearchInts(s.partitionEnds, i+1)
	if at > 0 {
		i -= s.partitionEnds[at-1]
	}
	child := s.partitions[at]
	return child.versionAt(child.rawIndex(i))
}
