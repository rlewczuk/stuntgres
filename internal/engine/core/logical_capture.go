package core

import (
	"context"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/logical"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Capture owns immutable event evidence at real write staging. Successful
// effects also follow the existing statement/savepoint publication projection;
// rollback clips that projection, never the event journal.

// firstNormalObjectID bounds system catalogs, which are never decoded.
const firstNormalObjectID ids.OID = 16384

// logicalRelationCache memoizes one relation snapshot per catalog state.
type logicalRelationCache struct {
	ref      ids.ObjectRef
	catalog  *catalog.State
	relation *logical.Relation // nil when the relation is not logically logged.
}

// captureLogicalRow records one row change. old is zero for an insert and
// next is zero for a delete. Engine.Mu is held.
func (s *StatementScope) captureLogicalRow(ref ids.ObjectRef, old, next storage.RowVersion) {
	e := s.Engine()
	if !e.logicalCaptureLocked() {
		return
	}
	relation := s.logicalRelationLocked(e, ref)
	if relation == nil {
		return
	}
	change := logical.Change{LSN: e.Cluster.logical.lsn.Allocate(), Relation: relation}
	switch {
	case old.Version == 0:
		change.Kind, change.New = logical.Insert, next.Values
	case next.Version == 0:
		change.Kind = logical.Delete
		change.OldTuple, change.Old = logicalOldTuple(relation, old.Values, nil)
	default:
		change.Kind, change.New = logical.Update, next.Values
		change.OldTuple, change.Old = logicalOldTuple(relation, old.Values, next.Values)
	}
	owned := ownLogicalChange(change)
	s.appendLogicalEventLocked(logical.RowEvent, owned)
	s.logicalChanges = append(s.logicalChanges, *owned)
}

// captureLogicalTruncate records one TRUNCATE of the statement's logged
// relations with its CASCADE and RESTART IDENTITY options.
func (s *StatementScope) captureLogicalTruncate() {
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if !e.logicalCaptureLocked() {
		return
	}
	scope := s.truncateOrZero()
	change := logical.Change{Kind: logical.Truncate, Cascade: scope.ForeignCascade, RestartIdentity: scope.Restart}
	for _, selected := range scope.Relations {
		if relation := s.logicalRelationLocked(e, selected.Ref); relation != nil {
			change.Truncated = append(change.Truncated, relation)
		}
	}
	if len(change.Truncated) == 0 {
		return
	}
	change.LSN = e.Cluster.logical.lsn.Allocate()
	owned := ownLogicalChange(change)
	s.appendLogicalEventLocked(logical.RowEvent, owned)
	s.logicalChanges = append(s.logicalChanges, *owned)
}

// logicalRelationLocked returns the capture-time snapshot of a logically
// logged relation: a permanent, non-catalog, non-foreign table.
func (s *StatementScope) logicalRelationLocked(e *Engine, ref ids.ObjectRef) *logical.Relation {
	view := e.PrivateCatalogLocked(s.PendingCatalog())
	cached := &s.logicalRelations
	if cached.ref == ref && cached.catalog == view.State() {
		return cached.relation
	}
	*cached = logicalRelationCache{ref: ref, catalog: view.State()}
	if d, ok := view.Relation(ref.Address); ok {
		cached.relation = logicalRelationOf(view, d)
	}
	return cached.relation
}

// logicalRelationOf snapshots d when it is logically logged: a permanent,
// non-catalog, non-foreign table. Partitioned roots have no rows of their own.
func logicalRelationOf(view catalog.Read, d *catalog.Relation) *logical.Relation {
	if d.RelationKind != catalog.TableRelation || d.Address().ObjectID < firstNormalObjectID || table.EffectivePersistence(view, d) != "logged" {
		return nil
	}
	return logicalRelationSnapshot(view, d)
}

func logicalRelationSnapshot(view catalog.Read, d *catalog.Relation) *logical.Relation {
	r := &logical.Relation{OID: uint32(d.Address().ObjectID), Name: d.ObjectName, ReplicaIdentity: byte(d.EffectiveReplicaIdentity()), Columns: make([]logical.Column, len(d.Columns))}
	if ns, ok := view.Namespace(d.Namespace()); ok {
		r.Schema = ns.Name()
	}
	path := []ids.ObjectAddress{{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}
	var primary []int32
	for _, unique := range d.Constraints.Unique {
		if unique.Primary {
			primary = unique.Attributes
		}
	}
	for i, column := range d.Columns {
		number := int32(i + 1)
		if i < len(d.Attributes) {
			number = d.Attributes[i]
		}
		c := logical.Column{Name: column.Name, Number: number, Usage: column.Usage}
		if column.Usage.Type != nil {
			c.TypeName, _ = regobj.FormatType(view, path, column.Usage.Type.ID(), -1, false)
		}
		r.Columns[i] = c
		switch d.EffectiveReplicaIdentity() {
		case parser.ReplicaIdentityDefault:
			if slices.Contains(primary, number) {
				r.Key = append(r.Key, i)
			}
		case parser.ReplicaIdentityIndex:
			if catalog.ReplicaIdentityKey(view, d, number) {
				r.Key = append(r.Key, i)
			}
		}
	}
	return r
}

// logicalOldTuple applies the replica identity: FULL logs the old row; a key
// logs the old key, for an update only when a key column changed.
func logicalOldTuple(r *logical.Relation, old, next []sqltype.Value) (logical.OldTuple, []sqltype.Value) {
	if r.ReplicaIdentity == byte(parser.ReplicaIdentityFull) {
		return logical.OldFull, old
	}
	if len(r.Key) == 0 || len(old) != len(r.Columns) {
		return logical.OldNone, nil
	}
	if next != nil {
		changed := false
		for _, i := range r.Key {
			if i >= len(next) {
				changed = true
				break
			}
			same, err := old[i].SamePayload(context.Background(), next[i])
			if err != nil || !same {
				changed = true
				break
			}
		}
		if !changed {
			return logical.OldNone, nil
		}
	}
	key := make([]sqltype.Value, len(old))
	for i := range key {
		key[i] = sqltype.Null(r.Columns[i].Usage.Type)
	}
	for _, i := range r.Key {
		key[i] = old[i]
	}
	return logical.OldKey, key
}
