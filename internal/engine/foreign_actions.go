package engine

import (
	"cmp"
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func sameForeignKeyDefinition(a, b foreignKeyConstraint) bool {
	return a.OID == b.OID && a.Target == b.Target && a.Index == b.Index && a.Full == b.Full && a.UpdateAction == b.UpdateAction && a.DeleteAction == b.DeleteAction && slices.Equal(a.Attributes, b.Attributes) && slices.Equal(a.Referenced, b.Referenced) && slices.Equal(a.DeleteAttributes, b.DeleteAttributes)
}

func (s *StatementScope) prepareForeignActions() ([]foreignKeyJob, AuthorizationView, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		e.mu.Unlock()
		return nil, auth, err
	}
	changes := s.owner.catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	auth.catalog = e.privateCatalogLocked(changes)
	auth.recaptureCatalog(auth.catalog)
	relevant := false
	for _, descriptor := range auth.catalog.Descriptors() {
		child, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		for _, f := range child.foreignKeys {
			if len(s.stage.rows[child.address]) != 0 || s.stage.deletes[child.address] != nil || len(s.stage.rows[f.Target]) != 0 || s.stage.deletes[f.Target] != nil {
				relevant = true
				break
			}
		}
		if relevant {
			break
		}
	}
	if !relevant {
		e.mu.Unlock()
		return nil, auth, nil
	}
	// Reachability is by actual transition direction, not an executable global
	// registry. Only the existing catalog is consulted while capturing authority.
	directions := make(map[ids.ObjectAddress]uint8)
	changed := make(map[ids.ObjectAddress]bool)
	for a, rows := range s.stage.rows {
		if len(rows) != 0 {
			changed[a] = true
			directions[a] |= 2
		}
	}
	for a, root := range s.stage.deletes {
		if root != nil {
			changed[a] = true
			directions[a] |= 1
		}
	}
	again := true
	for again {
		again = false
		for _, descriptor := range auth.catalog.Descriptors() {
			child, ok := descriptor.(*relationDescriptor)
			if !ok {
				continue
			}
			for _, f := range child.foreignKeys {
				parentDirections := directions[f.Target]
				next := directions[child.address]
				if parentDirections&1 != 0 && f.DeleteAction >= parser.ForeignKeyCascade {
					if f.DeleteAction == parser.ForeignKeyCascade {
						next |= 1
					} else {
						next |= 2
					}
				}
				if parentDirections&2 != 0 && f.UpdateAction >= parser.ForeignKeyCascade {
					next |= 2
				}
				if next != directions[child.address] {
					directions[child.address] = next
					changed[child.address] = true
					again = true
				}
			}
		}
	}
	var jobs []foreignKeyJob
	modes := make(map[ids.ObjectRef]lock.Mode)
	for _, descriptor := range auth.catalog.Descriptors() {
		child, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		for _, f := range child.foreignKeys {
			if !changed[child.address] && !changed[f.Target] {
				continue
			}
			parent, pok := auth.catalog.Relation(f.Target)
			index, iok := auth.catalog.Relation(f.Index)
			if !pok || !iok {
				e.mu.Unlock()
				return nil, auth, ErrCatalogReplaced
			}
			jobs = append(jobs, foreignKeyJob{child: child, parent: parent, index: index, definition: f})
			for _, a := range []ids.ObjectAddress{child.address, parent.address} {
				ref := e.catalog.domain.Ref(auth.catalog.Epoch(), a)
				if modes[ref] == 0 {
					modes[ref] = lock.RowShare
				}
			}
			d := directions[f.Target]
			if d&1 != 0 && f.DeleteAction >= parser.ForeignKeyCascade || d&2 != 0 && f.UpdateAction >= parser.ForeignKeyCascade {
				modes[e.catalog.domain.Ref(auth.catalog.Epoch(), child.address)] = lock.RowExclusive
			}
		}
	}
	e.mu.Unlock()
	slices.SortFunc(jobs, func(a, b foreignKeyJob) int { return cmp.Compare(a.definition.OID, b.definition.OID) })
	refs := make([]ids.ObjectRef, 0, len(modes))
	for ref := range modes {
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		if err = s.foreignKeyGrant(ref, 0, modes[ref], nil); err != nil {
			return nil, auth, err
		}
	}
	e.mu.Lock()
	auth.catalog = e.privateCatalogLocked(changes)
	auth.recaptureCatalog(auth.catalog)
	e.mu.Unlock()
	for i := range jobs {
		job := &jobs[i]
		if err = refreshForeignActionJob(auth.catalog, job); err != nil {
			return nil, auth, err
		}
		job.comparisons, err = bindForeignKeyComparison(auth.catalog, job.child, job.parent, job.index, job.definition)
		if err != nil {
			return nil, auth, err
		}
		job.childKey = make([]sqltype.Value, len(job.comparisons))
	}
	return jobs, auth, nil
}

