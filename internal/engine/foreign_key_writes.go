package engine

import (
	"cmp"
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func (s *StatementScope) captureForeignKeyRows(ref ids.ObjectRef, idx ids.ObjectAddress) (table.ForeignKeyRows, error) {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return table.ForeignKeyRows{}, ids.ErrCatalogReplaced
	}
	changes := s.owner.Catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	storage := e.privateStorageLocked(changes, ref.Address)
	if storage == nil {
		return table.ForeignKeyRows{}, ids.ErrCatalogReplaced
	}
	r := table.ForeignKeyRows{Storage: storage, Committed: storage.Versions, Ends: storage.Ends, Own: s.ownWrites[ref.Address], Staged: s.stage.rows[ref.Address], PriorDeletes: s.ownDeletes[ref.Address], CurrentDeletes: s.stage.deletes[ref.Address], Frontier: e.visibility.CommitSeq()}
	r.Index = idx
	if idx != (ids.ObjectAddress{}) {
		r.Root = index.EffectiveRoot(e.storage.IndexRoots, changes, idx)
		if r.Root == nil {
			return table.ForeignKeyRows{}, fmt.Errorf("stuntgres: ready foreign-key index root is missing")
		}
	}
	if s.projectedRoots {
		r.Own = nil
		r.PriorDeletes = nil
	}
	if len(r.Own)+len(r.Staged) > 0 {
		r.Shadow = make(map[ids.RowID]uint8, len(r.Own)+len(r.Staged))
		for _, v := range r.Own {
			r.Shadow[v.Row] |= 1
		}
		for _, v := range r.Staged {
			r.Shadow[v.Row] |= 2
		}
	}
	return r, nil
}

func (s *StatementScope) foreignKeyGrant(ref ids.ObjectRef, row ids.RowID, mode lock.Mode, stored *storage.Relation) error {
	e := s.engine()
	policy := lock.Policy{TimeoutSlot: &s.op.LockWait, Timeout: s.op.LockTimeout, StatementDeadline: s.op.StatementDeadline, Source: s.op.SourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lock.Resource{ObjectRef: ref, Row: row}, mode, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	if s.implicit && !s.replacementRead {
		e.replacementMu.RLock()
		s.replacementRead = true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	changes := s.owner.Catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	if _, ok := e.privateCatalogLocked(changes).Find(ref.Address); !ok {
		return ids.ErrCatalogReplaced
	}
	if stored != nil && e.privateStorageLocked(changes, ref.Address) != stored {
		return ids.ErrCatalogReplaced
	}
	return s.validateLockedRelation(e)
}

func (s *StatementScope) acquireForeignKeyCreation(raw *parser.CreateTable) error {
	declarations := table.ForeignKeyDeclarations(raw)
	if len(declarations) == 0 {
		return nil
	}
	e := s.engine()
	auth, err := operationAuthorization(s.op)
	if err != nil {
		return err
	}
	e.mu.Lock()
	auth.Catalog = e.privateCatalogLocked(s.owner.Catalog)
	auth.Recapture(auth.Catalog)
	temporary := raw.Persistence == "temporary" || len(raw.Name.Parts) == 2 && raw.Name.Parts[0].Text == "pg_temp"
	var destination ids.ObjectAddress
	if temporary {
		destination = temporaryNamespace(auth)
	} else {
		ns, err := e.creationNamespace(auth, raw.Name)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		destination = ns.Address
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if raw.IfNotExists && destination != (ids.ObjectAddress{}) && auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: destination, Name: name}) != (ids.ObjectAddress{}) {
		e.mu.Unlock()
		return nil
	}
	var refs []ids.ObjectRef
	targets := make([]ids.ObjectRef, len(declarations))
	for i, f := range declarations {
		// Follow the candidate's search order, including a prospective TEMP
		// namespace, rather than comparing differently qualified SQL names.
		self := false
		if f.Target.Parts[len(f.Target.Parts)-1].Text == name {
			if temporary && (len(f.Target.Parts) == 1 || f.Target.Parts[0].Text == "pg_temp") {
				self = true
			} else {
				path, pathErr := e.lookupNamespacesSnapshot(auth, f.Target, analyze.RelationLookup)
				if pathErr != nil {
					e.mu.Unlock()
					return pathErr
				}
				for i := range path.Count() {
					namespace := path.At(i)
					if namespace == destination {
						self = true
						break
					}
					if auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name}) != (ids.ObjectAddress{}) {
						break
					}
				}
			}
		}
		if self {
			continue
		}
		ref, resolveErr := e.resolveReference(auth, f.Target)
		if resolveErr != nil {
			e.mu.Unlock()
			return resolveErr
		}
		targets[i] = ref
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	e.mu.Unlock()
	s.foreignActions().KeyTargets = targets
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		if err := s.foreignKeyGrant(ref, 0, lock.ShareRowExclusive, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) refreshForeignKeyRows(ref ids.ObjectRef, rows *table.ForeignKeyRows) error {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	changes := s.owner.Catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	storage := e.privateStorageLocked(changes, ref.Address)
	if storage == nil || storage != rows.Storage {
		return ids.ErrCatalogReplaced
	}
	rows.Committed, rows.Ends, rows.Frontier = storage.Versions, storage.Ends, e.visibility.CommitSeq()
	rows.Root = index.EffectiveRoot(e.storage.IndexRoots, changes, rows.Index)
	if rows.Root == nil {
		return fmt.Errorf("stuntgres: ready foreign-key index root is missing")
	}
	return nil
}
