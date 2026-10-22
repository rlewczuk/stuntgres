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

// PrepareForeignActions captures the FK jobs the staged rows reach and the
// authority they run with, takes each job's relation locks in OID order, then
// recaptures the authority and binds every job's comparisons over the current
// catalog. Without a reached job it takes no lock.
func PrepareForeignActions(h dml.FinishHost) ([]dml.ForeignKeyJob, dml.Authority, error) {
	jobs, modes, auth, err := h.ForeignKeyJobs()
	if err != nil || jobs == nil {
		return nil, auth, err
	}
	slices.SortFunc(jobs, func(a, b dml.ForeignKeyJob) int { return cmp.Compare(a.Definition.OID, b.Definition.OID) })
	refs := make([]ids.ObjectRef, 0, len(modes))
	for ref := range modes {
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(a, b ids.ObjectRef) int { return cmp.Compare(a.Address.ObjectID, b.Address.ObjectID) })
	for _, ref := range refs {
		if err = h.GrantForeignKey(ref, 0, modes[ref], nil); err != nil {
			return nil, auth, err
		}
	}
	auth = h.RecaptureAuthority(auth)
	view := auth.Access().Catalog
	for i := range jobs {
		job := &jobs[i]
		if err = dml.RefreshForeignActionJob(view, job); err != nil {
			return nil, auth, err
		}
		job.Comparisons, err = table.BindForeignKeyComparison(view, job.Child, job.Parent, job.Index, job.Definition)
		if err != nil {
			return nil, auth, err
		}
		job.ChildKey = make([]sqltype.Value, len(job.Comparisons))
	}
	return jobs, auth, nil
}

// SettleForeignActions runs the referential actions of the statement's staged
// parent transitions, including the transitions the actions themselves stage,
// as the referencing tables' owners. Finish calls it after the first
// exclusion event check.
func SettleForeignActions(h dml.FinishHost) error {
	rows, deletes := h.Staged()
	if len(rows) == 0 && len(deletes) == 0 {
		return nil
	}
	jobs, auth, err := PrepareForeignActions(h)
	if err != nil {
		return err
	}
	if len(jobs) == 0 {
		// An empty job list needs no slot: nil reads as no jobs.
		if slot := h.ForeignActions(false); slot != nil {
			slot.Jobs = jobs
		}
		return nil
	}
	scope := h.ForeignActions(true)
	scope.Jobs = jobs
	view := auth.Access().Catalog
	// Borrow the initiating predecessor/successor images before any action merge.
	for a, staged := range rows {
		ref := view.Domain().Ref(view.Epoch(), a)
		image, err := h.ForeignKeyRows(ref, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		for _, next := range staged {
			if index.UniqueRowDeleted(next.Row, deletes[a]) {
				continue
			}
			old, ok := image.Previous(next.Row)
			if ok {
				scope.Transitions = append(scope.Transitions, dml.ForeignActionTransition{Relation: a, Old: old, Next: next})
			} else if next.Base != 0 {
				return fmt.Errorf("stuntgres: missing foreign-key update predecessor")
			}
		}
	}
	for a, root := range deletes {
		if root == nil {
			continue
		}
		ref := view.Domain().Ref(view.Epoch(), a)
		image, err := h.ForeignKeyRows(ref, ids.ObjectAddress{})
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
				scope.Transitions = append(scope.Transitions, dml.ForeignActionTransition{Relation: a, Old: old})
			}
		}
	}
	scope.Settling = true
	defer func() { scope.Settling = false; h.SwapForeignActionAuthority(nil) }()
	for at := 0; at < len(scope.Transitions); at++ {
		event := scope.Transitions[at]
		if err = h.Context().Err(); err != nil {
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
			if err = applyForeignAction(h, auth, job, event, action); err != nil {
				return err
			}
		}
	}
	return nil
}
