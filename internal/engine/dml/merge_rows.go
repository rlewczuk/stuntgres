package dml

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// StageMergeRows owns the explicit acceptance boundary after source completion.
// Its embedded producer consumes only changed candidates; index staleness stays
// within ReplaceRows. No ordinary payload/context copying is needed.
func StageMergeRows(ctx context.Context, h Host, rows *MergeRows, ref ids.ObjectRef) error {
	if rows == nil {
		return errors.New("stuntgres: missing MERGE producer")
	}
	if rows.producer.owner != nil && rows.producer.owner != rows {
		return errors.New("stuntgres: copied MERGE producer")
	}
	defer rows.ReleaseProducer()
	for {
		failed, err := stageMergeWindow(ctx, h, rows, ref)
		if !errors.Is(err, ErrTupleRecheck) {
			return err
		}
		if rows.producer.owner != rows || failed < rows.StagedActions || failed >= len(rows.Actions) {
			return errors.New("stuntgres: invalid MERGE producer recheck")
		}
		if err := rows.producer.recheck(ctx, h, rows, failed); err != nil {
			return err
		}
	}
}

func stageMergeWindow(ctx context.Context, h Host, rows *MergeRows, ref ids.ObjectRef) (int, error) {
	for start := rows.StagedActions; start < len(rows.Actions); {
		action := rows.Actions[start]
		if action.Action == parser.MergeDeleteAction {
			expected := storage.RowVersion{Row: action.Deletion.Row, Version: action.Expected}
			if _, err := h.Delete(action.Deletion, expected); err != nil {
				return start, err
			}
			start++
			rows.StagedActions = start
			continue
		}
		end := start + 1
		for end < len(rows.Actions) && rows.Actions[end].Action == action.Action && rows.Actions[end].UpdateMode == action.UpdateMode && rows.Actions[end].Payload == rows.Actions[end-1].Payload+1 {
			end++
		}
		first, last := action.Payload, rows.Actions[end-1].Payload+1
		if action.Action == parser.MergeUpdateAction {
			batch := rows.WriteBatch.Slice(first, last)
			batch.ActionMode = action.UpdateMode
			failed, err := ReplaceRows(ctx, h, ref, rows.Predecessors[first:last], batch, ReplaceMerge)
			if err != nil {
				if errors.Is(err, ErrTupleRecheck) {
					return start + failed, err
				}
				return -1, err
			}
		} else if err := ApplyValidated(ctx, h, ref, rows.WriteBatch.Slice(first, last)); err != nil {
			return -1, err
		}
		start = end
		rows.StagedActions = start
	}
	return -1, nil
}

