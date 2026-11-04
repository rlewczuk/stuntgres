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
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// UpdateInput is one UPDATE execution: the captured target Source, the FROM
// sources (nil without FROM or subquery demand), the canonical parameters and
// the target schema name. ExecuteUpdate closes Source and From.
type UpdateInput struct {
	Context           context.Context
	Binding           analyze.Binding
	Source            *exec.Source
	From              *exec.Source
	Parameters        []sqltype.Value
	Schema            string
	DefaultOccurrence exec.DefaultOccurrence // Original preparation ownership, not runtime row identity.
}

// ExecuteUpdate runs UPDATE after the root has captured the target and its
// sources and released Engine.mu. The candidate set is frozen first; each
// candidate then takes its row lock, rereads its latest version, rechecks the
// predicate when it moved, validates and stages its replacement.
func ExecuteUpdate(h Host, in UpdateInput) (outcome Outcome, err error) {
	ctx, b, source, from, parameters, schema := in.Context, in.Binding, in.Source, in.From, in.Parameters, in.Schema
	defer source.Close()
	source.Host = h
	if err := BeforeStatement(h, b, stored.TriggerUpdate); err != nil {
		if from != nil {
			from.Close()
		}
		return Outcome{}, err
	}
	if err := RegisterAfterStatement(h, b, stored.TriggerUpdate); err != nil {
		if from != nil {
			from.Close()
		}
		return Outcome{}, err
	}
	defer CompleteAfterStatement(h, b, stored.TriggerUpdate, &outcome, &err)
	_, lockedStorage := h.Target()
	virtual := b.Relation.Partition != nil && b.Storage == nil && lockedStorage == nil && h.Command().PartitionInsert != nil && h.Command().PartitionInsert.Target.Address() == b.Ref.Address
	if !virtual && (b.Storage == nil || b.Storage != lockedStorage || b.Storage != source.View.Data().GenerationAt(b.Ref.Address)) {
		if from != nil {
			from.Close()
		}
		return Outcome{}, ids.ErrCatalogReplaced
	}
	roles := exec.DMLObservationOf(ctx)
	targetActual := roles.Role(exec.DMLTargetScan)
	pairActual := roles.Role(exec.DMLCandidateJoin)
	predicateActual := roles.Role(exec.DMLPredicate)
	var positioned MutationRowIdentity
	if b.CurrentOf != "" {
		var err error
		locator := roles.Role(exec.DMLCursorLocator)
		locator.BeginLoop()
		locator.StartWork()
		positioned, err = h.PositionedRow(b)
		locator.EndWork()
		if err != nil {
			if from != nil {
				from.Close()
			}
			return Outcome{}, err
		}
		locator.Emit()
	}
	inputs := source
	if from != nil {
		if err := exec.SelectQuerySources(ctx, from); err != nil {
			return Outcome{}, err
		}
		from = view.FreezeWriteSources(from, b.QuerySources, h.QueryAuthority(), h)
		inputs = from
		defer from.Close()
	}
	rawDemand, err := captureMutationXmax(h, b, source, inputs)
	if err != nil {
		return Outcome{}, err
	}
	input, outputEnv := h.Environments()
	state := exec.State{Source: inputs, SourceMode: exec.SourceReusable, Parameters: parameters, Input: input, Output: outputEnv, Locking: inputs.Locks != nil}
	state.Methods = h.AdmittedPlannerMethods()
	state.CapturePlannerAdmission()
	defer state.ReleaseMemberships()
	defer outcome.finishSources(&state, &err)
	eval := state.Evaluation(ctx)
	eval.DefaultOccurrence = in.DefaultOccurrence
	eval.PhysicalRawXmax = source.RawXmax
	var smallUpdateOutputs [4]sqltype.Value
	if b.Update.OutputWidth != 0 {
		if b.Update.OutputWidth <= len(smallUpdateOutputs) {
			eval.UpdateOutputScratch = smallUpdateOutputs[:b.Update.OutputWidth]
		} else {
			eval.UpdateOutputScratch = make([]sqltype.Value, b.Update.OutputWidth)
		}
	}
	var stage exec.Stage
	var frame []sqltype.Value
	var updated map[MutationRowIdentity]bool
	sourceCount := 1
	if b.Update.From != nil {
		state.IdentityReads = exec.QueryHasIdentityReads(b.Update.From) || exec.BoundHasIdentityReads(b.Update.Predicate)
		for _, expression := range b.Update.Assignments {
			state.IdentityReads = state.IdentityReads || exec.BoundHasIdentityReads(expression)
		}
		for _, target := range b.Update.ArrayTargets {
			state.IdentityReads = state.IdentityReads || exec.AssignmentTargetHasIdentityReads(target)
		}
		if b.Returning != nil {
			for _, expression := range b.Returning.Expressions {
				state.IdentityReads = state.IdentityReads || exec.BoundHasIdentityReads(expression)
			}
		}
		stage, err = exec.OpenNode(ctx, &state, b.Update.From)
		if err != nil {
			return Outcome{}, err
		}
		stage, err = exec.SettlePendingQueryMarks(ctx, &state, b.Update.From, stage)
		if err != nil {
			return Outcome{}, err
		}
		defer outcome.finishStage(&stage, &err)
		if err := stage.Drain(ctx); err != nil {
			return Outcome{}, err
		}
		sourceCount = stage.Count()
		if sourceCount != 0 {
			frame = make([]sqltype.Value, len(b.Relation.Columns)+b.Update.SourceWidth)
			updated = make(map[MutationRowIdentity]bool)
		}
	}
	var output *exec.FinalRows
	keep := false
	if b.Returning != nil {
		output = exec.NewFinalRows(b.Returning.Schema, 0)
		output.Output = outputEnv
		defer func() {
			if !keep {
				output.Close()
			}
		}()
	}
	// The candidate set is frozen before the first row wait. Rechecks can remove
	// candidates but never discover rows that did not qualify in this snapshot.
	var candidates []UpdateCandidate
	scan := exec.NewRelationScan(source)
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	targetActual.BeginLoop()
	pairActual.BeginLoop()
	predicateActual.BeginLoop()
	scan.Reach(0)
	for i := range source.Count() {
		if err := ctx.Err(); err != nil {
			return Outcome{}, err
		}
		scan.Reach(i)
		targetActual.StartWork()
		scan.Fetch(i)
		physical := MutationPhysicalRow(source, i, b)
		exec.ObserveLogicalAccess(ctx)
		targetActual.Emit()
		targetActual.EndWork()
		old := physical.Version
		if b.CurrentOf != "" && !MutationPositionMatches(positioned, physical, source) {
			continue
		}
		if sourceCount == 0 {
			continue
		}
		eval.Row = physical.Values(old)
		eval.PhysicalTable, eval.PhysicalVersion = physical.Leaf.Ref, old
		visible, _, err := exec.EvaluateRowSecurity(h.Command().RLS.Old, &eval, eval.Row)
		if err != nil {
			return Outcome{}, err
		}
		if !visible {
			continue
		}
		for j := range sourceCount {
			if err := ctx.Err(); err != nil {
				return Outcome{}, err
			}
			pairActual.StartWork()
			eval.Row = physical.Values(old)
			eval.PhysicalTable, eval.PhysicalVersion = physical.Leaf.Ref, old
			if b.Update.From != nil {
				copy(frame, eval.Row)
				copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(j)))
				eval.Row = frame
				eval.BindQueryIdentity(stage.Source, stage.Index(j))
			}
			pairActual.Emit()
			pairActual.EndWork()
			predicateActual.StartWork()
			matches, err := exec.UpdateMatches(&eval, b.Update.Predicate)
			predicateActual.EndWork()
			if err != nil {
				return Outcome{}, err
			}
			if matches {
				predicateActual.Emit()
				candidates = append(candidates, UpdateCandidate{RowVersion: old, Slot: physical.Hint, SourceIndex: j, Physical: physical})
			}
		}
	}
	scan.Reach(source.Count())
	type physicalBatch struct {
		ref    ids.ObjectRef
		old    []storage.RowVersion
		rows   index.WriteBatch
		staged int
		prior  map[ids.VersionID]struct{}
	}
	var single physicalBatch
	single.ref = b.Ref
	if source.Inheritance == nil {
		capacity := min(len(candidates), source.Count())
		single.old = make([]storage.RowVersion, 0, capacity)
		single.rows.Values = make([][]sqltype.Value, 0, capacity)
	}
	batches := []physicalBatch(nil)
	if source.Inheritance != nil && len(candidates) != 0 {
		batches = make([]physicalBatch, len(source.Inheritance))
		for i, segment := range source.Inheritance {
			batches[i].ref = segment.Leaf.Ref
			capacity := min(len(candidates), segment.Prefix.Count())
			batches[i].old = make([]storage.RowVersion, 0, capacity)
			batches[i].rows.Values = make([][]sqltype.Value, 0, capacity)
		}
	}
	var accepted []storage.RowVersion
	if returningHasPhysicalHeaders(b.Returning) {
		accepted = make([]storage.RowVersion, 1)
	}
	var count int64
