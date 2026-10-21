package dml

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

// AdmitInsertConflictRow admits candidate under ON CONFLICT DO NOTHING: it
// reports false (and releases the row's claims) when an arbiter conflicts,
// and fails with the violation of any other index. It may wait; the caller
// holds nothing.
func AdmitInsertConflictRow(h Host, ref ids.ObjectRef, candidate *index.WriteCandidate, prefix index.WriteBatch, purpose index.ProbePurpose) (bool, error) {
	c := h.Command()
	action := c.InsertConflict
	journal := h.UniqueJournal()
	if action.StatementKeys == nil {
		action.StatementKeys = journal.Checkpoint(nil)
	}
	action.Checkpoint = journal.Checkpoint(action.Checkpoint)
	for {
		conflict, err := ProbeUniqueRow(h, ref, candidate, nil, prefix, action, purpose)
		if err != nil {
			return false, err
		}
		if conflict.Index != nil {
			if err := InsertConflictVisible(h, conflict.Row, conflict.Committed); err != nil {
				return false, err
			}
			journal.Restore(action.Checkpoint)
			DiscardExclusion(h, candidate)
			return false, nil
		}
		other, err := ProbeUniqueRow(h, ref, candidate, nil, prefix, nil, purpose)
		if err != nil {
			return false, err
		}
		// A provider publication during the unselected probe invalidates the first
		// pass; recapture selected arbiters before treating any hit as a violation.
		if c.UniqueBinding != nil && action.Catalog != c.UniqueBinding.Catalog {
			continue
		}
		if other.Index != nil {
			return false, other.Diagnostic(h, ref)
		}
		return true, nil
	}
}

// InsertConflictVisible fails with a serialization error when a committed
// conflicting row is newer than a stable snapshot.
func InsertConflictVisible(h Host, row storage.RowVersion, committed bool) error {
	if !committed {
		return nil
	}
	if stable, frontier := h.Snapshot(); stable && row.Born > frontier {
		return sqlError("40001", "could not serialize access due to concurrent update")
	}
	return nil
}

func revalidateConflictRows(h Host, ref ids.ObjectRef, rows index.WriteBatch) (index.WriteBatch, error) {
	c := h.Command()
	prior := c.UniqueBinding
	h.UniqueJournal().Restore(c.InsertConflict.StatementKeys)
	c.UniqueBinding = nil
	binding, err := BindUniqueWrite(h, ref)
	if err != nil {
		return index.WriteBatch{}, err
	}
	purpose := index.ProbeRuntime
	if binding.Checked || prior != nil && prior.Checked {
		purpose = index.ProbeRevalidation
		if prior != nil && !index.SameRelationRowShape(prior.Parent, binding.Parent) {
			return index.WriteBatch{}, ids.ErrCatalogReplaced
		}
	}
	input, output := h.Environments()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: h.Context(), Input: input, Output: output}}
	if err := c.InsertConflict.SelectArbiters(&evaluation, binding.Parent, h.CatalogRead(binding.Catalog)); err != nil {
		return index.WriteBatch{}, err
	}
	accepted := rows.Prefix(0)
	for i := range rows.Values {
		row := rows.Candidate(i)
		admitted, err := AdmitInsertConflictRow(h, ref, &row, accepted, purpose)
		if err != nil {
			return index.WriteBatch{}, err
		}
		if admitted {
			accepted.Append(row)
		}
	}
	clear(rows.Values[len(accepted.Values):])
	if rows.States != nil {
		clear(rows.States[len(accepted.Values):])
	}
	return accepted, nil
}
