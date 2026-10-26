package core

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

func (s *StatementScope) CaptureForeignKeyRows(ref ids.ObjectRef, idx ids.ObjectAddress) (table.ForeignKeyRows, error) {
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() || ref.Epoch != s.TxRef.Epoch() {
		return table.ForeignKeyRows{}, ids.ErrCatalogReplaced
	}
	changes := s.Owner.Catalog
	if s.Catalog != nil {
		changes = s.Catalog
	}
	storage := e.PrivateStorageLocked(changes, ref.Address)
	if storage == nil {
		return table.ForeignKeyRows{}, ids.ErrCatalogReplaced
	}
	r := table.ForeignKeyRows{Storage: storage, Committed: storage.Versions, Ends: storage.Ends, Own: s.OwnWrites[ref.Address], Staged: s.Stage.Rows[ref.Address], PriorDeletes: s.OwnDeletes[ref.Address], CurrentDeletes: s.Stage.Deletes[ref.Address], Frontier: e.Visibility.CommitSeq()}
	r.Index = idx
	if idx != (ids.ObjectAddress{}) {
		r.Root = index.EffectiveRoot(e.Storage.IndexRoots, changes, idx)
		if r.Root == nil {
			return table.ForeignKeyRows{}, fmt.Errorf("stuntgres: ready foreign-key index root is missing")
		}
	}
	if s.ProjectedRoots {
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

func (s *StatementScope) ForeignKeyGrant(ref ids.ObjectRef, row ids.RowID, mode lock.Mode, stored *storage.Relation) error {
	e := s.Engine()
	policy := lock.Policy{TimeoutSlot: &s.Op.LockWait, Timeout: s.Op.LockTimeout, StatementDeadline: s.Op.StatementDeadline, Source: s.Op.SourceContext}
	request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, lock.Resource{ObjectRef: ref, Row: row}, mode, policy)
	if request != nil {
		if s.ReplacementRead {
			e.ReplacementMu.RUnlock()
			s.ReplacementRead = false
		}
		err = request.Wait()
	}
	if err != nil {
		return err
	}
	if s.implicit && !s.ReplacementRead {
		e.ReplacementMu.RLock()
		s.ReplacementRead = true
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() || ref.Epoch != s.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	changes := s.Owner.Catalog
	if s.Catalog != nil {
		changes = s.Catalog
	}
	if _, ok := e.PrivateCatalogLocked(changes).Find(ref.Address); !ok {
		return ids.ErrCatalogReplaced
	}
	if stored != nil && e.PrivateStorageLocked(changes, ref.Address) != stored {
		return ids.ErrCatalogReplaced
	}
	return s.ValidateLockedRelation(e)
}

func (s *StatementScope) acquireForeignKeyCreation(raw *parser.CreateTable) error {
	declarations := table.ForeignKeyDeclarations(raw)
	if len(declarations) == 0 {
		return nil
	}
	e := s.Engine()
	auth, err := OperationAuthorization(s.Op)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	auth.Catalog = e.PrivateCatalogLocked(s.PendingCatalog())
	auth.Recapture(auth.Catalog)
	temporary := raw.Persistence == "temporary" || len(raw.Name.Parts) == 2 && raw.Name.Parts[0].Text == "pg_temp"
	var destination ids.ObjectAddress
	if temporary {
		destination = temporaryNamespace(auth)
	} else {
		ns, err := e.CreationNamespace(auth, raw.Name)
		if err != nil {
			e.Mu.Unlock()
			return err
		}
		destination = ns.Address
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if raw.IfNotExists && destination != (ids.ObjectAddress{}) && auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: destination, Name: name}) != (ids.ObjectAddress{}) {
		e.Mu.Unlock()
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
				path, pathErr := LookupNamespacesSnapshot(auth, f.Target, analyze.RelationLookup)
				if pathErr != nil {
					e.Mu.Unlock()
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
		ref, resolveErr := e.ResolveReference(auth, f.Target)
		if resolveErr != nil {
			e.Mu.Unlock()
			return resolveErr
		}
		targets[i] = ref
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	e.Mu.Unlock()
	s.foreignKeys().KeyTargets = targets
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		if err := s.ForeignKeyGrant(ref, 0, lock.ShareRowExclusive, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) refreshForeignKeyRows(ref ids.ObjectRef, rows *table.ForeignKeyRows) error {
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() || ref.Epoch != s.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	changes := s.Owner.Catalog
	if s.Catalog != nil {
		changes = s.Catalog
	}
	storage := e.PrivateStorageLocked(changes, ref.Address)
	if storage == nil || storage != rows.Storage {
		return ids.ErrCatalogReplaced
	}
	rows.Committed, rows.Ends, rows.Frontier = storage.Versions, storage.Ends, e.Visibility.CommitSeq()
	rows.Root = index.EffectiveRoot(e.Storage.IndexRoots, changes, rows.Index)
	if rows.Root == nil {
		return fmt.Errorf("stuntgres: ready foreign-key index root is missing")
	}
	return nil
}

// foreignKeys returns the statement's CREATE/ALTER TABLE foreign-key slot,
// allocating it on first use.
func (s *StatementScope) foreignKeys() *table.ForeignKeyCreation {
	if s.ForeignKeysSlot == nil {
		s.ForeignKeysSlot = new(table.ForeignKeyCreation)
	}
	return s.ForeignKeysSlot
}
