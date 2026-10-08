package engine

import (
	"context"
	"fmt"
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
}

type catalogDependencyObservation struct {
	base    *catalogState
	targets []ObjectAddress
}

type catalogAuthority struct {
	session              *Session
	authorization, roles uint64
	ref                  ObjectRef
	descriptor           Descriptor
}

// Statement authority expires at successful Finish, never at outer Commit.
// Caller holds Engine.mu and the authority session's mutex.
func (e *Engine) catalogAuthorityPreflightLocked(a *catalogAuthority) error {
	if a == nil {
		return nil
	}
	if a.session.authorization.revision != a.authorization || e.roles.revision != a.roles || e.state.descriptors[a.ref.Address] != a.descriptor {
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

func projectVersions(ctx context.Context, rows []rowVersion, old, next *relationDescriptor) ([]rowVersion, error) {
	if len(rows) == 0 {
		return rows, nil
	}
	projected := make([]rowVersion, len(rows))
	slots := make([]int, len(next.attributes))
	source := 0
	for j, attribute := range next.attributes {
		for source < len(old.attributes) && old.attributes[source] != attribute {
			source++
		}
		if source == len(old.attributes) {
			return nil, fmt.Errorf("stuntgres: invalid column projection")
		}
		slots[j] = source
		source++
	}
	for i, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(row.values) != len(old.columns) {
			return nil, fmt.Errorf("stuntgres: invalid original row width")
		}
		values := make([]sqltype.Value, len(slots))
		for j, slot := range slots {
			values[j] = row.values[slot]
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
