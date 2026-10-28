package dml

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// CheckForeignKeys checks the statement's FK jobs (prepared by
// SettleForeignActions) after the actions: every newly written child row needs
// a parent, and every changed or deleted parent key must leave no referencing
// child. A child or parent that an action mutated reports as its owner.
// Command completion calls it last, after actions and their exclusion events.
func CheckForeignKeys(h FinishHost) (err error) {
	slot := h.ForeignActions(false)
	if slot == nil {
		return nil
	}
	defer func() {
		if err == nil {
			for i := range slot.Transitions {
				if slot.Transitions[i].Command == slot.Command {
					slot.Transitions[i].Checked = true
				}
			}
		}
	}()
	if len(slot.Jobs) == 0 {
		return nil
	}
	jobs := slot.Jobs
	auth, err := h.ForeignKeyAuthority()
	if err != nil {
		return err
	}
	view := auth.Access().Catalog
	input, output := h.Environments()
	for i := range jobs {
		job := &jobs[i]
		if err = RefreshForeignActionJob(view, job); err != nil {
			return err
		}
		f := job.Definition
		childAuth, parentAuth := auth, auth
		if admission := slot.Admissions[job.Child.Address()]; admission != nil && admission.Mutated {
			childAuth = auth.ActingAs(admission.Relation.Owner)
		}
		if admission := slot.Admissions[job.Parent.Address()]; admission != nil && admission.Mutated {
			parentAuth = auth.ActingAs(admission.Relation.Owner)
		}
		childRef := view.Domain().Ref(view.Epoch(), job.Child.Address())
		parentRef := view.Domain().Ref(view.Epoch(), f.Target)
		parentRows, err := h.ForeignKeyRows(parentRef, f.Index)
		if err != nil {
			return err
		}
		children, err := h.ForeignKeyRows(childRef, ids.ObjectAddress{})
		if err != nil {
			return err
		}
		// Child checks only newly written final rows, not unaffected old rows.
		staged := h.PrivateRows(childRef.Address)
		for _, event := range slot.Transitions {
			if event.Command != slot.Command || event.Checked || event.Relation != childRef.Address || event.Next.Row == 0 {
				continue
			}
			checkSlot := 2
			if event.Old.Row != 0 {
				checkSlot = 3
			}
			if !event.TriggerFires(f.OID, checkSlot) {
				continue
			}
			row := event.Next
			final, _, found := storage.RowByID(staged.Staged, row.Row, -1)
			if !found || final.Version != row.Version {
				continue
			}
			if root := staged.StagedDeletes; root != nil {
				if _, deleted := root.Rows[row.Row]; deleted {
					continue
				}
			}
			any, all := table.ForeignKeyNullState(job.Comparisons, row.Values)
			if any {
				if f.Full && !all {
					return table.ForeignKeyDiagnostic(h.Context(), childAuth.Access(), childAuth, job.Child, job.Parent, f, row.Values, false, true, output)
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
					for _, comparison := range job.Comparisons {
						equal, err := comparison.Comparison.EqualChildren(h.Context(), previous.Values[comparison.Child], row.Values[comparison.Child], input)
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
			if err := checkForeignKeyChild(h, childAuth, job, parentRef, &parentRows, row); err != nil {
				return err
			}
		}
		for _, event := range slot.Transitions {
			if event.Command != slot.Command || event.Checked || event.Relation != parentRef.Address || event.Old.Row == 0 {
				continue
			}
			actionSlot := 0
			if event.Next.Row != 0 {
				actionSlot = 1
			}
			if !event.TriggerFires(f.OID, actionSlot) {
				continue
			}
			if event.Next.Row != 0 && ForeignActionSameKey(job.Comparisons, event.Old.Values, event.Next.Values) {
				continue
			}
			action := f.DeleteAction
			if event.Next.Row != 0 {
				action = f.UpdateAction
			}
			if err := checkForeignKeyParent(h, parentAuth, job, parentRef, &parentRows, children, event.Old, action == parser.ForeignKeyNoAction); err != nil {
				return err
			}
		}
	}
	return h.Context().Err()
}

// Admission (mode, final version, NULL and unchanged key) precedes invocation.
func checkForeignKeyChild(h FinishHost, auth Authority, job *ForeignKeyJob, parentRef ids.ObjectRef, parents *table.ForeignKeyRows, row storage.RowVersion) error {
	h.EnterForeignKeyTrigger()
	defer h.LeaveForeignKeyTrigger()
	found, err := foreignKeyParentExists(h, parentRef, *job, parents, row.Values, true)
	if err != nil {
		return err
	}
	if !found {
		_, output := h.Environments()
		return table.ForeignKeyDiagnostic(h.Context(), auth.Access(), auth, job.Child, job.Parent, job.Definition, row.Values, false, false, output)
	}
	return nil
}

// Other actions already ran their actual callback in applyForeignAction. Their
// later validation must not invent an additional invocation.
func checkForeignKeyParent(h FinishHost, auth Authority, job *ForeignKeyJob, parentRef ids.ObjectRef, parents *table.ForeignKeyRows, children table.ForeignKeyRows, old storage.RowVersion, invoke bool) error {
	if invoke {
		h.EnterForeignKeyTrigger()
		defer h.LeaveForeignKeyTrigger()
	}
	input, output := h.Environments()
	return children.Each(func(child storage.RowVersion) (bool, error) {
		match, err := table.ForeignKeyRowsMatch(h.Context(), job.Comparisons, child.Values, old.Values, input)
		if err != nil || !match {
			return false, err
		}
		found, err := foreignKeyParentExists(h, parentRef, *job, parents, child.Values, false)
		if err != nil || found {
			return false, err
		}
		return false, table.ForeignKeyDiagnostic(h.Context(), auth.Access(), auth, job.Child, job.Parent, job.Definition, old.Values, true, false, output)
	})
}

// foreignKeyParentExists probes rows' FK index for child's parent key. With
// lockParent it takes a key-share grant on an uncommitted-by-us match and
// rechecks the exact version the index named, retrying on the current index.
func foreignKeyParentExists(h FinishHost, ref ids.ObjectRef, job ForeignKeyJob, rows *table.ForeignKeyRows, child []sqltype.Value, lockParent bool) (bool, error) {
	input, _ := h.Environments()
	if err := table.PrepareForeignKeyChild(h.Context(), job.Comparisons, child, job.ChildKey, input); err != nil {
		return false, err
	}
	for {
		matching, locator, err := table.FindForeignKeyParent(h.Context(), job.Index, job.Comparisons, *rows, job.ChildKey, input)
		if err != nil || matching.Row == 0 {
			return false, err
		}
		if !lockParent || matching.Born == 0 {
			return true, nil
		}
		if err := h.GrantForeignKey(ref, matching.Row, lock.ForKeyShare, rows.Storage); err != nil {
			return false, err
		}
		if err := h.RefreshForeignKeyRows(ref, rows); err != nil {
			return false, err
		}
		// The borrowed index entry is an exact physical-version locator. Waiting
		// may end this version; retry the current index, never scan the relation.
		if locator.Position >= 0 && locator.Position < len(rows.Committed) {
			parent := rows.Committed[locator.Position]
			if parent.Row == locator.Row && parent.Version == locator.Version && rows.CommittedLive(parent) {
				if err := h.PhysicalRowEvent(ref, parent, lock.MultiKeyShare, 0); err != nil {
					return false, err
				}
				return table.ForeignKeyRowsMatch(h.Context(), job.Comparisons, child, parent.Values, input)
			}
		}
	}
}
