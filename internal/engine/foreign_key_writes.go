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
	"stuntgres/internal/sqltype"
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
		r.Root = e.storage.IndexRoots[idx]
		if changes != nil && index.JournalIndexesOrZero(changes).UniqueRoots[idx] != nil {
			r.Root = index.JournalIndexesOrZero(changes).UniqueRoots[idx]
		}
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
	s.foreignActions().keyTargets = targets
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		if err := s.foreignKeyGrant(ref, 0, lock.ShareRowExclusive, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *StatementScope) checkForeignKeys() error {
	// Catalog shape projections replace historical own overlays. They are not
	// newly issued DML; selected ALTER/catalog validators own their obligations.
	if s.projectedRoots {
		return nil
	}
	if len(s.stage.rows) == 0 && len(s.stage.deletes) == 0 {
		return nil
	}
	e := s.engine()
	jobs := s.foreignActionsOrZero().jobs
	if len(jobs) == 0 {
		return nil
	}
	auth, err := operationAuthorization(s.op)
	if err != nil {
		return err
	}
	e.mu.Lock()
	changes := s.owner.Catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	auth.Catalog = e.privateCatalogLocked(changes)
	auth.Recapture(auth.Catalog)
	e.mu.Unlock()
	for i := range jobs {
		job := &jobs[i]
		if err = refreshForeignActionJob(auth.Catalog, job); err != nil {
			return err
		}
		f := job.definition
		childAuth, parentAuth := auth, auth
		if admission := s.foreignActionsOrZero().admissions[job.child.Address()]; admission != nil && admission.mutated {
			childAuth = foreignActionOwner(auth, admission.relation.Owner)
		}
		if admission := s.foreignActionsOrZero().admissions[job.parent.Address()]; admission != nil && admission.mutated {
			parentAuth = foreignActionOwner(auth, admission.relation.Owner)
		}
		childRef := e.catalog.Domain.Ref(auth.Catalog.Epoch(), job.child.Address())
		parentRef := e.catalog.Domain.Ref(auth.Catalog.Epoch(), f.Target)
		parentRows, err := s.captureForeignKeyRows(parentRef, f.Index)
		if err != nil {
			return err
		}
		children, err := s.captureForeignKeyRows(childRef, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		// Child checks only newly written final rows, not unaffected old rows.
		for _, row := range s.stage.rows[childRef.Address] {
			if root := s.stage.deletes[childRef.Address]; root != nil {
				if _, deleted := root.Rows[row.Row]; deleted {
					continue
				}
			}
			any, all := table.ForeignKeyNullState(job.comparisons, row.Values)
			if any {
				if f.Full && !all {
					return table.ForeignKeyDiagnostic(s.op.Context(), childAuth.View, childAuth, job.child, job.parent, f, row.Values, false, true, s.output)
				}
				continue
			}
			// Only an actual committed predecessor can suppress an unchanged-key
			// check. Prior own updates, as well as own inserts, still require it.
			if row.Base != 0 {
				previous, ok := children.Previous(row.Row)
				if !ok {
					return fmt.Errorf("stuntgres: missing foreign-key update predecessor")
				}
				if previous.Born != 0 {
					unchanged := true
					for _, comparison := range job.comparisons {
						equal, err := comparison.Comparison.EqualChildren(s.op.Context(), previous.Values[comparison.Child], row.Values[comparison.Child], s.input)
						if err != nil {
							return err
						}
						if !equal {
							unchanged = false
							break
						}
					}
					if unchanged {
						continue
					}
				}
			}
			found, err := s.foreignKeyParentExists(parentRef, *job, &parentRows, row.Values, true)
			if err != nil {
				return err
			}
			if !found {
				return table.ForeignKeyDiagnostic(s.op.Context(), childAuth.View, childAuth, job.child, job.parent, f, row.Values, false, false, s.output)
			}
		}
		checkOld := func(old storage.RowVersion) error {
			return children.Each(func(child storage.RowVersion) (bool, error) {
				match, err := table.ForeignKeyRowsMatch(s.op.Context(), job.comparisons, child.Values, old.Values, s.input)
				if err != nil || !match {
					return false, err
				}
				found, err := s.foreignKeyParentExists(parentRef, *job, &parentRows, child.Values, false)
				if err != nil || found {
					return false, err
				}
				return false, table.ForeignKeyDiagnostic(s.op.Context(), parentAuth.View, parentAuth, job.child, job.parent, f, old.Values, true, false, s.output)
			})
		}
		for _, row := range parentRows.Staged {
			old, ok := parentRows.Previous(row.Row)
			if ok {
				if foreignActionSameKey(job.comparisons, old.Values, row.Values) {
					continue
				}
				if err := checkOld(old); err != nil {
					return err
				}
			} else if row.Base != 0 {
				return fmt.Errorf("stuntgres: missing foreign-key update predecessor")
			}
		}
		if parentRows.CurrentDeletes != nil {
			for _, rows := range [][]storage.RowVersion{parentRows.Committed, parentRows.Own} {
				for _, old := range rows {
					if _, deleted := parentRows.CurrentDeletes.Rows[old.Row]; !deleted {
						continue
					}
					previous, ok := parentRows.Previous(old.Row)
					if !ok || previous.Version != old.Version {
						continue
					}
					if err := checkOld(old); err != nil {
						return err
					}
				}
			}
		}
	}
	return s.op.Context().Err()
}

func (s *StatementScope) foreignKeyParentExists(ref ids.ObjectRef, job foreignKeyJob, rows *table.ForeignKeyRows, child []sqltype.Value, lockParent bool) (bool, error) {
	if err := table.PrepareForeignKeyChild(s.op.Context(), job.comparisons, child, job.childKey, s.input); err != nil {
		return false, err
	}
	for {
		matching, locator, err := table.FindForeignKeyParent(s.op.Context(), job.index, job.comparisons, *rows, job.childKey, s.input)
		if err != nil || matching.Row == 0 {
			return false, err
		}
		if !lockParent || matching.Born == 0 {
			return true, nil
		}
		if err := s.foreignKeyGrant(ref, matching.Row, lock.ForKeyShare, rows.Storage); err != nil {
			return false, err
		}
		if err := s.refreshForeignKeyRows(ref, rows); err != nil {
			return false, err
		}
		// The borrowed index entry is an exact physical-version locator. Waiting
		// may end this version; retry the current index, never scan the relation.
		if locator.Position >= 0 && locator.Position < len(rows.Committed) {
			parent := rows.Committed[locator.Position]
			if parent.Row == locator.Row && parent.Version == locator.Version && rows.CommittedLive(parent) {
				if err := s.physicalRowEvent(ref, parent, lock.MultiKeyShare, 0); err != nil {
					return false, err
				}
				return table.ForeignKeyRowsMatch(s.op.Context(), job.comparisons, child, parent.Values, s.input)
			}
		}
	}
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
	rows.Root = e.storage.IndexRoots[rows.Index]
	if changes != nil && index.JournalIndexesOrZero(changes).UniqueRoots[rows.Index] != nil {
		rows.Root = index.JournalIndexesOrZero(changes).UniqueRoots[rows.Index]
	}
	if rows.Root == nil {
		return fmt.Errorf("stuntgres: ready foreign-key index root is missing")
	}
	return nil
}