func refreshForeignActionJob(view CatalogRead, job *foreignKeyJob) error {
	child, cok := view.Relation(job.child.address)
	parent, pok := view.Relation(job.parent.address)
	index, iok := view.Relation(job.index.address)
	if !cok || !pok || !iok || index.sqlIndex == nil || !sameRelationRowShape(child, job.child) || !sameRelationRowShape(parent, job.parent) || !sameIndexKeySemantics(index, job.index) {
		return ErrCatalogReplaced
	}
	found := false
	for _, current := range child.foreignKeys {
		if current.OID == job.definition.OID {
			if !sameForeignKeyDefinition(current, job.definition) {
				return ErrCatalogReplaced
			}
			job.definition = current
			found = true
			break
		}
	}
	if !found {
		return ErrCatalogReplaced
	}
	job.child, job.parent, job.index = child, parent, index
	return nil
}

func (s *StatementScope) settleForeignActions() error {
	if len(s.stage.rows) == 0 && len(s.stage.deletes) == 0 {
		return nil
	}
	jobs, auth, err := s.prepareForeignActions()
	if err != nil {
		return err
	}
	s.foreignActionJobs = jobs
	if len(jobs) == 0 {
		return nil
	}
	e := s.op.session.Engine()
	// Borrow the initiating predecessor/successor images before any action merge.
	for a, rows := range s.stage.rows {
		ref := e.catalog.domain.Ref(auth.catalog.Epoch(), a)
		image, err := s.captureForeignKeyRows(ref, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		for _, next := range rows {
			if uniqueRowDeleted(next.row, s.stage.deletes[a]) {
				continue
			}
			old, ok := image.previous(next.row)
			if ok {
				s.foreignActionTransitions = append(s.foreignActionTransitions, foreignActionTransition{relation: a, old: old, next: next})
			} else if next.base != 0 {
				return fmt.Errorf("stuntgres: missing foreign-key update predecessor")
			}
		}
	}
	for a, root := range s.stage.deletes {
		if root == nil {
			continue
		}
		ref := e.catalog.domain.Ref(auth.catalog.Epoch(), a)
		image, err := s.captureForeignKeyRows(ref, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		for id := range root.rows {
			old, ok := image.previous(id)
			if !ok {
				for _, row := range image.staged {
					if row.row == id {
						old, ok = row, true
						break
					}
				}
			}
			if ok {
				s.foreignActionTransitions = append(s.foreignActionTransitions, foreignActionTransition{relation: a, old: old})
			}
		}
	}
	s.foreignActionsSettling = true
	defer func() { s.foreignActionsSettling = false; s.foreignActionAuth = nil }()
	for at := 0; at < len(s.foreignActionTransitions); at++ {
		event := s.foreignActionTransitions[at]
		if err = s.op.Context().Err(); err != nil {
			return err
		}
		for i := range jobs {
			job := &jobs[i]
			if job.parent.address != event.relation {
				continue
			}
			action := job.definition.DeleteAction
			if event.next.row != 0 {
				if foreignActionSameKey(job.comparisons, event.old.values, event.next.values) {
					continue
				}
				action = job.definition.UpdateAction
			}
			if action == parser.ForeignKeyNoAction {
				continue
			}
			if err = s.applyForeignAction(auth, job, event, action); err != nil {
				return err
			}
		}
	}
	return nil
}
