package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

type catalogAuthority struct {
	session                                *Session
	authorization, roles                   uint64
	ref                                    ids.ObjectRef
	descriptor                             catalog.Descriptor
	databaseAuthority, databasePredecessor catalog.Descriptor
	// Immutable consulted providers captured by the statement's catalog stage.
	observations []catalog.Descriptor
}

// Caller holds Engine.mu; never derive entry authority from a later live view.
func (v AuthorizationView) catalogAuthorityLocked(ref ids.ObjectRef, descriptor catalog.Descriptor) *catalogAuthority {
	return &catalogAuthority{session: v.session, authorization: v.Revision, roles: v.RoleRecord.Revision, ref: ref, descriptor: descriptor, databaseAuthority: v.Catalog.Get(catalog.CurrentDatabaseAddress), databasePredecessor: v.DatabasePredecessor}
}

// Statement authority expires at successful Finish, never at outer Commit.
// Caller holds Engine.mu and the authority session's mutex. Catalog changes
// retain the owning transaction's validated installed predecessors.
func (e *Engine) catalogAuthorityPreflightLocked(a *catalogAuthority, c *catalogChanges) error {
	if a == nil {
		return nil
	}
	if a.session.Authorization.revision != a.authorization || e.sessionRolesLocked(a.session).Revision != a.roles || e.catalog.state.Get(a.ref.Address) != a.descriptor {
		return catalog.Conflict{}
	}
	if a.databasePredecessor != nil {
		if e.catalog.state.Get(catalog.CurrentDatabaseAddress) != a.databasePredecessor {
			return catalog.Conflict{}
		}
		if c != nil {
			if original, ok := c.Originals[catalog.CurrentDatabaseAddress]; ok && original != a.databasePredecessor {
				return catalog.Conflict{}
			}
			c.Originals[catalog.CurrentDatabaseAddress] = a.databasePredecessor
		}
	}
	for _, observed := range a.observations {
		address := observed.Address()
		installed := e.catalog.state.Get(address)
		if installed == observed {
			continue
		}
		if c != nil {
			original, owned := c.Originals[address]
			// Only an exact owner-private provider may use its predecessor.
			// An installed observation must never be rescued by the overlay.
			if owned && original != observed && installed == original && c.Descriptors[address] == observed {
				continue
			}
		}
		return catalog.Conflict{}
	}
	return nil
}

func sameVersionRoot(a, b []storage.RowVersion) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// Callers hold Engine.mu; private storage is never installed before Finish/Commit.
func (e *Engine) privateStorageLocked(c *catalogChanges, a ids.ObjectAddress) *storage.Relation {
	if c != nil {
		if shape, ok := c.Storage[a]; ok {
			return shape.Replacement
		}
	}
	return e.storage.Relations[a]
}

func (e *Engine) authorizationStorageLocked(auth AuthorizationView, a ids.ObjectAddress) *storage.Relation {
	if auth.session != nil && auth.session.Transactions != nil {
		return e.privateStorageLocked(auth.session.Transactions.catalog, a)
	}
	return e.storage.Relations[a]
}

type columnProjection struct {
	width int
	slots []int
	fills []sqltype.Value
}

func newColumnProjection(old, next *catalog.Relation, fills map[int32]sqltype.Value) (columnProjection, error) {
	p := columnProjection{width: len(old.Columns), slots: make([]int, len(next.Attributes))}
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
				return columnProjection{}, fmt.Errorf("stuntgres: invalid added column projection")
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

func projectVersions(ctx context.Context, rows []storage.RowVersion, old, next *catalog.Relation, fills map[int32]sqltype.Value) ([]storage.RowVersion, error) {
	if len(rows) == 0 {
		return rows, nil
	}
	p, err := newColumnProjection(old, next, fills)
	if err != nil {
		return nil, err
	}
	return p.versions(ctx, rows)
}

func (p columnProjection) versions(ctx context.Context, rows []storage.RowVersion) ([]storage.RowVersion, error) {
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

func projectDeletions(root *visibility.PrivateDeletes, store *storage.Relation) *visibility.PrivateDeletes {
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
func projectPrivateSnapshotRoots(ctx context.Context, catalog *catalogChanges, roots map[ids.ObjectAddress]visibility.Root) (*catalogChanges, error) {
	var next *catalogChanges
	for address, shape := range catalog.Storage {
		root, exists := roots[address]
		if !exists || shape.Rewritten || shape.HasSnapshot || slices.Equal(root.Relation.Attributes, shape.Relation.Attributes) {
			continue
		}
		versions := shape.Replacement.Versions
		if root.Storage != shape.Original || root.Ends != shape.Ends || !sameVersionRoot(root.Versions, shape.Root) {
			var err error
			versions, err = projectVersions(ctx, root.Versions, root.Relation, shape.Relation, shape.Fills)
			if err != nil {
				return nil, err
			}
		}
		if next == nil {
			copy := *catalog
			next = &copy
			next.Storage = make(map[ids.ObjectAddress]catalogStorageChange, len(catalog.Storage))
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
