package dml

import (
	"context"
	"errors"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// DeleteInput is one DELETE execution: the captured target Source, the query
// sources (nil without USING or subquery demand) and the canonical
// parameters. ExecuteDelete closes Inputs; the caller closes Source.
type DeleteInput struct {
	Context    context.Context // Effective runtime context; nil uses operation authority.
	Binding    analyze.Binding
	Source     *exec.Source
	Inputs     *exec.Source
	Parameters []sqltype.Value
}

// ExecuteDelete runs DELETE after the root has captured the target and its
// sources and released Engine.mu: each qualifying row takes its row lock,
// rereads its latest version, rechecks the predicate when it moved and is
// staged for deletion.
func ExecuteDelete(h Host, in DeleteInput) (outcome Outcome, err error) {
	b, source, inputs, parameters := in.Binding, in.Source, in.Inputs, in.Parameters
	if err := BeforeStatement(h, b, stored.TriggerDelete); err != nil {
		if inputs != nil {
			inputs.Close()
		}
		return Outcome{}, err
	}
	if err := RegisterAfterStatement(h, b, stored.TriggerDelete); err != nil {
		if inputs != nil {
			inputs.Close()
		}
		return Outcome{}, err
	}
	defer CompleteAfterStatement(h, b, stored.TriggerDelete, &outcome, &err)
	_, lockedStorage := h.Target()
	if b.Storage == nil || b.Storage != lockedStorage || b.Storage != source.View.Data().GenerationAt(b.Ref.Address) {
		if inputs != nil {
			inputs.Close()
		}
		return Outcome{}, ids.ErrCatalogReplaced
	}
	var positioned MutationRowIdentity
	if b.CurrentOf != "" {
		var err error
		positioned, err = h.PositionedRow(b)
		if err != nil {
			if inputs != nil {
				inputs.Close()
			}
			return Outcome{}, err
		}
	}
	ctx := in.Context
	input, output := h.Environments()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Parameters: parameters, Host: h, Input: input, Output: output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	if inputs != nil {
		if err := exec.SelectQuerySources(ctx, inputs); err != nil {
			return Outcome{}, err
		}
		inputs = view.FreezeWriteSources(inputs, b.QuerySources, h.QueryAuthority(), h)
		defer inputs.Close()
		state := &exec.State{Source: inputs, SourceMode: exec.SourceReusable, Parameters: parameters, Input: input, Output: output, Locking: inputs.Locks != nil}
		state.Methods = h.AdmittedPlannerMethods()
		state.CapturePlannerAdmission()
		defer state.ReleaseMemberships()
		defer outcome.finishSources(state, &err)
		inputs.Host = h
		evaluation.Query = state
	}
	rawDemand, err := captureMutationXmax(h, b, source, inputs)
	if err != nil {
		return Outcome{}, err
	}
	evaluation.PhysicalRawXmax = source.RawXmax
	if b.Delete.Using != nil {
		return deleteUsingSource(h, b, source, &evaluation, positioned)
	}
	physicalReturning := returningHasPhysicalHeaders(b.Returning)
	var count int64
	var rows *exec.FinalRows
	keep := false
	if b.Returning != nil {
		rows = exec.NewFinalRows(b.Returning.Schema, 0)
		rows.Output = output
		defer func() {
			if !keep {
				rows.Close()
			}
		}()
	}
deleteCandidate:
	for i := range source.Count() {
		if err := ctx.Err(); err != nil {
			return Outcome{}, err
		}
		physical := MutationPhysicalRow(source, i, b)
		version := physical.Version
		if b.CurrentOf != "" && !MutationPositionMatches(positioned, physical, source) {
			continue
		}
		evaluation.Row = physical.Values(version)
		evaluation.PhysicalTable, evaluation.PhysicalVersion = physical.Leaf.Ref, version
		evaluation.PhysicalXmaxValid, evaluation.OldPhysicalXmaxValid = false, false
		matches, err := MutationMatches(h, &evaluation, b.Predicate)
		if err != nil {
			return Outcome{}, err
		}
		if !matches {
			continue
		}
		candidate := physical.Deletion()
		if err := h.AcquireRow(candidate); err != nil {
			return Outcome{}, err
		}
		beforeDone, late := false, false
	retryDelete:
		for {
			latest, err := h.LatestRow(DeleteRowRequest(candidate))
			current, live := latest.Version, latest.Live
			if err != nil {
				return Outcome{}, err
			}
			if !live {
				continue deleteCandidate
			}
			if current.Version != candidate.Version || HasRowTriggers(physical.Leaf.Relation, stored.TriggerDelete, stored.TriggerBefore) {
				if err := h.PhysicalRowLock(candidate.Ref, current, lock.ForUpdate); err != nil {
					return Outcome{}, err
				}
			}
			if current.Version != candidate.Version || late {
				evaluation.Row = physical.Values(current)
				evaluation.PhysicalVersion = current
				if err := exec.CaptureReturningXmax(&evaluation, rawDemand); err != nil {
					return Outcome{}, err
				}
				matches, err := MutationMatches(h, &evaluation, b.Predicate)
				if err != nil {
					return Outcome{}, err
				}
				if !matches {
					continue deleteCandidate
				}
			}
			if !beforeDone {
				before, failure := Before(h, physical.Leaf.Relation, BeforeRow{Relation: candidate.Ref, Event: BeforeDelete, Old: current.Values})
				if failure != nil {
					return Outcome{}, failure
				}
				beforeDone = true
				if !before.Proceed {
					continue deleteCandidate
				}
			}
			if err := ClaimUniqueRow(ctx, h, candidate.Ref, current); err != nil {
				return Outcome{}, err
			}
			deleted, err := h.Delete(candidate, current)
			if errors.Is(err, ErrTupleRecheck) {
				late = true
				continue retryDelete
			}
			if err != nil {
				return Outcome{}, err
			}
			if deleted {
				if candidate.Ref != b.Ref && NeedsTransition(b.Relation, stored.TriggerDelete) {
					if err := captureTransition(h, b, stored.TriggerDelete, physical.Values(current), nil); err != nil {
						return Outcome{}, err
					}
				}
				count++
				if rows != nil {
					evaluation.Row = physical.Values(current)
					if physicalReturning {
						if err := captureDeletedHeader(h, candidate.Ref, current, &evaluation); err != nil {
							return Outcome{}, err
						}
					}
					if err := exec.CaptureReturningXmax(&evaluation, returningReadsXmax(b.Returning)); err != nil {
						return Outcome{}, err
					}
					for output, expr := range b.Returning.Expressions {
						value, err := expr.Eval(&evaluation)
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