candidateLoop:
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return Outcome{}, err
		}
		ref := candidate.Physical.Leaf.Ref
		// BEFORE prelocking demands the source targetlist before it can
		// wait. Ordinary admission keeps its existing grant-before-demand
		// boundary; neither path replays one pair merely for an own winner.
		eval.Row = candidate.Physical.Values(candidate.RowVersion)
		eval.PhysicalTable, eval.PhysicalVersion = ref, candidate.RowVersion
		eval.PhysicalXmaxValid, eval.OldPhysicalXmaxValid = false, false
		if b.Update.From != nil {
			copy(frame, eval.Row)
			copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(candidate.SourceIndex)))
			eval.Row = frame
			eval.BindQueryIdentity(stage.Source, stage.Index(candidate.SourceIndex))
		}
		prelock := HasRowTriggers(candidate.Physical.Leaf.Relation, stored.TriggerUpdate, stored.TriggerBefore)
		var initialRow []sqltype.Value
		initialReady := false
		if prelock {
			initialRow, err = EvaluateUpdateAssignments(&eval, b.Update, b.Targets, candidate.Physical.Values(candidate.RowVersion))
			if err != nil {
				return Outcome{}, err
			}
			initialReady = true
		}
		identity := MutationRowIdentity{Ref: ref, Row: candidate.Row, Version: candidate.Version}
		beforeDone, late := false, false
		beforeDeleteDone := false
		mode := lock.ForNoKeyUpdate
		if prelock {
			var layout []int
			if candidate.Physical.Segment != nil {
				layout = candidate.Physical.Segment.Layout
			}
			mode, err = updatePrelockMode(h, ref, b.Targets, layout)
			if err != nil {
				return Outcome{}, err
			}
		}
		request := UpdateRowRequest(ref, candidate)
		request.GrantMode = mode
	retryCandidate:
		for {
			if err := h.AcquireUpdateRow(ref, candidate.Physical.Version, mode); err != nil {
				return Outcome{}, err
			}
			if !initialReady {
				initialRow, err = EvaluateUpdateAssignments(&eval, b.Update, b.Targets, candidate.Physical.Values(candidate.RowVersion))
				if err != nil {
					return Outcome{}, err
				}
				initialReady = true
			}
			// Each eligible FROM pair demands SET once, including losing
			// pairs, but its already accepted target is not external EPQ.
			if b.Update.From != nil && updated[identity] {
				continue candidateLoop
			}
			latest, err := h.LatestRow(request)
			old, live := latest.Version, latest.Live
			if err != nil {
				return Outcome{}, err
			}
			if !live {
				continue candidateLoop
			}
			externalChange := old.Version != candidate.Version && (old.Born != 0 || old.ForcedFrozen)
			oldValues := candidate.Physical.Values(candidate.RowVersion)
			if externalChange || late {
				oldValues = candidate.Physical.Values(old)
			}
			eval.Row = oldValues
			eval.PhysicalTable, eval.PhysicalVersion = ref, old
			eval.OldPhysicalTable, eval.OldPhysicalVersion = ref, old
			eval.PhysicalXmaxValid, eval.OldPhysicalXmaxValid = false, false
			eval.OldRow = nil
			if b.Update.From != nil {
				copy(frame, oldValues)
				copy(frame[len(b.Relation.Columns):], stage.Source.Row(stage.Index(candidate.SourceIndex)))
				eval.Row = frame
				eval.BindQueryIdentity(stage.Source, stage.Index(candidate.SourceIndex))
			}
			if externalChange || prelock {
				if err := h.PhysicalRowLock(ref, old, mode); err != nil {
					return Outcome{}, err
				}
			}
			if err := exec.CaptureReturningXmax(&eval, rawDemand); err != nil {
				return Outcome{}, err
			}
			if externalChange || late {
				matches, err := MutationMatches(h, &eval, b.Update.Predicate)
				if err != nil {
					return Outcome{}, err
				}
				if !matches {
					continue candidateLoop
				}
			}
			// RHS and bounds see the unchanged old frame; partial writes compose
			// only in this private replacement. Losing FROM pairs still demand SET.
			row := initialRow
			if externalChange || late {
				row, err = EvaluateUpdateAssignments(&eval, b.Update, b.Targets, oldValues)
				if err != nil {
					return Outcome{}, err
				}
			}
			physicalRow := ScatterMutationRow(candidate.Physical, old, row, b.Targets)
			if !beforeDone {
				before, failure := BeforeMutationUpdate(h, b, candidate.Physical, old, row, physicalRow)
				if failure != nil {
					return Outcome{}, failure
				}
				beforeDone = true
				if !before.Proceed {
					continue candidateLoop
				}
				physicalRow = before.Row
			}
			destination, physicalRow, err := prepareUpdateRow(h, b, candidate.Physical, schema, physicalRow, row, &eval)
			if err != nil {
				return Outcome{}, err
			}
			keyChanged := destination != nil
			if !keyChanged {
				keyChanged, err = UniqueKeyChanged(h, ref, old.Values, physicalRow)
			}
			if err != nil {
				return Outcome{}, err
			}
			if keyChanged {
				if err := h.AcquireUpdateRow(ref, old, lock.ForUpdate); err != nil {
					return Outcome{}, err
				}
				strongRequest := request
				strongRequest.GrantMode = lock.ForUpdate
				latest, err := h.LatestRow(strongRequest)
				if err != nil {
					return Outcome{}, err
				}
				if !latest.Live {
					continue candidateLoop
				}
				if latest.Version.Version != old.Version {
					late = true
					continue retryCandidate
				}
			}
			if destination != nil {
				moved, err := moveUpdateRow(h, b, candidate.Physical, old, destination, physicalRow, row, &eval, accepted, &beforeDeleteDone)
				if errors.Is(err, ErrTupleRecheck) {
					late = true
					continue retryCandidate
				}
				if err != nil {
					return Outcome{}, err
				}
				if !moved {
					continue candidateLoop
				}
			} else {
				if err := ClaimUniqueRow(ctx, h, ref, old); err != nil {
					return Outcome{}, err
				}
				old.PendingKeyChange = old.PendingKeyChange || keyChanged
				batch := &single
				for i := range batches {
					if batches[i].ref == ref {
						batch = &batches[i]
						break
					}
				}
				batch.old = append(batch.old, old)
				actionMode := lock.MultiNoKeyAction
				if keyChanged {
					actionMode = lock.MultiUpdateAction
				}
				proposal := index.WriteCandidate{Values: physicalRow}
				if err := CheckUniqueRow(ctx, h, ref, &proposal, batch.old, batch.rows); err != nil {
					return Outcome{}, err
				}
				if err := CheckViewRow(h, &b, row, &eval); err != nil {
					return Outcome{}, err
				}
				if candidate.Physical.Segment != nil && candidate.Physical.Segment.Layout != nil {
					for i, slot := range candidate.Physical.Segment.Layout {
						row[i] = physicalRow[slot]
					}
				}
				batch.rows.Append(proposal)
				one := batch.rows.Slice(len(batch.old)-1, len(batch.old))
				one.ActionMode = actionMode
				one.PredecessorHint, one.PredecessorHintKnown = latest.Hint, latest.Hint >= 0
				if accepted != nil {
					one.Accepted = accepted
				}
				// Other action producers can have staged this physical target already.
				// Index only their prefix/additions; our accepted targets are distinct
				// and appending never mutates a prefix retained by an earlier view.
				existing := h.PrivateRows(ref.Address).Staged
				if len(existing) < batch.staged {
					batch.staged = 0
				}
				for _, predecessor := range existing[batch.staged:] {
					if batch.prior == nil {
						batch.prior = make(map[ids.VersionID]struct{})
					}
					batch.prior[predecessor.Version] = struct{}{}
					for prior := predecessor.PrivateHistory; prior != nil; prior = prior.Next {
						batch.prior[prior.Version.Version] = struct{}{}
					}
				}
				_, overlap := batch.prior[old.Version]
				if overlap {
					_, err = ReplaceRows(ctx, h, ref, batch.old[len(batch.old)-1:], one, ReplaceMerge)
				} else {
					_, err = ReplaceRows(ctx, h, ref, batch.old[len(batch.old)-1:], one, ReplaceAppend)
				}
				if errors.Is(err, ErrTupleRecheck) {
					DiscardExclusion(h, &proposal)
					batch.old = batch.old[:len(batch.old)-1]
					batch.rows = batch.rows.Slice(0, len(batch.rows.Values)-1)
					late = true
					continue retryCandidate
				}
				if err != nil {
					return Outcome{}, err
				}
				if accepted != nil {
					eval.PhysicalVersion = accepted[0]
					if accepted[0].PrivateHistory != nil {
						eval.OldPhysicalVersion = accepted[0].PrivateHistory.Version
					}
					eval.OldPhysicalVersion.DeleteFullXID = accepted[0].InsertFullXID
				}
				batch.staged = len(h.PrivateRows(ref.Address).Staged)
				if ref != b.Ref && NeedsTransition(b.Relation, stored.TriggerUpdate) {
					if err := captureTransition(h, b, stored.TriggerUpdate, candidate.Physical.Values(old), row); err != nil {
						return Outcome{}, err
					}
				}
			}
			if output != nil {
				eval.Row, eval.OldRow = row, oldValues
				if b.Update.From != nil {
					copy(frame, row)
					eval.Row = frame
				}
				if err := exec.CaptureReturningXmax(&eval, returningReadsXmax(b.Returning)); err != nil {
					return Outcome{}, err
				}
				for i, expr := range b.Returning.Expressions {
					value, err := expr.Eval(&eval)
					if err != nil {
						return Outcome{}, err
					}
					if value.Type() == nil || !value.Type().Concrete() || value.Type() != output.Columns[i].Usage.Type {
						return Outcome{}, errors.New("stuntgres: invalid finalized UPDATE value")
					}
					output.Values = append(output.Values, value)
				}
				output.Count++
				if err := h.Command().CapturedRow(); err != nil {
					return Outcome{}, err
				}
			}
			count++
			if b.Update.From != nil {
				updated[identity] = true
			}
			break
		}
	}
	if output != nil && b.Returning.RegclassOutput && output.Count != 0 {
		if err := h.CaptureRegclassOutput(output); err != nil {
			return Outcome{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	keep = true
	if output != nil {
		return Outcome{Count: count, Rows: output}, nil
	}
	return Outcome{Count: count}, nil
}
