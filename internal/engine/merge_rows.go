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
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func stageMergeRows(rows *dml.MergeRows, w *WriteStage, ref ids.ObjectRef) error {
	for start := 0; start < len(rows.Actions); {
		action := rows.Actions[start]
		if action.Action == parser.MergeDeleteAction {
			if _, err := w.Delete(action.Deletion); err != nil {
				return err
			}
			start++
			continue
		}
		end := start + 1
		for end < len(rows.Actions) && rows.Actions[end].Action == action.Action && rows.Actions[end].UpdateMode == action.UpdateMode {
			end++
		}
		first, last := action.Payload, rows.Actions[end-1].Payload+1
		var err error
		if action.Action == parser.MergeUpdateAction {
			batch := rows.WriteBatch.Slice(first, last)
			batch.ActionMode = action.UpdateMode
			err = replaceRows(w.scope.writeHost(), ref, rows.Predecessors[first:last], batch, dml.ReplaceMerge)
		} else {
			err = applyValidated(w.scope.writeHost(), ref, rows.WriteBatch.Slice(first, last))
		}
		if err != nil {
			return err
		}
		start = end
	}
	return nil
}

// Source completion and ON membership precede every action wait. Match marks
// refer to captured occurrences, preserving equal values and RR old+own bags.
func (e *Engine) assembleMergeRows(ctx context.Context, scope *StatementScope, b analyze.Binding, target *exec.Source, cursor exec.RowCursor, state *exec.State, schema string, unique bool) (*dml.MergeRows, error) {
	sourceSchema := cursor.Schema()
	width := len(sourceSchema)
	scratch := make([]sqltype.Value, width)
	var sources []sqltype.Value
	var occurrences, provenance []int
	// The borrowed stage closes before actions, but executeMerge's input owner
	// keeps the source namespace and any action-subquery leaves live until exit.
	var stage *exec.Stage
	if state.VirtualReads {
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
		if state.VirtualReads {
			if sourceCount == 0 {
				occurrences = exec.AppendQueryOccurrences(nil, stage.Source)
			}
			provenance = exec.AppendQueryProvenance(provenance, stage.Source, stage.Index(stage.Position-1))
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
	evaluation.Row = frame
	evaluation.PresenceSlots = presenceSlots
	evaluation.QueryOccurrences = occurrences
	bindIdentity := func(index int) {
		if state.VirtualReads {
			width := len(occurrences)
			evaluation.QueryProvenance = provenance[index*width : (index+1)*width]
		}
	}
	var pairs []dml.MergeCandidate
	var targetMatched []bool
	if b.Merge.HasBySource {
		targetMatched = make([]bool, target.Count())
	}
	capture := func(i int, class parser.MergeMatch, source int) dml.MergeCandidate {
		slot := target.RawIndex(i)
		old := target.VersionAt(slot)
		deletion := visibility.DeleteCandidate{Ref: b.Ref, Storage: b.Storage, Row: old.Row, Version: old.Version, Ordinal: slot}
		if slot >= len(target.Committed) {
			deletion.Own = true
			deletion.Ordinal -= len(target.Committed)
		}
		return dml.MergeCandidate{UpdateCandidate: dml.UpdateCandidate{RowVersion: old, Slot: slot, SourceIndex: source}, Class: class, Deletion: deletion}
	}
	for j := range sourceCount {
		if presenceCount != 0 {
			evaluation.Presence = presence[j*presenceCount : (j+1)*presenceCount]
		}
		copy(frame[len(b.Relation.Columns):], sources[j*width:(j+1)*width])
		bindIdentity(j)
		matched := false
		for i := range target.Count() {
			old := target.VersionAt(target.RawIndex(i))
			copy(frame, old.Values)
			truth, err := dml.MergeMatches(&evaluation, b.Merge.On)
			if err != nil {
				return nil, err
			}
			if truth {
				pairs = append(pairs, capture(i, parser.MergeMatched, j))
				matched = true
				if targetMatched != nil {
					targetMatched[i] = true
				}
			}
		}
		if !matched && b.Merge.HasByTarget {
			pairs = append(pairs, dml.MergeCandidate{UpdateCandidate: dml.UpdateCandidate{SourceIndex: j}, Class: parser.MergeNotMatched})
		}
	}
	for i, matched := range targetMatched {
		if !matched {
			pairs = append(pairs, capture(i, parser.MergeNotMatchedBySource, -1))
		}
	}
	var nullSource []sqltype.Value
	if b.Merge.HasBySource && (b.Merge.HasInsert || b.Returning != nil) {
		nullSource = make([]sqltype.Value, width)
		for i, column := range sourceSchema {
			nullSource[i] = sqltype.Null(column.Usage.Type)
		}
	}
	result := &dml.MergeRows{}
	output := newMergeReturningOutput(b, scope)
	if output != nil {
		result.Returning = output.Rows
	}
	complete := false
	defer func() {
		if !complete && result.Returning != nil {
			result.Returning.Close()
		}
	}()
	var accepted map[ids.RowID]bool
	if len(b.Merge.UpdateTargets) != 0 || b.Merge.HasDelete {
		accepted = make(map[ids.RowID]bool)
	}
	var excluded []storage.RowVersion
	for _, candidate := range pairs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source := nullSource
		if candidate.SourceIndex >= 0 {
			source = sources[candidate.SourceIndex*width : (candidate.SourceIndex+1)*width]
			copy(frame[len(b.Relation.Columns):], source)
			bindIdentity(candidate.SourceIndex)
			if presenceCount != 0 {
				evaluation.Presence = presence[candidate.SourceIndex*presenceCount : (candidate.SourceIndex+1)*presenceCount]
			}
		} else {
			evaluation.Presence = nil
			evaluation.QueryProvenance = nil
		}
		old, class := candidate.RowVersion, candidate.Class
		setFrame := func() {
			switch class {
			case parser.MergeMatched:
				copy(frame, old.Values)
				evaluation.Row = frame
			case parser.MergeNotMatchedBySource:
				evaluation.Row = old.Values
			case parser.MergeNotMatched:
				evaluation.Row = source
			}
		}
		setFrame()
		if class == parser.MergeMatched && b.Merge.HasBySource {
			// BY SOURCE action classification owns a guarded ON evaluation,
			// distinct from candidate matching and later update-chain rechecks.
			// Initial BY SOURCE has no source row, so its guard skips ON.
			matched, err := dml.MergeMatches(&evaluation, b.Merge.On)
			if err != nil {
				return nil, err
			}
			if !matched {
				class = parser.MergeNotMatchedBySource
				setFrame()
			}
		}
		arm, err := dml.SelectMergeArm(b.Merge, &evaluation, class)
		if err != nil {
			return nil, err
		}
		if arm == nil || arm.Action == parser.MergeNothingAction {
			continue
		}
		sourceAfter := false
		if class != parser.MergeNotMatched {
			if arm.Action == parser.MergeDeleteAction {
				err = scope.acquireRow(candidate.Deletion)
			} else {
				err = scope.acquireUpdateRow(b.Ref, old.Row)
			}
			if err != nil {
				return nil, err
			}
			current, err := scope.writeHost().LatestRow(dml.MergeRowRequest(b.Ref, candidate.UpdateCandidate))
			latest, live := current.Version, current.Live
			if err != nil {
				return nil, err
			}
			if latest.Version != old.Version && live {
				strong, err := scope.mergeFollowChainStrong(b)
				if err != nil {
					return nil, err
				}
				if strong && arm.Action != parser.MergeDeleteAction {
					if err := scope.acquireUpdateRowMode(b.Ref, old.Row, lock.ForUpdate); err != nil {
						return nil, err
					}
					current, err = scope.writeHost().LatestRow(dml.MergeRowRequest(b.Ref, candidate.UpdateCandidate))
					latest, live = current.Version, current.Live
					if err != nil {
						return nil, err
					}
				}
				lockMode := lock.MultiNoKeyLock
				if strong || arm.Action == parser.MergeDeleteAction {
					lockMode = lock.MultiUpdateLock
				}
				if err := scope.physicalRowEvent(b.Ref, latest, lockMode, 0); err != nil {
					return nil, err
				}
			}
			if !live {
				if class == parser.MergeNotMatchedBySource && arm.Action == parser.MergeDeleteAction {
					continue
				}
				class = parser.MergeNotMatched
			} else if latest.Version != old.Version {
				old = latest
				setFrame()
				if class == parser.MergeMatched {
					matched, err := dml.MergeMatches(&evaluation, b.Merge.On)
					if err != nil {
						return nil, err
					}
					if !matched {
						class = parser.MergeNotMatched
						if b.Merge.HasBySource {
							class = parser.MergeNotMatchedBySource
							sourceAfter = b.Merge.HasByTarget
						}
					}
				}
			}
			if !live || latest.Version != candidate.Version {
				setFrame()
				arm, err = dml.SelectMergeArm(b.Merge, &evaluation, class)
				if err != nil {
					return nil, err
				}
			}
		}
		// A MATCHED candidate that loses ON may act on the missing-source target
		// and then on its retained missing-target source. Initial BY SOURCE never
		// enters MATCHED merely because a peer made ON true.
		for {
			if arm != nil && arm.Action != parser.MergeNothingAction {
				if err := e.appendMergeAction(scope, b, result, &evaluation, schema, unique, arm, old, candidate.Deletion, accepted, &excluded); err != nil {
					return nil, err
				}
				if output != nil {
					actualOld := old.Values
					var row []sqltype.Value
					if arm.Action != parser.MergeDeleteAction {
						row = result.Values[len(result.Values)-1]
					}
					if arm.Action == parser.MergeInsertAction {
						actualOld = nil
					}
					if err := output.Project(b, &evaluation, arm.Action, actualOld, row, source, candidate.SourceIndex >= 0); err != nil {
						return nil, err
					}
				}
			}
			if !sourceAfter {
				break
			}
			sourceAfter = false
			class = parser.MergeNotMatched
			setFrame()
			arm, err = dml.SelectMergeArm(b.Merge, &evaluation, class)
			if err != nil {
				return nil, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	complete = true
	return result, nil
}

func (e *Engine) appendMergeAction(scope *StatementScope, b analyze.Binding, result *dml.MergeRows, evaluation *exec.Context, schema string, unique bool, arm *analyze.MergeArm, old storage.RowVersion, deletion visibility.DeleteCandidate, accepted map[ids.RowID]bool, excluded *[]storage.RowVersion) error {
	if arm.Action == parser.MergeDeleteAction {
		if accepted[old.Row] {
			return dml.MergeCardinalityError()
		}
		// Initial DELETE owns rowUpdate before following a predecessor; an
		// initial UPDATE restarts under the hasDelete strong-mode union.
		if err := claimUniqueRow(scope.writeHost(), b.Ref, old); err != nil {
			return err
		}
		if err := scope.physicalRowEvent(b.Ref, old, lock.MultiUpdateAction, 0); err != nil {
			return err
		}
		*excluded = append(*excluded, old)
		// Retire exclusion authority in actual action order before later admission.
		// Canonical Delete still validates the original captured provenance at stage.
		scope.writeHost().EndExclusionRow(deletion.Ref, deletion.Row, old.Version)
		accepted[old.Row] = true
		result.Actions = append(result.Actions, dml.MergeActionRow{Action: arm.Action, Deletion: deletion})
		return nil
	}
	row := make([]sqltype.Value, len(b.Relation.Columns))
	if arm.Action == parser.MergeUpdateAction {
		copy(row, old.Values)
		for i, expression := range arm.Input.Update.Assignments {
			if expression == nil {
				continue
			}
			target := arm.Input.Targets[i]
			var value sqltype.Value
			var err error
			if len(arm.Input.Update.ArrayTargets) != 0 && arm.Input.Update.ArrayTargets[i] != nil {
				value, err = arm.Input.Update.ArrayTargets[i].ApplyExpression(evaluation, row[target], expression)
			} else {
				value, err = expression.Eval(evaluation)
			}
			if err != nil {
				return err
			}
			row[target] = value
		}
	} else {
		for i, column := range b.Relation.Columns {
			row[i] = sqltype.Null(column.Usage.Type)
		}
		if err := evaluateInsertValuesRow(scope.inquiry.commentCatalog, &arm.Input, arm.Input.Expressions[0], arm.Syntax, row, false, evaluation); err != nil {
			return err
		}
	}
	rowContext := *evaluation
	scope.RLS.Proposal = scope.RLS.MergeInsert
	if arm.Action == parser.MergeUpdateAction {
		scope.RLS.Proposal = scope.RLS.MergeUpdate
	}
	if err := e.validateInsertRow(scope, b, schema, row, &rowContext); err != nil {
		return err
	}
	predecessor := storage.RowVersion{}
	var actionMode lock.MultiMode
	if arm.Action == parser.MergeUpdateAction {
		// Demanded projection/constraints precede second-modification arbitration.
		if accepted[old.Row] {
			return dml.MergeCardinalityError()
		}
		keyChanged, err := uniqueKeyChanged(scope.writeHost(), b.Ref, old.Values, row)
		if err != nil {
			return err
		}
		if keyChanged {
			if err := scope.acquireUpdateRowMode(b.Ref, old.Row, lock.ForUpdate); err != nil {
				return err
			}
		}
		if err := claimUniqueRow(scope.writeHost(), b.Ref, old); err != nil {
			return err
		}
		old.PendingKeyChange = old.PendingKeyChange || keyChanged
		actionMode = lock.MultiNoKeyAction
		if keyChanged {
			actionMode = lock.MultiUpdateAction
		}
		if err := scope.physicalRowEvent(b.Ref, old, actionMode, 0); err != nil {
			return err
		}
		*excluded = append(*excluded, old)
		predecessor = old
	}
	candidate := index.WriteCandidate{Values: row}
	if unique {
		if err := checkUniqueRow(scope.writeHost(), b.Ref, &candidate, *excluded, result.WriteBatch); err != nil {
			return err
		}
	}
	if arm.Action == parser.MergeUpdateAction {
		accepted[old.Row] = true
	}
	result.Actions = append(result.Actions, dml.MergeActionRow{Action: arm.Action, Payload: len(result.Values), UpdateMode: actionMode})
	result.Predecessors = append(result.Predecessors, predecessor)
	result.Append(candidate)
	return nil
}

func (s *StatementScope) mergeFollowChainStrong(b analyze.Binding) (bool, error) {
	if b.Merge.HasDelete {
		return true, nil
	}
	unique, err := bindUniqueWrite(s.writeHost(), b.Ref)
	if err != nil {
		return false, err
	}
	return dml.MergeFollowStrong(b.Merge, unique.KeySlots), nil
}
