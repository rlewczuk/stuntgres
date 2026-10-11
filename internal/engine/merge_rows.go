package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Payloads stay private until source completion, all demanded actions and
// constraints succeed. A zero predecessor denotes an insertion. Contiguous
// action runs reuse canonical staging, including its catalog revalidation.
type mergeRows struct {
	values       [][]sqltype.Value
	predecessors []rowVersion
}

func (rows *mergeRows) stage(w *WriteStage, ref ObjectRef) error {
	for start := 0; start < len(rows.values); {
		update := rows.predecessors[start].row != 0
		end := start + 1
		for end < len(rows.values) && (rows.predecessors[end].row != 0) == update {
			end++
		}
		var err error
		if update {
			err = w.replace(ref, rows.predecessors[start:end], rows.values[start:end])
		} else {
			err = w.applyValidated(ref, rows.values[start:end])
		}
		if err != nil {
			return err
		}
		start = end
	}
	return nil
}

// Drain projected source occurrences once and require Close before acquiring
// action row locks. Membership is frozen for every target/source pair before
// the first wait; a follow-chain recheck can remove a pair, never widen it.
func (e *Engine) assembleMergeRows(ctx context.Context, scope *StatementScope, b binding, target *querySource, cursor RowCursor, state *queryState, schema string, unique bool) (*mergeRows, error) {
	width := len(cursor.Schema())
	scratch := make([]sqltype.Value, width)
	var sources []sqltype.Value
	sourceCount := 0
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
	var pairs []updateCandidate
	for j := range sourceCount {
		copy(frame[len(b.relation.columns):], sources[j*width:(j+1)*width])
		matched := false
		for i := range target.count() {
			old := target.versionAt(target.rawIndex(i))
			copy(frame, old.values)
			truth, err := mergeMatches(&evaluation, b.merge.on)
			if err != nil {
				return nil, err
			}
			if truth {
				pairs = append(pairs, updateCandidate{rowVersion: old, slot: target.rawIndex(i), sourceIndex: j})
				matched = true
			}
		}
		if !matched {
			pairs = append(pairs, updateCandidate{sourceIndex: j})
		}
	}
	result := &mergeRows{}
	var accepted map[RowID]bool
	if len(b.merge.updateTargets) != 0 {
		accepted = make(map[RowID]bool)
	}
	var excluded []rowVersion
	for _, candidate := range pairs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		source := sources[candidate.sourceIndex*width : (candidate.sourceIndex+1)*width]
		old := candidate.rowVersion
		matched := old.row != 0
		copy(frame[len(b.relation.columns):], source)
		if matched {
			copy(frame, old.values)
		}
		evaluation.Row = source
		if matched {
			evaluation.Row = frame
		}
		arm, err := b.merge.selectArm(&evaluation, matched)
		if err != nil {
			return nil, err
		}
		if arm == nil {
			continue
		}
		if matched {
			if err := scope.acquireUpdateRow(b.ref, old.row); err != nil {
				return nil, err
			}
			latest, live, err := e.latestMergeRow(scope, b.ref, candidate)
			if err != nil {
				return nil, err
			}
			if latest.version != old.version && live {
				// Only the follow-chain path uses the whole syntactic UPDATE bitmap.
				strong, err := scope.mergeFollowChainStrong(b)
				if err != nil {
					return nil, err
				}
				if strong {
					if err := scope.acquireUpdateRowMode(b.ref, old.row, rowUpdate); err != nil {
						return nil, err
					}
					latest, live, err = e.latestMergeRow(scope, b.ref, candidate)
					if err != nil {
						return nil, err
					}
				}
			}
			if !live {
				matched = false
			} else if latest.version != old.version {
				old = latest
				copy(frame, old.values)
				matched, err = mergeMatches(&evaluation, b.merge.on)
				if err != nil {
					return nil, err
				}
			}
			if !live || latest.version != candidate.version {
				if !matched {
					evaluation.Row = source
				}
				// Every concurrent restart starts at the first arm of its new class.
				arm, err = b.merge.selectArm(&evaluation, matched)
				if err != nil {
					return nil, err
				}
				if arm == nil {
					continue
				}
			}
		}
		row := make([]sqltype.Value, len(b.relation.columns))
		diagnostic := b
		if matched {
			copy(row, old.values)
			for i, expression := range arm.input.update.assignments {
				if expression == nil {
					continue
				}
				target := arm.input.targets[i]
				var value sqltype.Value
				if len(arm.input.update.arrayTargets) != 0 && arm.input.update.arrayTargets[i] != nil {
					value, err = arm.input.update.arrayTargets[i].applyExpression(&evaluation, row[target], expression)
				} else {
					value, err = expression.Eval(&evaluation)
				}
				if err != nil {
					return nil, err
				}
				row[target] = value
			}
		} else {
			for i, column := range b.relation.columns {
				row[i] = sqltype.Null(column.Usage.Type)
			}
			if err := evaluateIdentityValuesRow(scope.commentCatalog, &arm.input, arm.input.expressions[0], arm.syntax, row, false, &evaluation); err != nil {
				return nil, err
			}
		}
		rowContext := evaluation
		scope.rlsProposal = scope.rlsMergeInsert
		if matched {
			scope.rlsProposal = scope.rlsMergeUpdate
		}
		if err := e.validateInsertRow(scope, diagnostic, schema, row, &rowContext); err != nil {
			return nil, err
		}
		if matched {
			// Projection and row constraints precede TM_SelfModified/cardinality.
			if accepted[old.row] {
				return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "21000", Message: "MERGE command cannot affect row a second time", Hint: "Ensure that not more than one source row matches any one target row."}
			}
			keyChanged, err := scope.uniqueKeyChanged(b.ref, old.values, row)
			if err != nil {
				return nil, err
			}
			if keyChanged {
				if err := scope.acquireUpdateRowMode(b.ref, old.row, rowUpdate); err != nil {
					return nil, err
				}
				if _, live, err := e.latestMergeRow(scope, b.ref, candidate); err != nil {
					return nil, err
				} else if !live {
					return nil, errors.New("stuntgres: lost locked MERGE predecessor")
				}
			}
			if err := scope.claimUniqueRow(b.ref, old.values); err != nil {
				return nil, err
			}
			old.pendingKeyChange = old.pendingKeyChange || keyChanged
			excluded = append(excluded, old)
		}
		if unique {
			if err := scope.checkUniqueRow(b.ref, row, excluded, result.values); err != nil {
				return nil, err
			}
		}
		predecessor := rowVersion{}
		if matched {
			predecessor = old
			accepted[old.row] = true
		}
		result.predecessors = append(result.predecessors, predecessor)
		result.values = append(result.values, row)
	}
	return result, ctx.Err()
}

func mergeMatches(evaluation *EvalContext, on BoundExpr) (bool, error) {
	value, err := on.Eval(evaluation)
	if err != nil {
		return false, err
	}
	truth, err := mergeOnTruth(value)
	return truth == True, err
}

func (merge *boundMerge) selectArm(evaluation *EvalContext, matched bool) (*boundMergeArm, error) {
	class := parser.MergeNotMatched
	if matched {
		class = parser.MergeMatched
	}
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
	if ref.Epoch != e.state.epoch || s.txRef.epoch != e.state.epoch {
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
