package engine

import (
	"cmp"
	"fmt"
	"slices"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) prepareForeignActions() ([]dml.ForeignKeyJob, AuthorizationView, error) {
	e := s.engine()
	jobs, modes, authority, err := s.writeHost().ForeignKeyJobs()
	if err != nil {
		return nil, AuthorizationView{}, err
	}
	auth := *foreignAuthorizationOf(authority)
	if jobs == nil {
		return nil, auth, nil
	}
	changes := s.foreignKeyChanges()
	slices.SortFunc(jobs, func(a, b dml.ForeignKeyJob) int { return cmp.Compare(a.Definition.OID, b.Definition.OID) })
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
		if err = dml.RefreshForeignActionJob(auth.Catalog, job); err != nil {
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
				s.foreignActions().Transitions = append(s.foreignActionsOrZero().Transitions, dml.ForeignActionTransition{Relation: a, Old: old, Next: next})
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
				s.foreignActions().Transitions = append(s.foreignActionsOrZero().Transitions, dml.ForeignActionTransition{Relation: a, Old: old})
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
				if dml.ForeignActionSameKey(job.Comparisons, event.Old.Values, event.Next.Values) {
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