// Source completion and ON membership precede every action wait. Match marks
// refer to captured occurrences, preserving equal values and source provenance.
func AssembleMergeRows(h Host, in MergeInput, cursor exec.RowCursor, state *exec.State, unique bool) (*MergeRows, error) {
	ctx, b, target := in.Context, in.Binding, in.Target
	target.Host = h
	roles := exec.DMLObservationOf(ctx)
	candidates := roles.Role(exec.DMLMergeCandidates)
	targetPass := roles.Role(exec.DMLMergeTarget)
	candidates.BeginLoop()
	candidates.StartWork()
	membershipComplete := false
	defer func() {
		if !membershipComplete {
			candidates.EndWork()
		}
	}()
	sourceSchema := cursor.Schema()
	width := len(sourceSchema)
	scratch := make([]sqltype.Value, width)
	var sources []sqltype.Value
	var occurrences, provenance []int
	// The borrowed stage closes before actions, but ExecuteMerge's input owner
	// keeps the source namespace and any action-subquery leaves live until exit.
	var stage *exec.Stage
	if state.IdentityReads {
		var ok bool
		stage, ok = cursor.(*exec.Stage)
		if !ok {
			cursor.Close()
			return nil, errors.New("stuntgres: invalid MERGE source identity")
		}
	}
	sourceCount := 0
	var presenceSlots []int
	var presenceCount int
	var presence []bool
	var projected [][]storage.RowVersion
	var raw []map[ids.VersionID]uint32
	for {
		present, err := cursor.Next(ctx, scratch)
		if err != nil {
			cursor.Close()
			return nil, err
		}
		if !present {
			break
		}
		if err := ctx.Err(); err != nil {
			cursor.Close()
			return nil, err
		}
		sources = append(sources, scratch...)
		if state.IdentityReads {
			if sourceCount == 0 {
				occurrences = exec.AppendQueryOccurrences(nil, stage.Source)
			}
			provenance = exec.AppendQueryProvenance(provenance, stage.Source, stage.Index(stage.Position-1))
			versions := exec.ProjectedQueryVersions(stage.Source, stage.Index(stage.Position-1))
			if versions != nil && projected == nil {
				projected = make([][]storage.RowVersion, sourceCount)
			}
			if projected != nil {
				projected = append(projected, versions)
			}
			capture := state.Source.RawXmax
			if locks := state.Source.Locks; locks != nil && locks.RawXmax != nil {
				capture = locks.RawXmax
			}
			if capture != nil && raw == nil {
				raw = make([]map[ids.VersionID]uint32, sourceCount)
			}
			if raw != nil {
				raw = append(raw, capture)
			}
		}
		if stage, ok := cursor.(*exec.Stage); ok && stage.Source.PresenceCount != 0 {
			presenceSlots, presenceCount = stage.Source.PresenceSlots, stage.Source.PresenceCount
			presence = append(presence, stage.Source.PresenceRow(stage.Index(stage.Position-1))...)
		}
		sourceCount++
	}
	if err := cursor.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	frame := make([]sqltype.Value, len(b.Relation.Columns)+width)
	evaluation := state.Evaluation(ctx)
	evaluation.PhysicalRawXmax = target.RawXmax
	evaluation.QueryRawXmax = state.Source.RawXmax
	evaluation.Row = frame
	evaluation.PresenceSlots = presenceSlots
	evaluation.QueryOccurrences = occurrences
	bindIdentity := func(index int) {
		if projected != nil {
			evaluation.QueryProjectedVersions = projected[index]
		}
		if raw != nil {
			evaluation.QueryRawXmax = raw[index]
		}
		if state.IdentityReads {
			width := len(occurrences)
			evaluation.QueryProvenance = provenance[index*width : (index+1)*width]
		}
	}
	var pairs []MergeCandidate
	var targetMatched []bool
	if b.Merge.HasBySource {
		targetMatched = make([]bool, target.Count())
	}
	capture := func(i int, class parser.MergeMatch, source int) MergeCandidate {
		slot := target.RawIndex(i)
		var old storage.RowVersion
		if sourceCount == 0 {
			old = target.ReadSequentialVersion(i)
			exec.ObserveLogicalAccess(ctx)
			targetPass.Emit()
		} else {
			old = target.VersionAt(slot) // Already visited candidate metadata.
		}
		deletion := visibility.DeleteCandidate{Ref: b.Ref, Storage: b.Storage, Row: old.Row, Version: old.Version, Ordinal: slot, RepairStamp: old.RepairStamp}
		if slot >= len(target.Committed) && !old.ForcedFrozen {
			deletion.Own = true
			deletion.Ordinal -= len(target.Committed)
		}
		return MergeCandidate{UpdateCandidate: UpdateCandidate{RowVersion: old, Slot: slot, SourceIndex: source}, Class: class, Deletion: deletion}
	}
	for j := range sourceCount {
		if presenceCount != 0 {
			evaluation.Presence = presence[j*presenceCount : (j+1)*presenceCount]
		}
		copy(frame[len(b.Relation.Columns):], sources[j*width:(j+1)*width])
		bindIdentity(j)
		matched := false
		targetPass.BeginLoop()
		targetPass.StartWork()
		for i := range target.Count() {
			old := target.ReadSequentialVersion(i)
			exec.ObserveLogicalAccess(ctx)
			targetPass.Emit()
			copy(frame, old.Values)
			evaluation.PhysicalTable, evaluation.PhysicalVersion = b.Ref, old
			truth, err := MergeMatches(&evaluation, b.Merge.On)
			if err != nil {
				targetPass.EndWork()
				return nil, err
			}
			if truth {
				pairs = append(pairs, capture(i, parser.MergeMatched, j))
				candidates.Emit()
				matched = true
				if targetMatched != nil {
					targetMatched[i] = true
				}
			}
		}
		targetPass.EndWork()
		if !matched && b.Merge.HasByTarget {
			pairs = append(pairs, MergeCandidate{UpdateCandidate: UpdateCandidate{SourceIndex: j}, Class: parser.MergeNotMatched})
			candidates.Emit()
		}
	}
	if sourceCount == 0 && b.Merge.HasBySource {
		targetPass.BeginLoop()
		targetPass.StartWork()
	}
	for i, matched := range targetMatched {
		if !matched {
			pairs = append(pairs, capture(i, parser.MergeNotMatchedBySource, -1))
			candidates.Emit()
		}
	}
	if sourceCount == 0 && b.Merge.HasBySource {
		targetPass.EndWork()
	}
	candidates.EndWork()
	membershipComplete = true
	var nullSource []sqltype.Value
	if b.Merge.HasBySource && (b.Merge.HasInsert || b.Returning != nil) {
		nullSource = make([]sqltype.Value, width)
		for i, column := range sourceSchema {
			nullSource[i] = sqltype.Null(column.Usage.Type)
		}
	}
	result := &MergeRows{}
	_, outputEnv := h.Environments()
	output := newMergeReturningOutput(b, outputEnv)
	physicalReturning := returningHasPhysicalHeaders(b.Returning)
	if physicalReturning {
		result.Accepted = make([]storage.RowVersion, 0)
	}
	if output != nil {
		result.Returning = output.Rows
	}
	complete := false
	defer func() {
		if !complete {
			result.ReleaseProducer()
		}
		if !complete && result.Returning != nil {
			result.Returning.Close()
		}
	}()
	var accepted map[ids.VersionID]bool
	if len(b.Merge.UpdateTargets) != 0 || b.Merge.HasDelete {
		accepted = make(map[ids.VersionID]bool)
	}
	result.producer = mergeProducer{
		owner: result, binding: b, catalog: in.Catalog, schema: in.Schema,
		targetRaw: target.RawXmax, state: state, pairs: pairs,
		projected: projected, raw: raw,
		sources: sources, nullSource: nullSource, frame: frame, width: width,
		occurrences: occurrences, provenance: provenance, presenceSlots: presenceSlots,
		presence: presence, presenceCount: presenceCount,
		unique: unique, physicalReturning: physicalReturning, output: output, accepted: accepted,
	}
	for i := range pairs {
		if err := result.producer.producePair(ctx, h, result, output, i, false, true); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result.producer.evaluation = exec.Context{}
	complete = true
	return result, nil
}

func appendMergeAction(h Host, b analyze.Binding, result *MergeRows, pending *index.WriteBatch, evaluation *exec.Context, schema string, unique bool, arm *analyze.MergeArm, old storage.RowVersion, deletion visibility.DeleteCandidate, projected []sqltype.Value, accepted map[ids.VersionID]bool, excluded *[]storage.RowVersion) error {
	if arm.Action == parser.MergeDeleteAction {
		if HasRowTriggers(b.Relation, stored.TriggerDelete, stored.TriggerBefore) {
			if err := h.PhysicalRowLock(b.Ref, old, lock.ForUpdate); err != nil {
				return err
			}
		}
		before, failure := Before(h, b.Relation, BeforeRow{Relation: b.Ref, Event: BeforeDelete, Old: old.Values})
		if failure != nil {
			return failure
		}
		if !before.Proceed {
			return nil
		}
		if mergeAlreadyAccepted(accepted, old) {
			return MergeCardinalityError()
		}
		// Initial DELETE owns rowUpdate before following a predecessor; an
		// initial UPDATE restarts under the hasDelete strong-mode union.
		if err := ClaimUniqueRow(evaluation.Context, h, b.Ref, old); err != nil {
			return err
		}
		*excluded = append(*excluded, old)
		accepted[old.Version] = true
		result.Actions = append(result.Actions, MergeActionRow{Action: arm.Action, Deletion: deletion, Expected: old.Version})
		return nil
	}
	row := projected
	rowContext := *evaluation
	command := h.Command()
	command.RLS.Proposal = command.RLS.MergeInsert
	if arm.Action == parser.MergeUpdateAction {
		command.RLS.Proposal = command.RLS.MergeUpdate
	}
	if arm.Action == parser.MergeUpdateAction {
		if HasRowTriggers(b.Relation, stored.TriggerUpdate, stored.TriggerBefore) {
			mode, err := updatePrelockMode(h, b.Ref, arm.Input.Targets, nil)
			if err != nil {
				return err
			}
			if err := h.AcquireUpdateRow(b.Ref, old, mode); err != nil {
				return err
			}
			current, err := h.LatestRow(RowRequest{Kind: RowMerge, Ref: b.Ref, Row: old.Row, Physical: old.Version, Hint: -1, GrantMode: mode})
			if err != nil {
				return err
			}
			if !current.Live || current.Version.Version != old.Version {
				return ErrTupleRecheck
			}
			old = current.Version
			if err := h.PhysicalRowLock(b.Ref, old, mode); err != nil {
				return err
			}
		}
		if b.Relation.HasTriggers() {
			targets := UpdateTargetAttributes(h, b.Ref, b.Relation, arm.Input.Targets, nil)
			before, failure := Before(h, b.Relation, BeforeRow{Relation: b.Ref, Event: BeforeUpdate, Old: old.Values, New: row, Targets: targets})
			if failure != nil {
				return failure
			}
			if !before.Proceed {
				return nil
			}
			row = before.Row
		}
		if err := validatePhysicalInsertRow(h, b, &b, schema, row, &rowContext); err != nil {
			return err
		}
	} else {
		proceed, err := ValidateInsertRow(h, b, schema, row, &rowContext)
		if err != nil {
			return err
		}
		if !proceed {
			return nil
		}
	}
	predecessor := storage.RowVersion{}
	var actionMode lock.MultiMode
	if arm.Action == parser.MergeUpdateAction {
		// Demanded projection/constraints precede second-modification arbitration.
		if mergeAlreadyAccepted(accepted, old) {
			return MergeCardinalityError()
		}
		keyChanged, err := UniqueKeyChanged(h, b.Ref, old.Values, row)
		if err != nil {
			return err
		}
		if keyChanged {
			if err := h.AcquireUpdateRow(b.Ref, old, lock.ForUpdate); err != nil {
				return err
			}
			current, err := h.LatestRow(RowRequest{Kind: RowMerge, Ref: b.Ref, Row: old.Row, Physical: old.Version, Hint: -1, GrantMode: lock.ForUpdate})
			if err != nil {
				return err
			}
			if !current.Live || current.Version.Version != old.Version {
				return ErrTupleRecheck
			}
			old = current.Version
		}
		if err := ClaimUniqueRow(evaluation.Context, h, b.Ref, old); err != nil {
			return err
		}
		old.PendingKeyChange = old.PendingKeyChange || keyChanged
		actionMode = lock.MultiNoKeyAction
		if keyChanged {
			actionMode = lock.MultiUpdateAction
		}
		*excluded = append(*excluded, old)
		predecessor = old
	}
	candidate := index.WriteCandidate{Values: row}
	if unique {
		if err := CheckUniqueRow(evaluation.Context, h, b.Ref, &candidate, *excluded, mergeUniquePrefix(result.WriteBatch, pending)); err != nil {
			return err
		}
	}
	if arm.Action == parser.MergeUpdateAction {
		accepted[old.Version] = true
	}
	result.Actions = append(result.Actions, MergeActionRow{Action: arm.Action, Payload: len(result.Values), UpdateMode: actionMode})
	result.Predecessors = append(result.Predecessors, predecessor)
	result.Append(candidate)
	return nil
}

func mergeFollowChainStrong(h Host, b analyze.Binding) (bool, error) {
	if b.Merge.HasDelete {
		return true, nil
	}
	unique, err := BindUniqueWrite(h, b.Ref)
	if err != nil {
		return false, err
	}
	return MergeFollowStrong(b.Merge, unique.KeySlots), nil
}

func newMergeReturningOutput(b analyze.Binding, output sqltype.OutputEnvironment) *MergeReturningOutput {
	if b.Returning == nil {
		return nil
	}
	rows := exec.NewFinalRows(b.Returning.Schema, 0)
	rows.Output = output
	return &MergeReturningOutput{Returning: b.Returning, Rows: rows, Frame: make([]sqltype.Value, b.Merge.TargetWidth+b.Merge.SourceWidth)}
}
