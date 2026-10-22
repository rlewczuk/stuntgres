package engine

import (
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

// The source stage owns immutable joined frames. Only qualifying identities and
// occurrence indexes are retained, before any target row arbitration can wait.
func deleteUsingSource(h dml.Host, b analyze.Binding, source *exec.Source, evaluation *exec.Context, positioned dml.MutationRowIdentity) (dml.Outcome, error) {
	ctx := evaluation.Context
	evaluation.Query.VirtualReads = exec.QueryHasVirtualReads(b.Delete.Using) || exec.BoundHasVirtualReads(b.Predicate)
	if b.Returning != nil {
		for _, expression := range b.Returning.Expressions {
			evaluation.Query.VirtualReads = evaluation.Query.VirtualReads || exec.BoundHasVirtualReads(expression)
		}
	}
	stage, err := exec.OpenNode(ctx, evaluation.Query, b.Delete.Using)
	if err != nil {
		return dml.Outcome{}, err
	}
	stage, err = exec.SettlePendingQueryMarks(ctx, evaluation.Query, b.Delete.Using, stage)
	if err != nil {
		return dml.Outcome{}, err
	}
	defer stage.Close()
	if err := stage.Drain(ctx); err != nil {
		return dml.Outcome{}, err
	}
	var frame []sqltype.Value
	sourceCount := stage.Count()
	targetCount := 0
	if sourceCount != 0 {
		frame = make([]sqltype.Value, len(b.Relation.Columns)+b.Delete.SourceWidth)
		targetCount = source.Count()
	}
	var candidates []dml.DeleteUsingCandidate
	for i := range targetCount {
		physical := dml.MutationPhysicalRow(source, i, b)
		version := physical.Version
		if b.CurrentOf != "" && (positioned.Ref != physical.Leaf.Ref || version.Row != positioned.Row) {
			continue
		}
		candidate := physical.Deletion()
		for j := range sourceCount {
			if err := ctx.Err(); err != nil {
				return dml.Outcome{}, err
			}
			copy(frame, physical.Values(version))
			copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(j)))
			evaluation.Row = frame
			evaluation.BindQueryIdentity(stage.Source, stage.Index(j))
			evaluation.PhysicalTable = physical.Leaf.Ref
			matches, err := exec.UpdateMatches(evaluation, b.Predicate)
			if err != nil {
				return dml.Outcome{}, err
			}
			if matches {
				candidates = append(candidates, dml.DeleteUsingCandidate{DeleteCandidate: candidate, SourceIndex: j, Physical: physical})
			}
		}
	}
	var deletedIDs map[dml.MutationRowIdentity]bool
	if len(candidates) != 0 {
		deletedIDs = make(map[dml.MutationRowIdentity]bool, min(len(candidates), source.Count()))
	}
	var rows *exec.FinalRows
	keep := false
	if b.Returning != nil {
		rows = exec.NewFinalRows(b.Returning.Schema, 0)
		rows.Output = evaluation.Output
		defer func() {
			if !keep {
				rows.Close()
			}
		}()
	}
	var count int64
	for _, pair := range candidates {
		if err := ctx.Err(); err != nil {
			return dml.Outcome{}, err
		}
		candidate := pair.DeleteCandidate
		identity := dml.MutationRowIdentity{Ref: candidate.Ref, Row: candidate.Row}
		if deletedIDs[identity] {
			continue
		}
		if err := h.AcquireRow(candidate); err != nil {
			return dml.Outcome{}, err
		}
		latest, err := h.LatestRow(dml.DeleteRowRequest(candidate))
		current, live := latest.Version, latest.Live
		if err != nil {
			return dml.Outcome{}, err
		}
		if !live {
			continue
		}
		copy(frame, pair.Physical.Values(current))
		copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(pair.SourceIndex)))
		evaluation.Row = frame
		evaluation.BindQueryIdentity(stage.Source, stage.Index(pair.SourceIndex))
		evaluation.PhysicalTable = candidate.Ref
		if current.Version != candidate.Version {
			if err := h.PhysicalRowEvent(candidate.Ref, current, lock.MultiUpdateLock, 0); err != nil {
				return dml.Outcome{}, err
			}
			matches, err := exec.UpdateMatches(evaluation, b.Predicate)
			if err != nil {
				return dml.Outcome{}, err
			}
			if !matches {
				continue
			}
		}
		if err := dml.ClaimUniqueRow(h, candidate.Ref, current); err != nil {
			return dml.Outcome{}, err
		}
		deleted, err := h.Delete(candidate)
		if err != nil {
			return dml.Outcome{}, err
		}
		if !deleted {
			continue
		}
		deletedIDs[identity] = true
		count++
		if rows != nil {
			for output, expr := range b.Returning.Expressions {
				value, err := expr.Eval(evaluation)
				if err != nil {
					return dml.Outcome{}, err
				}
				if value.Type() == nil || !value.Type().Concrete() || value.Type() != rows.Columns[output].Usage.Type {
					return dml.Outcome{}, errors.New("stuntgres: invalid finalized DELETE value")
				}
				rows.Values = append(rows.Values, value)
			}
			rows.Count++
		}
	}
	if rows != nil && b.Returning.RegclassOutput && count != 0 {
		if err := h.CaptureRegclassOutput(rows); err != nil {
			return dml.Outcome{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return dml.Outcome{}, err
	}
	keep = true
	if rows != nil {
		return dml.Outcome{Count: count, Rows: rows}, nil
	}
	return dml.Outcome{Count: count}, nil
}
