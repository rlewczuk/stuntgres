package dml

import (
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// The source stage owns immutable joined frames. Only qualifying identities and
// occurrence indexes are retained, before any target row arbitration can wait.
func deleteUsingSource(h Host, b analyze.Binding, source *exec.Source, evaluation *exec.Context, positioned MutationRowIdentity) (outcome Outcome, err error) {
	ctx := evaluation.Context
	evaluation.Query.IdentityReads = exec.QueryHasIdentityReads(b.Delete.Using) || exec.BoundHasIdentityReads(b.Predicate)
	if b.Returning != nil {
		for _, expression := range b.Returning.Expressions {
			evaluation.Query.IdentityReads = evaluation.Query.IdentityReads || exec.BoundHasIdentityReads(expression)
		}
	}
	stage, err := exec.OpenNode(ctx, evaluation.Query, b.Delete.Using)
	if err != nil {
		return Outcome{}, err
	}
	stage, err = exec.SettlePendingQueryMarks(ctx, evaluation.Query, b.Delete.Using, stage)
	if err != nil {
		return Outcome{}, err
	}
	defer outcome.finishStage(&stage, &err)
	if err := stage.Drain(ctx); err != nil {
		return Outcome{}, err
	}
	var frame []sqltype.Value
	sourceCount := stage.Count()
	targetCount := 0
	if sourceCount != 0 {
		frame = make([]sqltype.Value, len(b.Relation.Columns)+b.Delete.SourceWidth)
		targetCount = source.Count()
	}
	var candidates []DeleteUsingCandidate
	for i := range targetCount {
		physical := MutationPhysicalRow(source, i, b)
		version := physical.Version
		if b.CurrentOf != "" && !MutationPositionMatches(positioned, physical, source) {
			continue
		}
		evaluation.Row = physical.Values(version)
		evaluation.PhysicalTable, evaluation.PhysicalVersion = physical.Leaf.Ref, version
		visible, _, err := exec.EvaluateRowSecurity(h.Command().RLS.Old, evaluation, evaluation.Row)
		if err != nil {
			return Outcome{}, err
		}
		if !visible {
			continue
		}
		candidate := physical.Deletion()
		for j := range sourceCount {
			if err := ctx.Err(); err != nil {
				return Outcome{}, err
			}
			copy(frame, physical.Values(version))
			copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(j)))
			evaluation.Row = frame
			evaluation.BindQueryIdentity(stage.Source, stage.Index(j))
			evaluation.PhysicalTable, evaluation.PhysicalVersion = physical.Leaf.Ref, version
			matches, err := exec.UpdateMatches(evaluation, b.Predicate)
			if err != nil {
				return Outcome{}, err
			}
			if matches {
				candidates = append(candidates, DeleteUsingCandidate{DeleteCandidate: candidate, SourceIndex: j, Physical: physical})
			}
		}
	}
	var deletedIDs map[MutationRowIdentity]bool
	if len(candidates) != 0 {
		deletedIDs = make(map[MutationRowIdentity]bool, min(len(candidates), source.Count()))
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
	physicalReturning := returningHasPhysicalHeaders(b.Returning)
	var count int64
usingCandidate:
	for _, pair := range candidates {
		if err := ctx.Err(); err != nil {
			return Outcome{}, err
		}
		candidate := pair.DeleteCandidate
		identity := MutationRowIdentity{Ref: candidate.Ref, Row: candidate.Row, Version: candidate.Version}
		if deletedIDs[identity] {
			continue
		}
		if err := h.AcquireRow(candidate); err != nil {
			return Outcome{}, err
		}
		beforeDone, late := false, false
	retryUsing:
		for {
			latest, err := h.LatestRow(DeleteRowRequest(candidate))
			current, live := latest.Version, latest.Live
			if err != nil {
				return Outcome{}, err
			}
			if !live {
				continue usingCandidate
			}
			copy(frame, pair.Physical.Values(current))
			copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(pair.SourceIndex)))
			evaluation.Row = frame
			evaluation.BindQueryIdentity(stage.Source, stage.Index(pair.SourceIndex))
			evaluation.PhysicalTable, evaluation.PhysicalVersion = candidate.Ref, current
			evaluation.PhysicalXmaxValid, evaluation.OldPhysicalXmaxValid = false, false
			if current.Version != candidate.Version || HasRowTriggers(pair.Physical.Leaf.Relation, stored.TriggerDelete, stored.TriggerBefore) {
				if err := h.PhysicalRowLock(candidate.Ref, current, lock.ForUpdate); err != nil {
					return Outcome{}, err
				}
			}
			if current.Version != candidate.Version || late {
				if err := exec.CaptureReturningXmax(evaluation, evaluation.PhysicalRawXmax != nil); err != nil {
					return Outcome{}, err
				}
				matches, err := MutationMatches(h, evaluation, b.Predicate)
				if err != nil {
					return Outcome{}, err
				}
				if !matches {
					continue usingCandidate
				}
			}
			if !beforeDone {
				before, failure := Before(h, pair.Physical.Leaf.Relation, BeforeRow{Relation: candidate.Ref, Event: BeforeDelete, Old: current.Values})
				if failure != nil {
					return Outcome{}, failure
				}
				beforeDone = true
				if !before.Proceed {
					continue usingCandidate
				}
			}
			if err := ClaimUniqueRow(ctx, h, candidate.Ref, current); err != nil {
				return Outcome{}, err
			}
			deleted, err := h.Delete(candidate, current)
			if errors.Is(err, ErrTupleRecheck) {
				late = true
				continue retryUsing
			}
			if err != nil {
				return Outcome{}, err
			}
			if !deleted {
				continue usingCandidate
			}
			if candidate.Ref != b.Ref && NeedsTransition(b.Relation, stored.TriggerDelete) {
				if err := captureTransition(h, b, stored.TriggerDelete, pair.Physical.Values(current), nil); err != nil {
					return Outcome{}, err
				}
			}
			deletedIDs[identity] = true
			count++
			if rows != nil {
				if physicalReturning {
					if err := captureDeletedHeader(h, candidate.Ref, current, evaluation); err != nil {
						return Outcome{}, err
					}
				}
				if err := exec.CaptureReturningXmax(evaluation, returningReadsXmax(b.Returning)); err != nil {
					return Outcome{}, err
				}
				for output, expr := range b.Returning.Expressions {
					value, err := expr.Eval(evaluation)
					if err != nil {
						return Outcome{}, err
					}
					if value.Type() == nil || !value.Type().Concrete() || value.Type() != rows.Columns[output].Usage.Type {
						return Outcome{}, errors.New("stuntgres: invalid finalized DELETE value")
					}
					rows.Values = append(rows.Values, value)
				}
				rows.Count++
				if err := h.Command().CapturedRow(); err != nil {
					return Outcome{}, err
				}
			}
			break
		}
	}
	if rows != nil && b.Returning.RegclassOutput && count != 0 {
		if err := h.CaptureRegclassOutput(rows); err != nil {
			return Outcome{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	keep = true
	if rows != nil {
		return Outcome{Count: count, Rows: rows}, nil
	}
	return Outcome{Count: count}, nil
}
