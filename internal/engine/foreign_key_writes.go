package engine

import (
	"cmp"
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Captured row/version vectors are immutable. Only shadow identities are indexed;
// values are neither copied nor materialized into an alternate relation.
type foreignKeyRows struct {
	storage                      *storage.Relation
	committed, own, staged       []storage.RowVersion
	ends                         *storage.DeletionRoot
	priorDeletes, currentDeletes *visibility.PrivateDeletes
	shadow                       map[ids.RowID]uint8
	frontier                     ids.CommitSeq
	index                        ids.ObjectAddress
	root                         *storage.IndexRoot
}

func (r foreignKeyRows) each(visit func(storage.RowVersion) (bool, error)) error {
	for _, v := range r.committed {
		if r.shadow[v.Row] != 0 || !visibility.LiveVersion(v, r.frontier, r.ends, r.priorDeletes) || !visibility.LiveVersion(v, r.frontier, r.ends, r.currentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	for _, v := range r.own {
		if r.shadow[v.Row]&2 != 0 || !visibility.LiveVersion(v, r.frontier, nil, r.priorDeletes) || !visibility.LiveVersion(v, r.frontier, nil, r.currentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	for _, v := range r.staged {
		if !visibility.LiveVersion(v, r.frontier, nil, r.currentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	return nil
}
func (r foreignKeyRows) previous(id ids.RowID) (storage.RowVersion, bool) {
	for _, v := range r.own {
		if v.Row == id {
			return v, true
		}
	}
	for _, v := range r.committed {
		if v.Row == id && visibility.LiveVersion(v, r.frontier, r.ends, r.priorDeletes) {
			return v, true
		}
	}
	return storage.RowVersion{}, false
}

func (s *StatementScope) captureForeignKeyRows(ref ids.ObjectRef, index ids.ObjectAddress) (foreignKeyRows, error) {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return foreignKeyRows{}, ids.ErrCatalogReplaced
	}
	changes := s.owner.Catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	storage := e.privateStorageLocked(changes, ref.Address)
	if storage == nil {
		return foreignKeyRows{}, ids.ErrCatalogReplaced
	}
	r := foreignKeyRows{storage: storage, committed: storage.Versions, ends: storage.Ends, own: s.ownWrites[ref.Address], staged: s.stage.rows[ref.Address], priorDeletes: s.ownDeletes[ref.Address], currentDeletes: s.stage.deletes[ref.Address], frontier: e.visibility.CommitSeq()}
	r.index = index
	if index != (ids.ObjectAddress{}) {
		r.root = e.storage.IndexRoots[index]
		if changes != nil && JournalIndexesOrZero(changes).UniqueRoots[index] != nil {
			r.root = JournalIndexesOrZero(changes).UniqueRoots[index]
		}
		if r.root == nil {
			return foreignKeyRows{}, fmt.Errorf("stuntgres: ready foreign-key index root is missing")
		}
	}
	if s.projectedRoots {
		r.own = nil
		r.priorDeletes = nil
	}
	if len(r.own)+len(r.staged) > 0 {
		r.shadow = make(map[ids.RowID]uint8, len(r.own)+len(r.staged))
		for _, v := range r.own {
			r.shadow[v.Row] |= 1
		}
		for _, v := range r.staged {
			r.shadow[v.Row] |= 2
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
	declarations := foreignKeyDeclarations(raw)
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

type foreignKeyJob struct {
	child, parent, index   *catalog.Relation
	definition             catalog.ForeignKeyConstraint
	comparisons            []foreignKeyComparison
	childKey               []sqltype.Value // Reused across final rows and waited probes.
	deletePlan, updatePlan *foreignActionPlan
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
			any, all := foreignKeyNullState(job.comparisons, row.Values)
			if any {
				if f.Full && !all {
					return foreignKeyDiagnostic(s.op.Context(), childAuth.View, childAuth, job.child, job.parent, f, row.Values, false, true, s.output)
				}
				continue
			}
			// Only an actual committed predecessor can suppress an unchanged-key
			// check. Prior own updates, as well as own inserts, still require it.
			if row.Base != 0 {
				previous, ok := children.previous(row.Row)
				if !ok {
					return fmt.Errorf("stuntgres: missing foreign-key update predecessor")
				}
				if previous.Born != 0 {
					unchanged := true
					for _, comparison := range job.comparisons {
						equal, err := comparison.comparison.EqualChildren(s.op.Context(), previous.Values[comparison.child], row.Values[comparison.child], s.input)
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
				return foreignKeyDiagnostic(s.op.Context(), childAuth.View, childAuth, job.child, job.parent, f, row.Values, false, false, s.output)
			}
		}
		checkOld := func(old storage.RowVersion) error {
			return children.each(func(child storage.RowVersion) (bool, error) {
				match, err := foreignKeyRowsMatch(s.op.Context(), job.comparisons, child.Values, old.Values, s.input)
				if err != nil || !match {
					return false, err
				}
				found, err := s.foreignKeyParentExists(parentRef, *job, &parentRows, child.Values, false)
				if err != nil || found {
					return false, err
				}
				return false, foreignKeyDiagnostic(s.op.Context(), parentAuth.View, parentAuth, job.child, job.parent, f, old.Values, true, false, s.output)
			})
		}
		for _, row := range parentRows.staged {
			old, ok := parentRows.previous(row.Row)
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
		if parentRows.currentDeletes != nil {
			for _, rows := range [][]storage.RowVersion{parentRows.committed, parentRows.own} {
				for _, old := range rows {
					if _, deleted := parentRows.currentDeletes.Rows[old.Row]; !deleted {
						continue
					}
					previous, ok := parentRows.previous(old.Row)
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

func (s *StatementScope) foreignKeyParentExists(ref ids.ObjectRef, job foreignKeyJob, rows *foreignKeyRows, child []sqltype.Value, lockParent bool) (bool, error) {
	if err := prepareForeignKeyChild(s.op.Context(), job.comparisons, child, job.childKey, s.input); err != nil {
		return false, err
	}
	for {
		matching, locator, err := findForeignKeyParent(s.op.Context(), job.index, job.comparisons, *rows, job.childKey, s.input)
		if err != nil || matching.Row == 0 {
			return false, err
		}
		if !lockParent || matching.Born == 0 {
			return true, nil
		}
		if err := s.foreignKeyGrant(ref, matching.Row, lock.ForKeyShare, rows.storage); err != nil {
			return false, err
		}
		if err := s.refreshForeignKeyRows(ref, rows); err != nil {
			return false, err
		}
		// The borrowed index entry is an exact physical-version locator. Waiting
		// may end this version; retry the current index, never scan the relation.
		if locator.Position >= 0 && locator.Position < len(rows.committed) {
			parent := rows.committed[locator.Position]
			if parent.Row == locator.Row && parent.Version == locator.Version && rows.committedLive(parent) {
				if err := s.physicalRowEvent(ref, parent, lock.MultiKeyShare, 0); err != nil {
					return false, err
				}
				return foreignKeyRowsMatch(s.op.Context(), job.comparisons, child, parent.Values, s.input)
			}
		}
	}
}

func (s *StatementScope) refreshForeignKeyRows(ref ids.ObjectRef, rows *foreignKeyRows) error {
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
	if storage == nil || storage != rows.storage {
		return ids.ErrCatalogReplaced
	}
	rows.committed, rows.ends, rows.frontier = storage.Versions, storage.Ends, e.visibility.CommitSeq()
	rows.root = e.storage.IndexRoots[rows.index]
	if changes != nil && JournalIndexesOrZero(changes).UniqueRoots[rows.index] != nil {
		rows.root = JournalIndexesOrZero(changes).UniqueRoots[rows.index]
	}
	if rows.root == nil {
		return fmt.Errorf("stuntgres: ready foreign-key index root is missing")
	}
	return nil
}
