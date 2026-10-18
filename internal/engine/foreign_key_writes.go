package engine

import (
	"cmp"
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Captured row/version vectors are immutable. Only shadow identities are indexed;
// values are neither copied nor materialized into an alternate relation.
type foreignKeyRows struct {
	storage                      *relationStorage
	committed, own, staged       []rowVersion
	ends                         *deletionRoot
	priorDeletes, currentDeletes *privateDeletionRoot
	shadow                       map[ids.RowID]uint8
	frontier                     ids.CommitSeq
	index                        ids.ObjectAddress
	root                         *logicalIndexRoot
}

func (r foreignKeyRows) each(visit func(rowVersion) (bool, error)) error {
	for _, v := range r.committed {
		if r.shadow[v.row] != 0 || !liveVersion(v, r.frontier, r.ends, r.priorDeletes) || !liveVersion(v, r.frontier, r.ends, r.currentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	for _, v := range r.own {
		if r.shadow[v.row]&2 != 0 || !liveVersion(v, r.frontier, nil, r.priorDeletes) || !liveVersion(v, r.frontier, nil, r.currentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	for _, v := range r.staged {
		if !liveVersion(v, r.frontier, nil, r.currentDeletes) {
			continue
		}
		if stop, err := visit(v); err != nil || stop {
			return err
		}
	}
	return nil
}
func (r foreignKeyRows) previous(id ids.RowID) (rowVersion, bool) {
	for _, v := range r.own {
		if v.row == id {
			return v, true
		}
	}
	for _, v := range r.committed {
		if v.row == id && liveVersion(v, r.frontier, r.ends, r.priorDeletes) {
			return v, true
		}
	}
	return rowVersion{}, false
}

func (s *StatementScope) captureForeignKeyRows(ref ids.ObjectRef, index ids.ObjectAddress) (foreignKeyRows, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Domain() != e.catalog.domain || ref.Epoch != e.catalog.state.epoch || ref.Epoch != s.txRef.Epoch() {
		return foreignKeyRows{}, ErrCatalogReplaced
	}
	changes := s.owner.catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	storage := e.privateStorageLocked(changes, ref.Address)
	if storage == nil {
		return foreignKeyRows{}, ErrCatalogReplaced
	}
	r := foreignKeyRows{storage: storage, committed: storage.versions, ends: storage.ends, own: s.ownWrites[ref.Address], staged: s.stage.rows[ref.Address], priorDeletes: s.ownDeletes[ref.Address], currentDeletes: s.stage.deletes[ref.Address], frontier: e.commitSeq}
	r.index = index
	if index != (ids.ObjectAddress{}) {
		r.root = e.indexRoots[index]
		if changes != nil && changes.uniqueRoots[index] != nil {
			r.root = changes.uniqueRoots[index]
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
			r.shadow[v.row] |= 1
		}
		for _, v := range r.staged {
			r.shadow[v.row] |= 2
		}
	}
	return r, nil
}

func (s *StatementScope) foreignKeyGrant(ref ids.ObjectRef, row ids.RowID, mode relationLockMode, storage *relationStorage) error {
	e := s.op.session.Engine()
	policy := relationLockPolicy{TimeoutSlot: &s.op.lockWait, Timeout: s.op.lockTimeout, StatementDeadline: s.op.statementDeadline, Source: s.op.sourceContext}
	request, err := e.locks.StartAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: ref, Row: row}, mode, policy)
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
	if ref.Domain() != e.catalog.domain || ref.Epoch != e.catalog.state.epoch || ref.Epoch != s.txRef.Epoch() {
		return ErrCatalogReplaced
	}
	changes := s.owner.catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	if _, ok := e.privateCatalogLocked(changes).Find(ref.Address); !ok {
		return ErrCatalogReplaced
	}
	if storage != nil && e.privateStorageLocked(changes, ref.Address) != storage {
		return ErrCatalogReplaced
	}
	return s.validateLockedRelation(e)
}

func (s *StatementScope) acquireForeignKeyCreation(raw *parser.CreateTable) error {
	declarations := foreignKeyDeclarations(raw)
	if len(declarations) == 0 {
		return nil
	}
	e := s.op.session.Engine()
	auth, err := s.op.Authorization()
	if err != nil {
		return err
	}
	e.mu.Lock()
	auth.catalog = e.privateCatalogLocked(s.owner.catalog)
	auth.recaptureCatalog(auth.catalog)
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
	if raw.IfNotExists && destination != (ids.ObjectAddress{}) && auth.catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: destination, Name: name}) != (ids.ObjectAddress{}) {
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
				path, pathErr := e.lookupNamespacesSnapshot(auth, f.Target, relationLookup)
				if pathErr != nil {
					e.mu.Unlock()
					return pathErr
				}
				for i := range path.count() {
					namespace := path.at(i)
					if namespace == destination {
						self = true
						break
					}
					if auth.catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: name}) != (ids.ObjectAddress{}) {
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
	s.foreignKeyTargets = targets
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		if err := s.foreignKeyGrant(ref, 0, shareRowExclusive, nil); err != nil {
			return err
		}
	}
	return nil
}

type foreignKeyJob struct {
	child, parent, index   *relationDescriptor
	definition             foreignKeyConstraint
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
	e := s.op.session.Engine()
	jobs := s.foreignActionJobs
	if len(jobs) == 0 {
		return nil
	}
	auth, err := s.op.Authorization()
	if err != nil {
		return err
	}
	e.mu.Lock()
	changes := s.owner.catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	auth.catalog = e.privateCatalogLocked(changes)
	auth.recaptureCatalog(auth.catalog)
	e.mu.Unlock()
	for i := range jobs {
		job := &jobs[i]
		if err = refreshForeignActionJob(auth.catalog, job); err != nil {
			return err
		}
		f := job.definition
		childAuth, parentAuth := auth, auth
		if admission := s.foreignActionAdmissions[job.child.address]; admission != nil && admission.mutated {
			childAuth = foreignActionOwner(auth, admission.relation.owner)
		}
		if admission := s.foreignActionAdmissions[job.parent.address]; admission != nil && admission.mutated {
			parentAuth = foreignActionOwner(auth, admission.relation.owner)
		}
		childRef := e.catalog.domain.Ref(auth.catalog.Epoch(), job.child.address)
		parentRef := e.catalog.domain.Ref(auth.catalog.Epoch(), f.Target)
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
				if _, deleted := root.rows[row.row]; deleted {
					continue
				}
			}
			any, all := foreignKeyNullState(job.comparisons, row.values)
			if any {
				if f.Full && !all {
					return foreignKeyDiagnostic(s.op.Context(), childAuth, job.child, job.parent, f, row.values, false, true, s.output)
				}
				continue
			}
			// Only an actual committed predecessor can suppress an unchanged-key
			// check. Prior own updates, as well as own inserts, still require it.
			if row.base != 0 {
				previous, ok := children.previous(row.row)
				if !ok {
					return fmt.Errorf("stuntgres: missing foreign-key update predecessor")
				}
				if previous.born != 0 {
					unchanged := true
					for _, comparison := range job.comparisons {
						equal, err := comparison.comparison.EqualChildren(s.op.Context(), previous.values[comparison.child], row.values[comparison.child], s.input)
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
			found, err := s.foreignKeyParentExists(parentRef, *job, &parentRows, row.values, true)
			if err != nil {
				return err
			}
			if !found {
				return foreignKeyDiagnostic(s.op.Context(), childAuth, job.child, job.parent, f, row.values, false, false, s.output)
			}
		}
		checkOld := func(old rowVersion) error {
			return children.each(func(child rowVersion) (bool, error) {
				match, err := foreignKeyRowsMatch(s.op.Context(), job.comparisons, child.values, old.values, s.input)
				if err != nil || !match {
					return false, err
				}
				found, err := s.foreignKeyParentExists(parentRef, *job, &parentRows, child.values, false)
				if err != nil || found {
					return false, err
				}
				return false, foreignKeyDiagnostic(s.op.Context(), parentAuth, job.child, job.parent, f, old.values, true, false, s.output)
			})
		}
		for _, row := range parentRows.staged {
			old, ok := parentRows.previous(row.row)
			if ok {
				if foreignActionSameKey(job.comparisons, old.values, row.values) {
					continue
				}
				if err := checkOld(old); err != nil {
					return err
				}
			} else if row.base != 0 {
				return fmt.Errorf("stuntgres: missing foreign-key update predecessor")
			}
		}
		if parentRows.currentDeletes != nil {
			for _, rows := range [][]rowVersion{parentRows.committed, parentRows.own} {
				for _, old := range rows {
					if _, deleted := parentRows.currentDeletes.rows[old.row]; !deleted {
						continue
					}
					previous, ok := parentRows.previous(old.row)
					if !ok || previous.version != old.version {
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
		if err != nil || matching.row == 0 {
			return false, err
		}
		if !lockParent || matching.born == 0 {
			return true, nil
		}
		if err := s.foreignKeyGrant(ref, matching.row, keyShare, rows.storage); err != nil {
			return false, err
		}
		if err := s.refreshForeignKeyRows(ref, rows); err != nil {
			return false, err
		}
		// The borrowed index entry is an exact physical-version locator. Waiting
		// may end this version; retry the current index, never scan the relation.
		if locator.position >= 0 && locator.position < len(rows.committed) {
			parent := rows.committed[locator.position]
			if parent.row == locator.row && parent.version == locator.version && rows.committedLive(parent) {
				if err := s.physicalRowEvent(ref, parent, multiKeyShare, 0); err != nil {
					return false, err
				}
				return foreignKeyRowsMatch(s.op.Context(), job.comparisons, child, parent.values, s.input)
			}
		}
	}
}

func (s *StatementScope) refreshForeignKeyRows(ref ids.ObjectRef, rows *foreignKeyRows) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Domain() != e.catalog.domain || ref.Epoch != e.catalog.state.epoch || ref.Epoch != s.txRef.Epoch() {
		return ErrCatalogReplaced
	}
	changes := s.owner.catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	storage := e.privateStorageLocked(changes, ref.Address)
	if storage == nil || storage != rows.storage {
		return ErrCatalogReplaced
	}
	rows.committed, rows.ends, rows.frontier = storage.versions, storage.ends, e.commitSeq
	rows.root = e.indexRoots[rows.index]
	if changes != nil && changes.uniqueRoots[rows.index] != nil {
		rows.root = changes.uniqueRoots[rows.index]
	}
	if rows.root == nil {
		return fmt.Errorf("stuntgres: ready foreign-key index root is missing")
	}
	return nil
}
