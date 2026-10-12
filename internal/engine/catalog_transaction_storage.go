package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/sqltype"
)

type catalogStorageChange struct {
	original    *relationStorage
	root        []rowVersion
	ends        *deletionRoot
	generation  uint64
	replacement *relationStorage
	relation    *relationDescriptor
	snapshot    []rowVersion
	hasSnapshot bool
	rewritten   bool
	truncated   bool
	frontier    CommitSeq
	fills       map[int32]sqltype.Value
}

type catalogDependencyObservation struct {
	base    *catalogState
	targets []ObjectAddress
}

type catalogAuthority struct {
	session                                *Session
	authorization, roles                   uint64
	ref                                    ObjectRef
	descriptor                             Descriptor
	databaseAuthority, databasePredecessor Descriptor
	// Immutable consulted providers captured by the statement's catalog stage.
	observations []Descriptor
}

// Caller holds Engine.mu; never derive entry authority from a later live view.
func (v AuthorizationView) catalogAuthorityLocked(ref ObjectRef, descriptor Descriptor) *catalogAuthority {
	return &catalogAuthority{session: v.session, authorization: v.revision, roles: v.roles.state.revision, ref: ref, descriptor: descriptor, databaseAuthority: v.catalog.state.descriptors[currentDatabaseAddress], databasePredecessor: v.databasePredecessor}
}

// Statement authority expires at successful Finish, never at outer Commit.
// Caller holds Engine.mu and the authority session's mutex. Catalog changes
// retain the owning transaction's validated installed predecessors.
func (e *Engine) catalogAuthorityPreflightLocked(a *catalogAuthority, c *catalogChanges) error {
	if a == nil {
		return nil
	}
	if a.session.authorization.revision != a.authorization || e.sessionRolesLocked(a.session).revision != a.roles || e.state.descriptors[a.ref.Address] != a.descriptor {
		return CatalogConflict{}
	}
	if a.databasePredecessor != nil {
		if e.state.descriptors[currentDatabaseAddress] != a.databasePredecessor {
			return CatalogConflict{}
		}
		if c != nil {
			if original, ok := c.originals[currentDatabaseAddress]; ok && original != a.databasePredecessor {
				return CatalogConflict{}
			}
			c.originals[currentDatabaseAddress] = a.databasePredecessor
		}
	}
	for _, observed := range a.observations {
		address := observed.Address()
		installed := e.state.descriptors[address]
		if installed == observed {
			continue
		}
		if c != nil {
			original, owned := c.originals[address]
			// Only an exact owner-private provider may use its predecessor.
			// An installed observation must never be rescued by the overlay.
			if owned && original != observed && installed == original && c.descriptors[address] == observed {
				continue
			}
		}
		return CatalogConflict{}
	}
	return nil
}

func sameVersionRoot(a, b []rowVersion) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// Callers hold Engine.mu; private storage is never installed before Finish/Commit.
func (e *Engine) privateStorageLocked(c *catalogChanges, a ObjectAddress) *relationStorage {
	if c != nil {
		if shape, ok := c.storage[a]; ok {
			return shape.replacement
		}
	}
	return e.storage[a]
}

func (e *Engine) authorizationStorageLocked(auth AuthorizationView, a ObjectAddress) *relationStorage {
	if auth.session != nil && auth.session.transactions != nil {
		return e.privateStorageLocked(auth.session.transactions.catalog, a)
	}
	return e.storage[a]
}

type columnProjection struct {
	width int
	slots []int
	fills []sqltype.Value
}

func newColumnProjection(old, next *relationDescriptor, fills map[int32]sqltype.Value) (columnProjection, error) {
	p := columnProjection{width: len(old.columns), slots: make([]int, len(next.attributes))}
	source := 0
	for j, attribute := range next.attributes {
		for source < len(old.attributes) && old.attributes[source] < attribute {
			source++
		}
		if source < len(old.attributes) && old.attributes[source] == attribute {
			p.slots[j] = source
			source++
		} else {
			value, ok := fills[attribute]
			if !ok || value.Type() != next.columns[j].Usage.Type {
				return columnProjection{}, fmt.Errorf("stuntgres: invalid added column projection")
			}
			p.slots[j] = -1
			if p.fills == nil {
				p.fills = make([]sqltype.Value, len(next.attributes))
			}
			p.fills[j] = value
		}
	}
	return p, nil
}

func projectVersions(ctx context.Context, rows []rowVersion, old, next *relationDescriptor, fills map[int32]sqltype.Value) ([]rowVersion, error) {
	if len(rows) == 0 {
		return rows, nil
	}
	p, err := newColumnProjection(old, next, fills)
	if err != nil {
		return nil, err
	}
	return p.versions(ctx, rows)
}

func (p columnProjection) versions(ctx context.Context, rows []rowVersion) ([]rowVersion, error) {
	if len(rows) == 0 {
		return rows, nil
	}
	projected := make([]rowVersion, len(rows))
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(row.values) != p.width {
			return nil, fmt.Errorf("stuntgres: invalid original row width")
		}
		values := make([]sqltype.Value, len(p.slots))
		for j, slot := range p.slots {
			if slot >= 0 {
				values[j] = row.values[slot]
			} else {
				values[j] = p.fills[j]
			}
		}
		row.values = values
		projected[i] = row
	}
	return projected, nil
}

func projectDeletions(root *privateDeletionRoot, store *relationStorage) *privateDeletionRoot {
	if root == nil {
		return nil
	}
	next := &privateDeletionRoot{rows: make(map[RowID]deleteCandidate, len(root.rows))}
	for id, candidate := range root.rows {
		candidate.storage = store
		next.rows[id] = candidate
	}
	return next
}

// Savepoint marks may predate first snapshot establishment. Clone only the
// immutable shape map when attaching the matching projection to such a mark.
func projectPrivateSnapshotRoots(ctx context.Context, catalog *catalogChanges, roots map[ObjectAddress]transactionRoot) (*catalogChanges, error) {
	var next *catalogChanges
	for address, shape := range catalog.storage {
		root, exists := roots[address]
		if !exists || shape.rewritten || shape.hasSnapshot || slices.Equal(root.relation.attributes, shape.relation.attributes) {
			continue
		}
		versions := shape.replacement.versions
		if root.storage != shape.original || root.ends != shape.ends || !sameVersionRoot(root.versions, shape.root) {
			var err error
			versions, err = projectVersions(ctx, root.versions, root.relation, shape.relation, shape.fills)
			if err != nil {
				return nil, err
			}
		}
		if next == nil {
			copy := *catalog
			next = &copy
			next.storage = make(map[ObjectAddress]catalogStorageChange, len(catalog.storage))
			for a, entry := range catalog.storage {
				next.storage[a] = entry
			}
		}
		shape.snapshot, shape.hasSnapshot = versions, true
		next.storage[address] = shape
	}
	if next == nil {
		return catalog, nil
	}
	return next, nil
}
