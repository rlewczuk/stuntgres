package dml

import (
	"cmp"
	"context"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// PrepareForeignActions captures the FK jobs the staged rows reach and the
// authority they run with, takes each job's relation locks in OID order, then
// recaptures the authority and binds every job's comparisons over the current
// catalog. Without a reached job it takes no lock.
func PrepareForeignActions(h FinishHost) ([]ForeignKeyJob, Authority, error) {
	jobs, modes, auth, err := h.ForeignKeyJobs()
	if err != nil || jobs == nil {
		return nil, auth, err
	}
	slices.SortFunc(jobs, func(a, b ForeignKeyJob) int { return cmp.Compare(a.Definition.OID, b.Definition.OID) })
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
		if err = RefreshForeignActionJob(view, job); err != nil {
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
// as the referencing tables' owners. Command completion calls it after the
// first owned exclusion-event check.
func SettleForeignActions(ctx context.Context, h FinishHost) error {
	scope := h.ForeignActions(false)
	if scope == nil {
		return nil
	}
	pending := false
	for _, event := range scope.Transitions {
		if event.Command == scope.Command && !event.Checked {
			pending = true
			break
		}
	}
	if !pending {
		return nil
	}
	jobs, auth, err := PrepareForeignActions(h)
	if err != nil {
		return err
	}
	scope.Jobs = jobs
	if len(jobs) == 0 {
		// No RI work means no new execution/cancellation phase. Preserve the
		// root's existing final publication fence while consuming bookkeeping.
		for i := range scope.Transitions {
			if scope.Transitions[i].Command == scope.Command {
				scope.Transitions[i].ActionsDone = true
			}
		}
		return nil
	}
	previousSettling := scope.Settling
	scope.Settling = true
	defer func() { scope.Settling = previousSettling }()
	// RI actions append further events; the loop intentionally observes growth.
	for at := 0; at < len(scope.Transitions); at++ {
		event := scope.Transitions[at]
		if event.Command != scope.Command || event.ActionsDone {
			continue
		}
		if err = h.Context().Err(); err != nil {
			return err
		}
		if event.Old.Row != 0 {
			for i := range jobs {
				job := &jobs[i]
				if job.Parent.Address() != event.Relation {
					continue
				}
				action := job.Definition.DeleteAction
				actionSlot := 0
				if event.Next.Row != 0 {
					if ForeignActionSameKey(job.Comparisons, event.Old.Values, event.Next.Values) {
						continue
					}
					action = job.Definition.UpdateAction
					actionSlot = 1
				}
				if !event.TriggerFires(job.Definition.OID, actionSlot) {
					continue
				}
				if action == parser.ForeignKeyNoAction {
					continue
				}
				if err = applyForeignAction(ctx, h, auth, job, event, action); err != nil {
					return err
				}
			}
		}
		scope.Transitions[at].ActionsDone = true
	}
	return nil
}
