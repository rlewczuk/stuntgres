package engine

import (
	"errors"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

type deleteUsingCandidate struct {
	visibility.DeleteCandidate
	sourceIndex int
	physical    exec.QueryPhysicalRow
}

// The source stage owns immutable joined frames. Only qualifying identities and
// occurrence indexes are retained, before any target row arbitration can wait.
func (s *StatementScope) deleteUsingSource(b binding, source *exec.Source, evaluation *exec.Context, positioned mutationRowIdentity) (Result, error) {
	ctx := evaluation.Context
	evaluation.Query.VirtualReads = exec.QueryHasVirtualReads(b.delete.using) || exec.BoundHasVirtualReads(b.predicate)
	if b.returning != nil {
		for _, expression := range b.returning.expressions {
			evaluation.Query.VirtualReads = evaluation.Query.VirtualReads || exec.BoundHasVirtualReads(expression)
		}
	}
	stage, err := exec.OpenNode(ctx, evaluation.Query, b.delete.using)
	if err != nil {
		return Result{}, err
	}
	stage, err = exec.SettlePendingQueryMarks(ctx, evaluation.Query, b.delete.using, stage)
	if err != nil {
		return Result{}, err
	}
	defer stage.Close()
	if err := stage.Drain(ctx); err != nil {
		return Result{}, err
	}
	var frame []sqltype.Value
	sourceCount := stage.Count()
	targetCount := 0
	if sourceCount != 0 {
		frame = make([]sqltype.Value, len(b.relation.Columns)+b.delete.sourceWidth)
		targetCount = source.Count()
	}
	var candidates []deleteUsingCandidate
	for i := range targetCount {
		physical := mutationPhysicalRow(source, i, b)
		version := physical.Version
		if b.currentOf != "" && (positioned.ref != physical.Leaf.Ref || version.Row != positioned.row) {
			continue
		}
		candidate := physical.Deletion()
		for j := range sourceCount {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			copy(frame, physical.Values(version))
			copy(frame[len(b.relation.Columns):], stage.Source.Row(stage.Index(j)))
			evaluation.Row = frame
			evaluation.BindQueryIdentity(stage.Source, stage.Index(j))
			evaluation.PhysicalTable = physical.Leaf.Ref
			matches, err := exec.UpdateMatches(evaluation, b.predicate)
			if err != nil {
				return Result{}, err
			}
			if matches {
				candidates = append(candidates, deleteUsingCandidate{DeleteCandidate: candidate, sourceIndex: j, physical: physical})
			}
		}
	}
	var deletedIDs map[mutationRowIdentity]bool
	if len(candidates) != 0 {
		deletedIDs = make(map[mutationRowIdentity]bool, min(len(candidates), source.Count()))
	}
	var rows *exec.FinalRows
	keep := false
	if b.returning != nil {
		rows = exec.NewFinalRows(b.returning.schema, 0)
		rows.Output = s.output
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
		candidate := pair.DeleteCandidate
		identity := mutationRowIdentity{candidate.Ref, candidate.Row}
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
		copy(frame, pair.physical.Values(current))
		copy(frame[len(b.relation.Columns):], stage.Source.Row(stage.Index(pair.sourceIndex)))
		evaluation.Row = frame
		evaluation.BindQueryIdentity(stage.Source, stage.Index(pair.sourceIndex))
		evaluation.PhysicalTable = candidate.Ref
		if current.Version != candidate.Version {
			if err := s.physicalRowEvent(candidate.Ref, current, lock.MultiUpdateLock, 0); err != nil {
				return Result{}, err
			}
			matches, err := exec.UpdateMatches(evaluation, b.predicate)
			if err != nil {
				return Result{}, err
			}
			if !matches {
				continue
			}
		}
		if err := s.claimUniqueRow(candidate.Ref, current); err != nil {
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
				if value.Type() == nil || !value.Type().Concrete() || value.Type() != rows.Columns[output].Usage.Type {
					return Result{}, errors.New("stuntgres: invalid finalized DELETE value")
				}
				rows.Values = append(rows.Values, value)
			}
			rows.Count++
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
