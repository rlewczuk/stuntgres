package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// DELETE carries captured identity only; payload indexes name INSERT/UPDATE
// rows. All records remain private until the sole statement Finish.
type mergeActionRow struct {
	action     parser.MergeAction
	payload    int
	deletion   visibility.DeleteCandidate
	updateMode lock.MultiMode
}
type mergeRows struct {
	indexWriteBatch
	predecessors []storage.RowVersion
	actions      []mergeActionRow
	returning    *exec.FinalRows
}

func (rows *mergeRows) stage(w *WriteStage, ref ids.ObjectRef) error {
	for start := 0; start < len(rows.actions); {
		action := rows.actions[start]
		if action.action == parser.MergeDeleteAction {
			if _, err := w.Delete(action.deletion); err != nil {
				return err
			}
			start++
			continue
		}
		end := start + 1
		for end < len(rows.actions) && rows.actions[end].action == action.action && rows.actions[end].updateMode == action.updateMode {
			end++
		}
		first, last := action.payload, rows.actions[end-1].payload+1
		var err error
		if action.action == parser.MergeUpdateAction {
			batch := rows.indexWriteBatch.slice(first, last)
			batch.actionMode = action.updateMode
			err = w.replace(ref, rows.predecessors[first:last], batch)
		} else {
			err = w.applyValidated(ref, rows.indexWriteBatch.slice(first, last))
		}
		if err != nil {
			return err
		}
		start = end
	}
	return nil
}

type mergeCandidate struct {
	updateCandidate
	class    parser.MergeMatch
	deletion visibility.DeleteCandidate
}

