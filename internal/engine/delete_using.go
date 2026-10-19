package engine

import (
	"errors"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

type deleteUsingCandidate struct {
	deleteCandidate
	sourceIndex int
	physical    queryPhysicalRow
}

// The source stage owns immutable joined frames. Only qualifying identities and
// occurrence indexes are retained, before any target row arbitration can wait.
func (s *StatementScope) deleteUsingSource(b binding, source *querySource, evaluation *EvalContext, positioned mutationRowIdentity) (Result, error) {
	ctx := evaluation.Context
	evaluation.query.virtualReads = queryHasVirtualReads(b.deleteUsing) || boundHasVirtualReads(b.predicate)
	if b.returning != nil {
		for _, expression := range b.returning.expressions {
			evaluation.query.virtualReads = evaluation.query.virtualReads || boundHasVirtualReads(expression)
		}
	}
	stage, err := openQueryNode(ctx, evaluation.query, b.deleteUsing)
	if err != nil {
		return Result{}, err
	}
	stage, err = settlePendingQueryMarks(ctx, evaluation.query, b.deleteUsing, stage)
	if err != nil {
		return Result{}, err
	}
	defer stage.Close()
	if err := stage.drain(ctx); err != nil {
		return Result{}, err
	}
	var frame []sqltype.Value
	sourceCount := stage.count()
	targetCount := 0
	if sourceCount != 0 {
		frame = make([]sqltype.Value, len(b.relation.Columns)+b.deleteSourceWidth)
		targetCount = source.count()
	}
	var candidates []deleteUsingCandidate
	for i := range targetCount {
		physical := mutationPhysicalRow(source, i, b)
		version := physical.version
		if b.currentOf != "" && (positioned.ref != physical.leaf.ref || version.Row != positioned.row) {
			continue
		}
		candidate := physical.deletion()
		for j := range sourceCount {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			copy(frame, physical.values(version))
			copy(frame[len(b.relation.Columns):], stage.source.row(stage.index(j)))
			evaluation.Row = frame
			evaluation.bindQueryIdentity(stage.source, stage.index(j))
			evaluation.physicalTable = physical.leaf.ref
			matches, err := updateMatches(evaluation, b.predicate)
			if err != nil {
				return Result{}, err
			}
			if matches {
				candidates = append(candidates, deleteUsingCandidate{deleteCandidate: candidate, sourceIndex: j, physical: physical})
			}
		}
	}
	var deletedIDs map[mutationRowIdentity]bool
	if len(candidates) != 0 {
		deletedIDs = make(map[mutationRowIdentity]bool, min(len(candidates), source.count()))
	}
	var rows *finalRows
	keep := false
	if b.returning != nil {
		rows = newFinalRows(b.returning.schema, 0)
		rows.output = s.output
		defer func() {
			if !keep {
				rows.Close()
			}
		}()
	}
	var count int64
	for _, pair := range candidates {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		candidate := pair.deleteCandidate
		identity := mutationRowIdentity{candidate.ref, candidate.row}
		if deletedIDs[identity] {
			continue
		}
		if err := s.acquireRow(candidate); err != nil {
			return Result{}, err
		}
		current, live, err := s.latestDeleteRow(candidate)
		if err != nil {
			return Result{}, err
		}
		if !live {
			continue
		}
		copy(frame, pair.physical.values(current))
		copy(frame[len(b.relation.Columns):], stage.source.row(stage.index(pair.sourceIndex)))
		evaluation.Row = frame
		evaluation.bindQueryIdentity(stage.source, stage.index(pair.sourceIndex))
		evaluation.physicalTable = candidate.ref
		if current.Version != candidate.version {
			if err := s.physicalRowEvent(candidate.ref, current, lock.MultiUpdateLock, 0); err != nil {
				return Result{}, err
			}
			matches, err := updateMatches(evaluation, b.predicate)
			if err != nil {
				return Result{}, err
			}
			if !matches {
				continue
			}
		}
		if err := s.claimUniqueRow(candidate.ref, current); err != nil {
			return Result{}, err
		}
		deleted, err := s.Writes().Delete(candidate)
		if err != nil {
			return Result{}, err
		}
		if !deleted {
			continue
		}
		deletedIDs[identity] = true
		count++
		if rows != nil {
			for output, expr := range b.returning.expressions {
				value, err := expr.Eval(evaluation)
				if err != nil {
					return Result{}, err
				}
				if value.Type() == nil || !value.Type().Concrete() || value.Type() != rows.schema[output].Usage.Type {
					return Result{}, errors.New("stuntgres: invalid finalized DELETE value")
				}
				rows.values = append(rows.values, value)
			}
			rows.count++
		}
	}
	if rows != nil && b.returning.regclassOutput && count != 0 {
		if err := s.op.session.Engine().captureRegclassOutput(s.op, rows); err != nil {
			return Result{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	keep = true
	if rows != nil {
		return Result{Count: count, Rows: rows}, nil
	}
	return Result{Count: count}, nil
}
