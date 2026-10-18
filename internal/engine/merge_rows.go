package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// DELETE carries captured identity only; payload indexes name INSERT/UPDATE
// rows. All records remain private until the sole statement Finish.
type mergeActionRow struct {
	action     parser.MergeAction
	payload    int
	deletion   deleteCandidate
	updateMode multiMode
}
type mergeRows struct {
	indexWriteBatch
	predecessors []rowVersion
	actions      []mergeActionRow
	returning    *finalRows
}

func (rows *mergeRows) stage(w *WriteStage, ref ObjectRef) error {
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
	deletion deleteCandidate
}

// Source completion and ON membership precede every action wait. Match marks
// refer to captured occurrences, preserving equal values and RR old+own bags.
func (e *Engine) assembleMergeRows(ctx context.Context, scope *StatementScope, b binding, target *querySource, cursor RowCursor, state *queryState, schema string, unique bool) (*mergeRows, error) {
	sourceSchema := cursor.Schema()
	width := len(sourceSchema)
	scratch := make([]sqltype.Value, width)
	var sources []sqltype.Value
	var occurrences, provenance []int
	// The borrowed stage closes before actions, but executeMerge's input owner
	// keeps the source namespace and any action-subquery leaves live until exit.
	var stage *indexedStage
	if state.virtualReads {
		var ok bool
		stage, ok = cursor.(*indexedStage)
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
		if state.virtualReads {
			if sourceCount == 0 {
				occurrences = appendQueryOccurrences(nil, stage.source)
			}
			provenance = appendQueryProvenance(provenance, stage.source, stage.index(stage.position-1))
		}
		if stage, ok := cursor.(*indexedStage); ok && stage.source.presenceCount != 0 {
			presenceSlots, presenceCount = stage.source.presenceSlots, stage.source.presenceCount
			presence = append(presence, stage.source.presenceRow(stage.index(stage.position-1))...)
		}
		sourceCount++
	}
	if err := cursor.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	frame := make([]sqltype.Value, len(b.relation.columns)+width)
	evaluation := state.evaluation(ctx)
	evaluation.Row = frame
	evaluation.presenceSlots = presenceSlots
	evaluation.queryOccurrences = occurrences
	bindIdentity := func(index int) {
		if state.virtualReads {
			width := len(occurrences)
			evaluation.queryProvenance = provenance[index*width : (index+1)*width]
		}
	}
	var pairs []mergeCandidate
	var targetMatched []bool
	if b.merge.hasBySource {
		targetMatched = make([]bool, target.count())
	}
	capture := func(i int, class parser.MergeMatch, source int) mergeCandidate {
		slot := target.rawIndex(i)
		old := target.versionAt(slot)
		deletion := deleteCandidate{ref: b.ref, storage: b.storage, row: old.row, version: old.version, ordinal: slot}
		if slot >= len(target.committed) {
			deletion.own = true
			deletion.ordinal -= len(target.committed)
		}
		return mergeCandidate{updateCandidate: updateCandidate{rowVersion: old, slot: slot, sourceIndex: source}, class: class, deletion: deletion}
	}
	for j := range sourceCount {
		if presenceCount != 0 {
			evaluation.presence = presence[j*presenceCount : (j+1)*presenceCount]
		}
		copy(frame[len(b.relation.columns):], sources[j*width:(j+1)*width])
		bindIdentity(j)
		matched := false
		for i := range target.count() {
			old := target.versionAt(target.rawIndex(i))
			copy(frame, old.values)
			truth, err := mergeMatches(&evaluation, b.merge.on)
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
		if !matched && b.merge.hasByTarget {
			pairs = append(pairs, mergeCandidate{updateCandidate: updateCandidate{sourceIndex: j}, class: parser.MergeNotMatched})
		}
	}
	for i, matched := range targetMatched {
		if !matched {
			pairs = append(pairs, capture(i, parser.MergeNotMatchedBySource, -1))
		}
	}
	var nullSource []sqltype.Value
	if b.merge.hasBySource && (b.merge.hasInsert || b.returning != nil) {
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
	var accepted map[RowID]bool
	if len(b.merge.updateTargets) != 0 || b.merge.hasDelete {
		accepted = make(map[RowID]bool)
	}
	var excluded []rowVersion
	for _, candidate := range pairs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source := nullSource
		if candidate.sourceIndex >= 0 {
			source = sources[candidate.sourceIndex*width : (candidate.sourceIndex+1)*width]
			copy(frame[len(b.relation.columns):], source)
			bindIdentity(candidate.sourceIndex)
			if presenceCount != 0 {
				evaluation.presence = presence[candidate.sourceIndex*presenceCount : (candidate.sourceIndex+1)*presenceCount]
			}
		} else {
			evaluation.presence = nil
			evaluation.queryProvenance = nil
		}
		old, class := candidate.rowVersion, candidate.class
		setFrame := func() {
			switch class {
			case parser.MergeMatched:
				copy(frame, old.values)
				evaluation.Row = frame
			case parser.MergeNotMatchedBySource:
				evaluation.Row = old.values
			case parser.MergeNotMatched:
				evaluation.Row = source
			}
		}
		setFrame()
		if class == parser.MergeMatched && b.merge.hasBySource {
			// BY SOURCE action classification owns a guarded ON evaluation,
			// distinct from candidate matching and later update-chain rechecks.
			// Initial BY SOURCE has no source row, so its guard skips ON.
			matched, err := mergeMatches(&evaluation, b.merge.on)
			if err != nil {
				return nil, err
			}
			if !matched {
				class = parser.MergeNotMatchedBySource
				setFrame()
			}
		}
		arm, err := b.merge.selectArm(&evaluation, class)
		if err != nil {
			return nil, err
		}
		if arm == nil || arm.action == parser.MergeNothingAction {
			continue
		}
		sourceAfter := false
		if class != parser.MergeNotMatched {
			if arm.action == parser.MergeDeleteAction {
				err = scope.acquireRow(candidate.deletion)
			} else {
				err = scope.acquireUpdateRow(b.ref, old.row)
			}
			if err != nil {
				return nil, err
			}
			latest, live, err := e.latestMergeRow(scope, b.ref, candidate.updateCandidate)
			if err != nil {
				return nil, err
			}
			if latest.version != old.version && live {
				strong, err := scope.mergeFollowChainStrong(b)
				if err != nil {
					return nil, err
				}
				if strong && arm.action != parser.MergeDeleteAction {
					if err := scope.acquireUpdateRowMode(b.ref, old.row, rowUpdate); err != nil {
						return nil, err
					}
					latest, live, err = e.latestMergeRow(scope, b.ref, candidate.updateCandidate)
					if err != nil {
						return nil, err
					}
				}
				lockMode := multiNoKeyLock
				if strong || arm.action == parser.MergeDeleteAction {
					lockMode = multiUpdateLock
				}
				if err := scope.physicalRowEvent(b.ref, latest, lockMode, 0); err != nil {
					return nil, err
				}
			}
			if !live {
				if class == parser.MergeNotMatchedBySource && arm.action == parser.MergeDeleteAction {
					continue
				}
				class = parser.MergeNotMatched
			} else if latest.version != old.version {
				old = latest
				setFrame()
				if class == parser.MergeMatched {
					matched, err := mergeMatches(&evaluation, b.merge.on)
					if err != nil {
						return nil, err
					}
					if !matched {
						class = parser.MergeNotMatched
						if b.merge.hasBySource {
							class = parser.MergeNotMatchedBySource
							sourceAfter = b.merge.hasByTarget
						}
					}
				}
			}
			if !live || latest.version != candidate.version {
				setFrame()
				arm, err = b.merge.selectArm(&evaluation, class)
				if err != nil {
					return nil, err
				}
			}
		}
		// A MATCHED candidate that loses ON may act on the missing-source target
		// and then on its retained missing-target source. Initial BY SOURCE never
		// enters MATCHED merely because a peer made ON true.
		for {
			if arm != nil && arm.action != parser.MergeNothingAction {
				if err := e.appendMergeAction(scope, b, result, &evaluation, schema, unique, arm, old, candidate.deletion, accepted, &excluded); err != nil {
					return nil, err
				}
				if output != nil {
					actualOld := old.values
					var row []sqltype.Value
					if arm.action != parser.MergeDeleteAction {
						row = result.values[len(result.values)-1]
					}
					if arm.action == parser.MergeInsertAction {
						actualOld = nil
					}
					if err := output.project(b, &evaluation, arm.action, actualOld, row, source, candidate.sourceIndex >= 0); err != nil {
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
			arm, err = b.merge.selectArm(&evaluation, class)
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

func (e *Engine) appendMergeAction(scope *StatementScope, b binding, result *mergeRows, evaluation *EvalContext, schema string, unique bool, arm *boundMergeArm, old rowVersion, deletion deleteCandidate, accepted map[RowID]bool, excluded *[]rowVersion) error {
	if arm.action == parser.MergeDeleteAction {
		if accepted[old.row] {
			return mergeCardinalityError()
		}
		// Initial DELETE owns rowUpdate before following a predecessor; an
		// initial UPDATE restarts under the hasDelete strong-mode union.
		if err := scope.claimUniqueRow(b.ref, old); err != nil {
			return err
		}
		if err := scope.physicalRowEvent(b.ref, old, multiUpdateAction, 0); err != nil {
			return err
		}
		*excluded = append(*excluded, old)
		// Retire exclusion authority in actual action order before later admission.
		// Canonical Delete still validates the original captured provenance at stage.
		projected := deletion
		projected.version = old.version
		e.mu.Lock()
		scope.projectExclusionDeletionLocked(e, projected)
		e.mu.Unlock()
		accepted[old.row] = true
		result.actions = append(result.actions, mergeActionRow{action: arm.action, deletion: deletion})
		return nil
	}
	row := make([]sqltype.Value, len(b.relation.columns))
	if arm.action == parser.MergeUpdateAction {
		copy(row, old.values)
		for i, expression := range arm.input.update.assignments {
			if expression == nil {
				continue
			}
			target := arm.input.targets[i]
			var value sqltype.Value
			var err error
			if len(arm.input.update.arrayTargets) != 0 && arm.input.update.arrayTargets[i] != nil {
				value, err = arm.input.update.arrayTargets[i].applyExpression(evaluation, row[target], expression)
			} else {
				value, err = expression.Eval(evaluation)
			}
			if err != nil {
				return err
			}
			row[target] = value
		}
	} else {
		for i, column := range b.relation.columns {
			row[i] = sqltype.Null(column.Usage.Type)
		}
		if err := evaluateIdentityValuesRow(scope.commentCatalog, &arm.input, arm.input.expressions[0], arm.syntax, row, false, evaluation); err != nil {
			return err
		}
	}
	rowContext := *evaluation
	scope.rlsProposal = scope.rlsMergeInsert
	if arm.action == parser.MergeUpdateAction {
		scope.rlsProposal = scope.rlsMergeUpdate
	}
	if err := e.validateInsertRow(scope, b, schema, row, &rowContext); err != nil {
		return err
	}
	predecessor := rowVersion{}
	var actionMode multiMode
	if arm.action == parser.MergeUpdateAction {
		// Demanded projection/constraints precede second-modification arbitration.
		if accepted[old.row] {
			return mergeCardinalityError()
		}
		keyChanged, err := scope.uniqueKeyChanged(b.ref, old.values, row)
		if err != nil {
			return err
		}
		if keyChanged {
			if err := scope.acquireUpdateRowMode(b.ref, old.row, rowUpdate); err != nil {
				return err
			}
		}
		if err := scope.claimUniqueRow(b.ref, old); err != nil {
			return err
		}
		old.pendingKeyChange = old.pendingKeyChange || keyChanged
		actionMode = multiNoKeyAction
		if keyChanged {
			actionMode = multiUpdateAction
		}
		if err := scope.physicalRowEvent(b.ref, old, actionMode, 0); err != nil {
			return err
		}
		*excluded = append(*excluded, old)
		predecessor = old
	}
	candidate := indexWriteCandidate{values: row}
	if unique {
		if err := scope.checkUniqueRow(b.ref, &candidate, *excluded, result.indexWriteBatch); err != nil {
			return err
		}
	}
	if arm.action == parser.MergeUpdateAction {
		accepted[old.row] = true
	}
	result.actions = append(result.actions, mergeActionRow{action: arm.action, payload: len(result.values), updateMode: actionMode})
	result.predecessors = append(result.predecessors, predecessor)
	result.append(candidate)
	return nil
}

func mergeMatches(evaluation *EvalContext, on BoundExpr) (bool, error) {
	value, err := on.Eval(evaluation)
	if err != nil {
		return false, err
	}
	truth, err := mergeOnTruth(value)
	return truth == True, err
}

func (merge *boundMerge) selectArm(evaluation *EvalContext, class parser.MergeMatch) (*boundMergeArm, error) {
	for i := range merge.arms {
		arm := &merge.arms[i]
		if arm.match != class {
			continue
		}
		selected, err := updateMatches(evaluation, arm.condition)
		if err != nil {
			return nil, err
		}
		if selected {
			return arm, nil
		}
	}
	return nil, nil
}

func (s *StatementScope) mergeFollowChainStrong(b binding) (bool, error) {
	if b.merge.hasDelete {
		return true, nil
	}
	unique, err := s.bindUniqueWrite(b.ref)
	if err != nil {
		return false, err
	}
	for _, key := range unique.keySlots {
		for _, target := range b.merge.updateTargets {
			if key == target {
				return true, nil
			}
		}
	}
	return false, nil
}

// MERGE follows a committed update even with a retained RR frontier. Deletion
// still conflicts; query visibility, not this fetch, owns old+own occurrences.
func (e *Engine) latestMergeRow(s *StatementScope, ref ObjectRef, candidate updateCandidate) (rowVersion, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := s.validateLockedRelation(e); err != nil {
		return rowVersion{}, false, err
	}
	if ref.Epoch != e.state.epoch || s.txRef.Epoch() != e.state.epoch {
		return rowVersion{}, false, ErrCatalogReplaced
	}
	store := e.privateStorageLocked(s.owner.catalog, ref.Address)
	if store == nil {
		return rowVersion{}, false, ErrCatalogReplaced
	}
	current, hint := store.versions, candidate.slot
	if own, _, found := rowByID(s.ownWrites[ref.Address], candidate.row, -1); found {
		return own, liveVersion(own, e.commitSeq, store.ends, s.ownDeletes[ref.Address]), nil
	}
	row, _, found := rowByID(current, candidate.row, hint)
	if !found {
		return rowVersion{}, false, errors.New("stuntgres: invalid MERGE predecessor")
	}
	live := liveVersion(row, e.commitSeq, store.ends, s.ownDeletes[ref.Address])
	if !live && stableIsolation(s.owner.isolation) && store.ends != nil {
		if tombstone, ok := store.ends.rows[row.row]; ok && tombstone.version == row.version && tombstone.end > s.owner.frontier {
			return rowVersion{}, false, sqlError("40001", "could not serialize access due to concurrent delete")
		}
	}
	return row, live, nil
}
