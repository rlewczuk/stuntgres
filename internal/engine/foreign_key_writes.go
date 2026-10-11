package engine

import (
	"cmp"
	"fmt"
	"slices"

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
	shadow                       map[RowID]uint8
	frontier                     CommitSeq
	index                        ObjectAddress
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
func (r foreignKeyRows) previous(id RowID) (rowVersion, bool) {
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

func (s *StatementScope) captureForeignKeyRows(ref ObjectRef, index ObjectAddress) (foreignKeyRows, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.owner != e.owner || ref.Epoch != e.state.epoch || ref.Epoch != s.txRef.epoch {
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
	if index != (ObjectAddress{}) {
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
		r.shadow = make(map[RowID]uint8, len(r.own)+len(r.staged))
		for _, v := range r.own {
			r.shadow[v.row] |= 1
		}
		for _, v := range r.staged {
			r.shadow[v.row] |= 2
		}
	}
	return r, nil
}

func (s *StatementScope) foreignKeyGrant(ref ObjectRef, row RowID, mode relationLockMode, storage *relationStorage) error {
	e := s.op.session.Engine()
	policy := relationLockPolicy{operation: s.op, timeout: s.op.lockTimeout, statementDeadline: s.op.statementDeadline, source: s.op.sourceContext}
	request, err := e.locks.startAcquire(s.op.Context(), s.ticket, lockResource{ObjectRef: ref, row: row}, mode, policy)
	if request != nil {
		if s.replacementRead {
			e.replacementMu.RUnlock()
			s.replacementRead = false
		}
		err = request.wait()
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
	if ref.owner != e.owner || ref.Epoch != e.state.epoch || ref.Epoch != s.txRef.epoch {
		return ErrCatalogReplaced
	}
	changes := s.owner.catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	if _, ok := e.privateCatalogLocked(changes).state.descriptors[ref.Address]; !ok {
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
	temporary := raw.Persistence == "temporary" || len(raw.Name.Parts) == 2 && raw.Name.Parts[0].Text == "pg_temp"
	var destination ObjectAddress
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
	if raw.IfNotExists && destination != (ObjectAddress{}) && auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: destination, Name: name}] != (ObjectAddress{}) {
		e.mu.Unlock()
		return nil
	}
	var refs []ObjectRef
	targets := make([]ObjectRef, len(declarations))
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
					if auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: name}] != (ObjectAddress{}) {
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
	slices.SortFunc(refs, func(a, b ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		if err := s.foreignKeyGrant(ref, 0, shareRowExclusive, nil); err != nil {
			return err
		}
	}
	return nil
}

type foreignKeyJob struct {
	child, parent, index *relationDescriptor
	definition           foreignKeyConstraint
	comparisons          []foreignKeyComparison
	childKey             []sqltype.Value // Reused across final rows and waited probes.
}

func (s *StatementScope) checkForeignKeys() error {
	if len(s.stage.rows) == 0 && len(s.stage.deletes) == 0 {
		return nil
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	changes := s.owner.catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	view := e.privateCatalogLocked(changes)
	var jobs []foreignKeyJob
	var refs []ObjectRef
	for _, descriptor := range view.state.descriptors {
		child, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		for _, f := range child.foreignKeys {
			if len(s.stage.rows[child.address]) == 0 && len(s.stage.rows[f.Target]) == 0 && s.stage.deletes[f.Target] == nil {
				continue
			}
			parent, ok := view.state.descriptors[f.Target].(*relationDescriptor)
			if !ok {
				e.mu.Unlock()
				return ErrCatalogReplaced
			}
			index, ok := view.state.descriptors[f.Index].(*relationDescriptor)
			if !ok {
				e.mu.Unlock()
				return ErrCatalogReplaced
			}
			jobs = append(jobs, foreignKeyJob{child: child, parent: parent, index: index, definition: f})
			for _, a := range []ObjectAddress{child.address, parent.address} {
				ref := ObjectRef{owner: e.owner, Epoch: view.Epoch(), Address: a}
				if !slices.Contains(refs, ref) {
					refs = append(refs, ref)
				}
			}
		}
	}
	e.mu.Unlock()
	if len(jobs) == 0 {
		return nil
	}
	slices.SortFunc(jobs, func(a, b foreignKeyJob) int { return cmp.Compare(a.definition.OID, b.definition.OID) })
	slices.SortFunc(refs, func(a, b ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		if err := s.foreignKeyGrant(ref, 0, relationRowShare, nil); err != nil {
			return err
		}
	}
	auth, err := s.op.Authorization()
	if err != nil {
		return err
	}
	e.mu.Lock()
	auth.catalog = e.privateCatalogLocked(changes)
	e.mu.Unlock()
	for i := range jobs {
		job := &jobs[i]
		f := job.definition
		child, ok := auth.catalog.state.descriptors[job.child.address].(*relationDescriptor)
		if !ok {
			return ErrCatalogReplaced
		}
		parent, ok := auth.catalog.state.descriptors[job.parent.address].(*relationDescriptor)
		if !ok {
			return ErrCatalogReplaced
		}
		index, ok := auth.catalog.state.descriptors[job.index.address].(*relationDescriptor)
		if !ok {
			return ErrCatalogReplaced
		}
		found := false
		for _, current := range child.foreignKeys {
			if current.OID != f.OID {
				continue
			}
			if current.Target != f.Target || current.Index != f.Index || current.Full != f.Full || !slices.Equal(current.Attributes, f.Attributes) || !slices.Equal(current.Referenced, f.Referenced) {
				return ErrCatalogReplaced
			}
			f = current
			job.definition = current
			found = true
			break
		}
		if !found || index.sqlIndex == nil || index.sqlIndex.parent != parent.address {
			return ErrCatalogReplaced
		}
		job.child, job.parent, job.index = child, parent, index
		job.comparisons, err = bindForeignKeyComparison(auth.catalog, job.child, job.parent, job.index, f)
		if err != nil {
			return err
		}
		job.childKey = make([]sqltype.Value, len(job.comparisons))
		childRef := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: job.child.address}
		parentRef := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: f.Target}
		parentRows, err := s.captureForeignKeyRows(parentRef, f.Index)
		if err != nil {
			return err
		}
		children, err := s.captureForeignKeyRows(childRef, ObjectAddress{})
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
					return foreignKeyDiagnostic(s.op.Context(), auth, job.child, job.parent, f, row.values, false, true, s.output)
				}
				continue
			}
			found, err := s.foreignKeyParentExists(parentRef, *job, &parentRows, row.values, true)
			if err != nil {
				return err
			}
			if !found {
				return foreignKeyDiagnostic(s.op.Context(), auth, job.child, job.parent, f, row.values, false, false, s.output)
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
				return false, foreignKeyDiagnostic(s.op.Context(), auth, job.child, job.parent, f, old.values, true, false, s.output)
			})
		}
		for _, row := range parentRows.staged {
			old, ok := parentRows.previous(row.row)
			if ok {
				unchanged, err := foreignKeyParentUnchanged(s.op.Context(), job.comparisons, old.values, row.values, s.input)
				if err != nil {
					return err
				}
				if unchanged {
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

func (s *StatementScope) foreignKeyParentExists(ref ObjectRef, job foreignKeyJob, rows *foreignKeyRows, child []sqltype.Value, lock bool) (bool, error) {
	if err := prepareForeignKeyChild(s.op.Context(), job.comparisons, child, job.childKey, s.input); err != nil {
		return false, err
	}
	for {
		matching, locator, err := findForeignKeyParent(s.op.Context(), job.index, job.comparisons, *rows, job.childKey, s.input)
		if err != nil || matching.row == 0 {
			return false, err
		}
		if !lock || matching.born == 0 {
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
				return foreignKeyRowsMatch(s.op.Context(), job.comparisons, child, parent.values, s.input)
			}
		}
	}
}

func (s *StatementScope) refreshForeignKeyRows(ref ObjectRef, rows *foreignKeyRows) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.owner != e.owner || ref.Epoch != e.state.epoch || ref.Epoch != s.txRef.epoch {
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
