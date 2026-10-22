package engine

import (
	"context"
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// UpdateInput is one UPDATE execution: the captured target Source, the FROM
// sources (nil without FROM or subquery demand), the canonical parameters and
// the target schema name. ExecuteUpdate closes Source and From.
type UpdateInput struct {
	Context    context.Context
	Binding    analyze.Binding
	Source     *exec.Source
	From       *exec.Source
	Parameters []sqltype.Value
	Schema     string
}

// ExecuteUpdate runs UPDATE after the root has captured the target and its
// sources and released Engine.mu. The candidate set is frozen first; each
// candidate then takes its row lock, rereads its latest version, rechecks the
// predicate when it moved, validates and stages its replacement.
func ExecuteUpdate(h dml.Host, in UpdateInput) (dml.Outcome, error) {
	ctx, b, source, from, parameters, schema := in.Context, in.Binding, in.Source, in.From, in.Parameters, in.Schema
	defer source.Close()
	source.Host = h
	_, lockedStorage := h.Target()
	if b.Storage == nil || b.Storage != lockedStorage || b.Storage != source.View.Data().GenerationAt(b.Ref.Address) {
		if from != nil {
			from.Close()
		}
		return dml.Outcome{}, ids.ErrCatalogReplaced
	}
	var positioned dml.MutationRowIdentity
	if b.CurrentOf != "" {
		var err error
		positioned, err = h.PositionedRow(b)
		if err != nil {
			if from != nil {
				from.Close()
			}
			return dml.Outcome{}, err
		}
	}
	inputs := source
	if from != nil {
		if err := exec.SelectQuerySources(ctx, from); err != nil {
			return dml.Outcome{}, err
		}
		if b.ViewWrite != nil || b.Rule.Template {
			from = view.FreezeWriteSources(from, b.QuerySources, h.QueryAuthority(), h)
		}
		defer from.Close()
		inputs = from
	}
	input, outputEnv := h.Environments()
	state := exec.State{Source: inputs, Parameters: parameters, Input: input, Output: outputEnv, Locking: inputs.Locks != nil}
	defer state.ReleaseMemberships()
	eval := state.Evaluation(ctx)
	var smallUpdateOutputs [4]sqltype.Value
	if b.Update.OutputWidth != 0 {
		if b.Update.OutputWidth <= len(smallUpdateOutputs) {
			eval.UpdateOutputScratch = smallUpdateOutputs[:b.Update.OutputWidth]
		} else {
			eval.UpdateOutputScratch = make([]sqltype.Value, b.Update.OutputWidth)
		}
	}
	var stage exec.Stage
	var frame []sqltype.Value
	var updated map[dml.MutationRowIdentity]bool
	sourceCount := 1
	if b.Update.From != nil {
		state.VirtualReads = exec.QueryHasVirtualReads(b.Update.From) || exec.BoundHasVirtualReads(b.Update.Predicate)
		for _, expression := range b.Update.Assignments {
			state.VirtualReads = state.VirtualReads || exec.BoundHasVirtualReads(expression)
		}
		for _, target := range b.Update.ArrayTargets {
			state.VirtualReads = state.VirtualReads || exec.AssignmentTargetHasVirtualReads(target)
		}
		if b.Returning != nil {
			for _, expression := range b.Returning.Expressions {
				state.VirtualReads = state.VirtualReads || exec.BoundHasVirtualReads(expression)
			}
		}
		var err error
		stage, err = exec.OpenNode(ctx, &state, b.Update.From)
		if err != nil {
			return dml.Outcome{}, err
		}
		stage, err = exec.SettlePendingQueryMarks(ctx, &state, b.Update.From, stage)
		if err != nil {
			return dml.Outcome{}, err
		}
		defer stage.Close()
		if err := stage.Drain(ctx); err != nil {
			return dml.Outcome{}, err
		}
		sourceCount = stage.Count()
		if sourceCount != 0 {
			frame = make([]sqltype.Value, len(b.Relation.Columns)+b.Update.SourceWidth)
			updated = make(map[dml.MutationRowIdentity]bool)
		}
	}
	var output *exec.FinalRows
	keep := false
	if b.Returning != nil {
		output = exec.NewFinalRows(b.Returning.Schema, 0)
		output.Output = outputEnv
		defer func() {
			if !keep {
				output.Close()
			}
		}()
	}
	// The candidate set is frozen before the first row wait. Rechecks can remove
	// candidates but never discover rows that did not qualify in this snapshot.
	var candidates []dml.UpdateCandidate
	for i := range source.Count() {
		physical := dml.MutationPhysicalRow(source, i, b)
		old := physical.Version
		if b.CurrentOf != "" && (positioned.Ref != physical.Leaf.Ref || old.Row != positioned.Row) {
			continue
		}
		for j := range sourceCount {
			if err := ctx.Err(); err != nil {
				return dml.Outcome{}, err
			}
			eval.Row = physical.Values(old)
			eval.PhysicalTable = physical.Leaf.Ref
			if b.Update.From != nil {
				copy(frame, eval.Row)
				copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(j)))
				eval.Row = frame
				eval.BindQueryIdentity(stage.Source, stage.Index(j))
			}
			matches, err := exec.UpdateMatches(&eval, b.Update.Predicate)
			if err != nil {
				return dml.Outcome{}, err
			}
			if matches {
				candidates = append(candidates, dml.UpdateCandidate{RowVersion: old, Slot: physical.Hint, SourceIndex: j, Physical: physical})
			}
		}
	}
	type physicalBatch struct {
		ref    ids.ObjectRef
		old    []storage.RowVersion
		rows   index.WriteBatch
		staged int
		prior  map[ids.RowID]struct{}
	}
	var single physicalBatch
	single.ref = b.Ref
	command := h.Command()
	if len(command.InheritedTargets) == 0 {
		capacity := min(len(candidates), source.Count())
		single.old = make([]storage.RowVersion, 0, capacity)
		single.rows.Values = make([][]sqltype.Value, 0, capacity)
	}
	batches := []physicalBatch(nil)
	if len(command.InheritedTargets) != 0 && len(candidates) != 0 {
		batches = make([]physicalBatch, len(command.InheritedTargets)+1)
		batches[0].ref = b.Ref
		for i, target := range command.InheritedTargets {
			batches[i+1].ref = target.Ref
		}
		for i := range batches {
			capacity := min(len(candidates), source.Inheritance[i].Prefix.Count())
			batches[i].old = make([]storage.RowVersion, 0, capacity)
			batches[i].rows.Values = make([][]sqltype.Value, 0, capacity)
		}
	}
	var count int64
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return dml.Outcome{}, err
		}
		ref := candidate.Physical.Leaf.Ref
		if err := h.AcquireUpdateRow(ref, candidate.Row, lock.ForNoKeyUpdate); err != nil {
			return dml.Outcome{}, err
		}
		latest, err := h.LatestRow(dml.UpdateRowRequest(ref, candidate))
		old, live := latest.Version, latest.Live
		if err != nil {
			return dml.Outcome{}, err
		}
		if !live {
			continue
		}
		oldValues := candidate.Physical.Values(old)
		eval.Row = oldValues
		eval.PhysicalTable = ref
		eval.OldRow = nil
		if b.Update.From != nil {
			copy(frame, oldValues)
			copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(candidate.SourceIndex)))
			eval.Row = frame
			eval.BindQueryIdentity(stage.Source, stage.Index(candidate.SourceIndex))
		}
		if old.Version != candidate.Version {
			if err := h.PhysicalRowEvent(ref, old, lock.MultiNoKeyLock, 0); err != nil {
				return dml.Outcome{}, err
			}
			matches, err := exec.UpdateMatches(&eval, b.Update.Predicate)
			if err != nil {
				return dml.Outcome{}, err
			}
			if !matches {
				continue
			}
		}
		// RHS and bounds see the unchanged old frame; partial writes compose
		// only in this private replacement. Losing FROM pairs still demand SET.
		row, err := dml.EvaluateUpdateAssignments(&eval, b.Update, b.Targets, oldValues)
		if err != nil {
			return dml.Outcome{}, err
		}
		identity := dml.MutationRowIdentity{Ref: ref, Row: candidate.Row}
		if b.Update.From != nil && updated[identity] {
			continue
		}
		physicalRow := dml.ScatterMutationRow(candidate.Physical, old, row, b.Targets)
		if err := validateInheritedMutationRow(h, b, candidate.Physical, schema, physicalRow, &eval); err != nil {
			return dml.Outcome{}, err
		}
		keyChanged, err := dml.UniqueKeyChanged(h, ref, old.Values, physicalRow)
		if err != nil {
			return dml.Outcome{}, err
		}
		if keyChanged {
			if err := h.AcquireUpdateRow(ref, old.Row, lock.ForUpdate); err != nil {
				return dml.Outcome{}, err
			}
			if latest, err := h.LatestRow(dml.UpdateRowRequest(ref, candidate)); err != nil {
				return dml.Outcome{}, err
			} else if !latest.Live {
				continue
			}
		}
		if err := dml.ClaimUniqueRow(h, ref, old); err != nil {
			return dml.Outcome{}, err
		}
		old.PendingKeyChange = old.PendingKeyChange || keyChanged
		batch := &single
		for i := range batches {
			if batches[i].ref == ref {
				batch = &batches[i]
				break
			}
		}
		batch.old = append(batch.old, old)
		actionMode := lock.MultiNoKeyAction
		if keyChanged {
			actionMode = lock.MultiUpdateAction
		}
		if err := h.PhysicalRowEvent(ref, old, actionMode, 0); err != nil {
			return dml.Outcome{}, err
		}
		proposal := index.WriteCandidate{Values: physicalRow}
		if err := dml.CheckUniqueRow(h, ref, &proposal, batch.old, batch.rows); err != nil {
			return dml.Outcome{}, err
		}
		if err := dml.CheckViewRow(h, &b, row, &eval); err != nil {
			return dml.Outcome{}, err
		}
		if candidate.Physical.Segment != nil && candidate.Physical.Segment.Layout != nil {
			for i, slot := range candidate.Physical.Segment.Layout {
				row[i] = physicalRow[slot]
			}
		}
		batch.rows.Append(proposal)
		one := batch.rows.Slice(len(batch.old)-1, len(batch.old))
		one.ActionMode = actionMode
		// Other action producers can have staged this physical target already.
		// Index only their prefix/additions; our accepted targets are distinct
		// and appending never mutates a prefix retained by an earlier view.
		existing := h.PrivateRows(ref.Address).Staged
		if len(existing) < batch.staged {
			batch.staged = 0
		}
		for _, predecessor := range existing[batch.staged:] {
			if batch.prior == nil {
				batch.prior = make(map[ids.RowID]struct{})
			}
			batch.prior[predecessor.Row] = struct{}{}
		}
		_, overlap := batch.prior[old.Row]
		if overlap {
			err = dml.ReplaceRows(h, ref, batch.old[len(batch.old)-1:], one, dml.ReplaceMerge)
		} else {
			err = dml.ReplaceRows(h, ref, batch.old[len(batch.old)-1:], one, dml.ReplaceAppend)
		}
		if err != nil {
			return dml.Outcome{}, err
		}
		batch.staged = len(h.PrivateRows(ref.Address).Staged)
		if output != nil {
			eval.Row, eval.OldRow = row, oldValues
			if b.Update.From != nil {
				copy(frame, row)
				eval.Row = frame
			}
			for i, expr := range b.Returning.Expressions {
				value, err := expr.Eval(&eval)
				if err != nil {
					return dml.Outcome{}, err
				}
				if value.Type() == nil || !value.Type().Concrete() || value.Type() != output.Columns[i].Usage.Type {
					return dml.Outcome{}, errors.New("stuntgres: invalid finalized UPDATE value")
				}
				output.Values = append(output.Values, value)
			}
			output.Count++
		}
		count++
		if b.Update.From != nil {
			updated[identity] = true
		}
	}
	if output != nil && b.Returning.RegclassOutput && output.Count != 0 {
		if err := h.CaptureRegclassOutput(output); err != nil {
			return dml.Outcome{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return dml.Outcome{}, err
	}
	keep = true
	if output != nil {
		return dml.Outcome{Count: count, Rows: output}, nil
	}
	return dml.Outcome{Count: count}, nil
}