// Source completion and ON membership precede every action wait. Match marks
// refer to captured occurrences, preserving equal values and RR old+own bags.
func (e *Engine) assembleMergeRows(ctx context.Context, scope *StatementScope, b analyze.Binding, target *exec.Source, cursor exec.RowCursor, state *exec.State, schema string, unique bool) (*mergeRows, error) {
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
	var pairs []mergeCandidate
	var targetMatched []bool
	if b.Merge.HasBySource {
		targetMatched = make([]bool, target.Count())
	}
	capture := func(i int, class parser.MergeMatch, source int) mergeCandidate {
		slot := target.RawIndex(i)
		old := target.VersionAt(slot)
		deletion := visibility.DeleteCandidate{Ref: b.Ref, Storage: b.Storage, Row: old.Row, Version: old.Version, Ordinal: slot}
		if slot >= len(target.Committed) {
			deletion.Own = true
			deletion.Ordinal -= len(target.Committed)
		}
		return mergeCandidate{updateCandidate: updateCandidate{RowVersion: old, slot: slot, sourceIndex: source}, class: class, deletion: deletion}
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
			truth, err := mergeMatches(&evaluation, b.Merge.On)
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
			pairs = append(pairs, mergeCandidate{updateCandidate: updateCandidate{sourceIndex: j}, class: parser.MergeNotMatched})
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
	result := &mergeRows{}
	output := newMergeReturningOutput(b, scope)
	if output != nil {
		result.returning = output.rows
	}
	complete := false
	defer func() {
		if !complete && result.returning != nil {
			result.returning.Close()
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
		if candidate.sourceIndex >= 0 {
			source = sources[candidate.sourceIndex*width : (candidate.sourceIndex+1)*width]
			copy(frame[len(b.Relation.Columns):], source)
			bindIdentity(candidate.sourceIndex)
			if presenceCount != 0 {
				evaluation.Presence = presence[candidate.sourceIndex*presenceCount : (candidate.sourceIndex+1)*presenceCount]
			}
		} else {
			evaluation.Presence = nil
			evaluation.QueryProvenance = nil
		}
		old, class := candidate.RowVersion, candidate.class
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
			matched, err := mergeMatches(&evaluation, b.Merge.On)
			if err != nil {
				return nil, err
			}
			if !matched {
				class = parser.MergeNotMatchedBySource
				setFrame()
			}
		}
		arm, err := selectMergeArm(b.Merge, &evaluation, class)
		if err != nil {
			return nil, err
		}
		if arm == nil || arm.Action == parser.MergeNothingAction {
			continue
		}
		sourceAfter := false
		if class != parser.MergeNotMatched {
			if arm.Action == parser.MergeDeleteAction {
				err = scope.acquireRow(candidate.deletion)
			} else {
				err = scope.acquireUpdateRow(b.Ref, old.Row)
			}
			if err != nil {
				return nil, err
			}
			latest, live, err := e.latestMergeRow(scope, b.Ref, candidate.updateCandidate)
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
					latest, live, err = e.latestMergeRow(scope, b.Ref, candidate.updateCandidate)
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
					matched, err := mergeMatches(&evaluation, b.Merge.On)
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
				arm, err = selectMergeArm(b.Merge, &evaluation, class)
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
				if err := e.appendMergeAction(scope, b, result, &evaluation, schema, unique, arm, old, candidate.deletion, accepted, &excluded); err != nil {
					return nil, err
				}
				if output != nil {
					actualOld := old.Values
					var row []sqltype.Value
					if arm.Action != parser.MergeDeleteAction {
						row = result.values[len(result.values)-1]
					}
					if arm.Action == parser.MergeInsertAction {
						actualOld = nil
					}
					if err := output.project(b, &evaluation, arm.Action, actualOld, row, source, candidate.sourceIndex >= 0); err != nil {
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
			arm, err = selectMergeArm(b.Merge, &evaluation, class)
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

func mergeCardinalityError() error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "21000", Message: "MERGE command cannot affect row a second time", Hint: "Ensure that not more than one source row matches any one target row."}
}

func (e *Engine) appendMergeAction(scope *StatementScope, b analyze.Binding, result *mergeRows, evaluation *exec.Context, schema string, unique bool, arm *analyze.MergeArm, old storage.RowVersion, deletion visibility.DeleteCandidate, accepted map[ids.RowID]bool, excluded *[]storage.RowVersion) error {
	if arm.Action == parser.MergeDeleteAction {
		if accepted[old.Row] {
			return mergeCardinalityError()
		}
		// Initial DELETE owns rowUpdate before following a predecessor; an
		// initial UPDATE restarts under the hasDelete strong-mode union.
		if err := scope.claimUniqueRow(b.Ref, old); err != nil {
			return err
		}
		if err := scope.physicalRowEvent(b.Ref, old, lock.MultiUpdateAction, 0); err != nil {
			return err
		}
		*excluded = append(*excluded, old)
		// Retire exclusion authority in actual action order before later admission.
		// Canonical Delete still validates the original captured provenance at stage.
		projected := deletion
		projected.Version = old.Version
		e.mu.Lock()
		scope.projectExclusionDeletionLocked(e, projected)
		e.mu.Unlock()
		accepted[old.Row] = true
		result.actions = append(result.actions, mergeActionRow{action: arm.Action, deletion: deletion})
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
	scope.rls.proposal = scope.rls.mergeInsert
	if arm.Action == parser.MergeUpdateAction {
		scope.rls.proposal = scope.rls.mergeUpdate
	}
	if err := e.validateInsertRow(scope, b, schema, row, &rowContext); err != nil {
		return err
	}
	predecessor := storage.RowVersion{}
	var actionMode lock.MultiMode
	if arm.Action == parser.MergeUpdateAction {
		// Demanded projection/constraints precede second-modification arbitration.
		if accepted[old.Row] {
			return mergeCardinalityError()
		}
		keyChanged, err := scope.uniqueKeyChanged(b.Ref, old.Values, row)
		if err != nil {
			return err
		}
		if keyChanged {
			if err := scope.acquireUpdateRowMode(b.Ref, old.Row, lock.ForUpdate); err != nil {
				return err
			}
		}
		if err := scope.claimUniqueRow(b.Ref, old); err != nil {
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
	candidate := indexWriteCandidate{values: row}
	if unique {
		if err := scope.checkUniqueRow(b.Ref, &candidate, *excluded, result.indexWriteBatch); err != nil {
			return err
		}
	}
	if arm.Action == parser.MergeUpdateAction {
		accepted[old.Row] = true
	}
	result.actions = append(result.actions, mergeActionRow{action: arm.Action, payload: len(result.values), updateMode: actionMode})
	result.predecessors = append(result.predecessors, predecessor)
	result.append(candidate)
	return nil
}

func mergeMatches(evaluation *exec.Context, on expr.Expr) (bool, error) {
	value, err := on.Eval(evaluation)
	if err != nil {
		return false, err
	}
	truth, err := mergeOnTruth(value)
	return truth == plan.True, err
}

func selectMergeArm(merge *analyze.MergeBinding, evaluation *exec.Context, class parser.MergeMatch) (*analyze.MergeArm, error) {
	for i := range merge.Arms {
		arm := &merge.Arms[i]
		if arm.Match != class {
			continue
		}
		selected, err := exec.UpdateMatches(evaluation, arm.Condition)
		if err != nil {
			return nil, err
		}
		if selected {
			return arm, nil
		}
	}
	return nil, nil
}

func (s *StatementScope) mergeFollowChainStrong(b analyze.Binding) (bool, error) {
	if b.Merge.HasDelete {
		return true, nil
	}
	unique, err := s.bindUniqueWrite(b.Ref)
	if err != nil {
		return false, err
	}
	for _, key := range unique.keySlots {
		for _, target := range b.Merge.UpdateTargets {
			if key == target {
				return true, nil
			}
		}
	}
	return false, nil
}

// MERGE follows a committed update even with a retained RR frontier. Deletion
// still conflicts; query visibility, not this fetch, owns old+own occurrences.
func (e *Engine) latestMergeRow(s *StatementScope, ref ids.ObjectRef, candidate updateCandidate) (storage.RowVersion, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return storage.RowVersion{}, false, err
	}
	if ref.Epoch != e.catalog.state.Epoch() || s.txRef.Epoch() != e.catalog.state.Epoch() {
		return storage.RowVersion{}, false, ids.ErrCatalogReplaced
	}
	store := e.privateStorageLocked(s.owner.Catalog, ref.Address)
	if store == nil {
		return storage.RowVersion{}, false, ids.ErrCatalogReplaced
	}
	current, hint := store.Versions, candidate.slot
	if own, _, found := storage.RowByID(s.ownWrites[ref.Address], candidate.Row, -1); found {
		return own, visibility.LiveVersion(own, e.visibility.CommitSeq(), store.Ends, s.ownDeletes[ref.Address]), nil
	}
	row, _, found := storage.RowByID(current, candidate.Row, hint)
	if !found {
		return storage.RowVersion{}, false, errors.New("stuntgres: invalid MERGE predecessor")
	}
	live := visibility.LiveVersion(row, e.visibility.CommitSeq(), store.Ends, s.ownDeletes[ref.Address])
	if !live && stableIsolation(s.owner.Isolation) && store.Ends != nil {
		if tombstone, ok := store.Ends.Rows[row.Row]; ok && tombstone.Version == row.Version && tombstone.End > s.owner.Frontier {
			return storage.RowVersion{}, false, sqlError("40001", "could not serialize access due to concurrent delete")
		}
	}
	return row, live, nil
}
