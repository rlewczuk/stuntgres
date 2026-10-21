package engine

import (
	"cmp"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) prepareForeignActions() ([]foreignKeyJob, AuthorizationView, error) {
	e := s.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(s.op)
	if err != nil {
		e.mu.Unlock()
		return nil, auth, err
	}
	changes := s.owner.Catalog
	if s.catalog != nil {
		changes = s.catalog
	}
	auth.Catalog = e.privateCatalogLocked(changes)
	auth.Recapture(auth.Catalog)
	relevant := false
	for _, descriptor := range auth.Catalog.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if len(s.stage.rows[child.Address()]) != 0 || s.stage.deletes[child.Address()] != nil || len(s.stage.rows[f.Target]) != 0 || s.stage.deletes[f.Target] != nil {
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
		for _, descriptor := range auth.Catalog.Descriptors() {
			child, ok := descriptor.(*catalog.Relation)
			if !ok {
				continue
			}
			for _, f := range child.Constraints.ForeignKeys {
				parentDirections := directions[f.Target]
				next := directions[child.Address()]
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
				if next != directions[child.Address()] {
					directions[child.Address()] = next
					changed[child.Address()] = true
					again = true
				}
			}
		}
	}
	var jobs []foreignKeyJob
	modes := make(map[ids.ObjectRef]lock.Mode)
	for _, descriptor := range auth.Catalog.Descriptors() {
		child, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, f := range child.Constraints.ForeignKeys {
			if !changed[child.Address()] && !changed[f.Target] {
				continue
			}
			parent, pok := auth.Catalog.Relation(f.Target)
			index, iok := auth.Catalog.Relation(f.Index)
			if !pok || !iok {
				e.mu.Unlock()
				return nil, auth, ids.ErrCatalogReplaced
			}
			jobs = append(jobs, foreignKeyJob{Child: child, Parent: parent, Index: index, Definition: f})
			for _, a := range []ids.ObjectAddress{child.Address(), parent.Address()} {
				ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), a)
				if modes[ref] == 0 {
					modes[ref] = lock.RowShare
				}
			}
			d := directions[f.Target]
			if d&1 != 0 && f.DeleteAction >= parser.ForeignKeyCascade || d&2 != 0 && f.UpdateAction >= parser.ForeignKeyCascade {
				modes[e.catalog.Domain.Ref(auth.Catalog.Epoch(), child.Address())] = lock.RowExclusive
			}
		}
	}
	e.mu.Unlock()
	slices.SortFunc(jobs, func(a, b foreignKeyJob) int { return cmp.Compare(a.Definition.OID, b.Definition.OID) })
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
	auth.Catalog = e.privateCatalogLocked(changes)
	auth.Recapture(auth.Catalog)
	e.mu.Unlock()
	for i := range jobs {
		job := &jobs[i]
		if err = RefreshForeignActionJob(auth.Catalog, job); err != nil {
			return nil, auth, err
		}
		job.Comparisons, err = table.BindForeignKeyComparison(auth.Catalog, job.Child, job.Parent, job.Index, job.Definition)
		if err != nil {
			return nil, auth, err
		}
		job.ChildKey = make([]sqltype.Value, len(job.Comparisons))
	}
	return jobs, auth, nil
}

func (s *StatementScope) settleForeignActions() error {
	if len(s.stage.rows) == 0 && len(s.stage.deletes) == 0 {
		return nil
	}
	jobs, auth, err := s.prepareForeignActions()
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		// An empty job list needs no slot: nil reads as no jobs.
		if s.foreignActionsSlot != nil {
			s.foreignActionsSlot.Jobs = jobs
		}
		return nil
	}
	s.foreignActions().Jobs = jobs
	e := s.engine()
	// Borrow the initiating predecessor/successor images before any action merge.
	for a, rows := range s.stage.rows {
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), a)
		image, err := s.captureForeignKeyRows(ref, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		for _, next := range rows {
			if index.UniqueRowDeleted(next.Row, s.stage.deletes[a]) {
				continue
			}
			old, ok := image.Previous(next.Row)
			if ok {
				s.foreignActions().Transitions = append(s.foreignActionsOrZero().Transitions, foreignActionTransition{Relation: a, Old: old, Next: next})
			} else if next.Base != 0 {
				return fmt.Errorf("stuntgres: missing foreign-key update predecessor")
			}
		}
	}
	for a, root := range s.stage.deletes {
		if root == nil {
			continue
		}
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), a)
		image, err := s.captureForeignKeyRows(ref, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		for id := range root.Rows {
			old, ok := image.Previous(id)
			if !ok {
				for _, row := range image.Staged {
					if row.Row == id {
						old, ok = row, true
						break
					}
				}
			}
			if ok {
				s.foreignActions().Transitions = append(s.foreignActionsOrZero().Transitions, foreignActionTransition{Relation: a, Old: old})
			}
		}
	}
	s.foreignActions().Settling = true
	defer func() { s.foreignActions().Settling = false; s.foreignActionAuth = nil }()
	for at := 0; at < len(s.foreignActionsOrZero().Transitions); at++ {
		event := s.foreignActionsOrZero().Transitions[at]
		if err = s.op.Context().Err(); err != nil {
			return err
		}
		for i := range jobs {
			job := &jobs[i]
			if job.Parent.Address() != event.Relation {
				continue
			}
			action := job.Definition.DeleteAction
			if event.Next.Row != 0 {
				if ForeignActionSameKey(job.Comparisons, event.Old.Values, event.Next.Values) {
					continue
				}
				action = job.Definition.UpdateAction
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
