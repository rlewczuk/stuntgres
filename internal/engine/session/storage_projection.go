package session

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// ColumnProjection maps row values from an old column shape to a new one,
// filling added attributes.
type ColumnProjection struct {
	width int
	slots []int
	fills []sqltype.Value
}

// NewColumnProjection builds the projection from old to next; every added
// attribute needs a fill of the new column's type.
func NewColumnProjection(old, next *catalog.Relation, fills map[int32]sqltype.Value) (ColumnProjection, error) {
	p := ColumnProjection{width: len(old.Columns), slots: make([]int, len(next.Attributes))}
	source := 0
	for j, attribute := range next.Attributes {
		for source < len(old.Attributes) && old.Attributes[source] < attribute {
			source++
		}
		if source < len(old.Attributes) && old.Attributes[source] == attribute {
			p.slots[j] = source
			source++
		} else {
			value, ok := fills[attribute]
			if !ok || value.Type() != next.Columns[j].Usage.Type {
				return ColumnProjection{}, fmt.Errorf("stuntgres: invalid added column projection")
			}
			p.slots[j] = -1
			if p.fills == nil {
				p.fills = make([]sqltype.Value, len(next.Attributes))
			}
			p.fills[j] = value
		}
	}
	return p, nil
}

// ProjectVersions projects rows from old to next.
func ProjectVersions(ctx context.Context, rows []storage.RowVersion, old, next *catalog.Relation, fills map[int32]sqltype.Value) ([]storage.RowVersion, error) {
	if len(rows) == 0 {
		return rows, nil
	}
	p, err := NewColumnProjection(old, next, fills)
	if err != nil {
		return nil, err
	}
	return p.Versions(ctx, rows)
}

// Versions returns projected copies of rows; it checks ctx per row.
func (p ColumnProjection) Versions(ctx context.Context, rows []storage.RowVersion) ([]storage.RowVersion, error) {
	if len(rows) == 0 {
		return rows, nil
	}
	projected := make([]storage.RowVersion, len(rows))
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(row.Values) != p.width {
			return nil, fmt.Errorf("stuntgres: invalid original row width")
		}
		values := make([]sqltype.Value, len(p.slots))
		for j, slot := range p.slots {
			if slot >= 0 {
				values[j] = row.Values[slot]
			} else {
				values[j] = p.fills[j]
			}
		}
		row.Values = values
		row.IndexOutputs = nil
		projected[i] = row
	}
	return projected, nil
}

// ProjectDeletions points a private delete root at store.
func ProjectDeletions(root *visibility.PrivateDeletes, store *storage.Relation) *visibility.PrivateDeletes {
	if root == nil {
		return nil
	}
	next := &visibility.PrivateDeletes{Rows: make(map[ids.RowID]visibility.DeleteCandidate, len(root.Rows))}
	for id, candidate := range root.Rows {
		candidate.Storage = store
		next.Rows[id] = candidate
	}
	return next
}

// Savepoint marks may predate first snapshot establishment. Clone only the
// immutable shape map when attaching the matching projection to such a mark.
func ProjectPrivateSnapshotRoots(ctx context.Context, catalog *Journal, roots map[ids.ObjectAddress]visibility.Root) (*Journal, error) {
	var next *Journal
	for address, shape := range catalog.Storage {
		root, exists := roots[address]
		if !exists || shape.Rewritten || shape.HasSnapshot || slices.Equal(root.Relation.Attributes, shape.Relation.Attributes) {
			continue
		}
		versions := shape.Replacement.Versions
		if root.Storage != shape.Original || root.Ends != shape.Ends || !storage.SameVersionRoot(root.Versions, shape.Root) {
			var err error
			versions, err = ProjectVersions(ctx, root.Versions, root.Relation, shape.Relation, shape.Fills)
			if err != nil {
				return nil, err
			}
		}
		if next == nil {
			copy := *catalog
			next = &copy
			next.Storage = make(map[ids.ObjectAddress]StorageChange, len(catalog.Storage))
			for a, entry := range catalog.Storage {
				next.Storage[a] = entry
			}
		}
		shape.Snapshot, shape.HasSnapshot = versions, true
		next.Storage[address] = shape
	}
	if next == nil {
		return catalog, nil
	}
	return next, nil
}

// ProjectColumnShapeRoots projects a column shape change through committed
// storage, the statement's private writes and deletes and, for a stable
// snapshot, the retained snapshot root.
func ProjectColumnShapeRoots(ctx context.Context, stable bool, old, next *catalog.Relation, store *storage.Relation, shape StorageChange, snapshot visibility.Root, snapshotExists bool, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) (StorageChange, []storage.RowVersion, *visibility.PrivateDeletes, error) {
	if slices.Equal(old.Attributes, next.Attributes) {
		shape.Relation = next
		return shape, writes, deletes, nil
	}
	if store.Generation == ^uint64(0) {
		return shape, nil, nil, fmt.Errorf("stuntgres: storage generation exhausted")
	}
	shape.Fills = maps.Clone(shape.Fills)
	for attribute := range shape.Fills {
		if !next.HasAttribute(attribute) {
			delete(shape.Fills, attribute)
		}
	}
	projection, err := NewColumnProjection(old, next, shape.Fills)
	if err != nil {
		return shape, nil, nil, err
	}
	versions, err := projection.Versions(ctx, store.Versions)
	if err != nil {
		return shape, nil, nil, err
	}
	shape.Replacement = &storage.Relation{Versions: versions, Ends: store.Ends, Generation: store.Generation + 1, Maintenance: store.Maintenance}
	shape.Truncated = false
	writes, err = projection.Versions(ctx, writes)
	if err != nil {
		return shape, nil, nil, err
	}
	deletes = ProjectDeletions(deletes, shape.Replacement)
	if stable && snapshotExists && !shape.Rewritten {
		if slices.Equal(snapshot.Relation.Attributes, old.Attributes) {
			shape.Snapshot, err = projection.Versions(ctx, snapshot.Versions)
		} else {
			shape.Snapshot, err = ProjectVersions(ctx, snapshot.Versions, snapshot.Relation, next, shape.Fills)
		}
		if err != nil {
			return shape, nil, nil, err
		}
		shape.HasSnapshot = true
	}
	shape.Relation = next
	return shape, writes, deletes, nil
}
