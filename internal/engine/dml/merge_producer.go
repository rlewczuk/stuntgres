package dml

import (
	"context"
	"errors"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Embedded in the one execution-private MergeRows allocation. Sources are
// already materialized owned vectors; there is no cursor, borrowed row scratch,
// retained callback, prepared authority or second statement invocation here.
// The caller keeps Target/Inputs/State alive through its explicit Stage boundary.
type mergeProducer struct {
	owner                                  *MergeRows
	binding                                analyze.Binding
	catalog                                catalog.Read
	schema                                 string
	targetRaw                              map[ids.VersionID]uint32
	state                                  *exec.State
	pairs                                  []MergeCandidate
	sources, nullSource, frame             []sqltype.Value
	width                                  int
	occurrences, provenance, presenceSlots []int
	presence                               []bool
	projected                              [][]storage.RowVersion
	raw                                    []map[ids.VersionID]uint32
	presenceCount                          int
	evaluation                             exec.Context
	unique, physicalReturning              bool
	output                                 *MergeReturningOutput
	accepted                               map[ids.VersionID]bool
	excluded                               []storage.RowVersion
}

func (p *mergeProducer) producePair(ctx context.Context, h Host, rows *MergeRows, output *MergeReturningOutput, pairIndex int, late, acceptNow bool) error {
	b, candidate := p.binding, p.pairs[pairIndex]
retryPair:
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Rebuild the lexical frame at its final embedded address. The same
		// State retains hoisted/materialized input lifetimes across EPQ.
		p.evaluation = p.state.Evaluation(ctx)
		evaluation := &p.evaluation
		evaluation.Host = h
		evaluation.PhysicalRawXmax = p.targetRaw
		evaluation.QueryOccurrences = p.occurrences
		evaluation.PresenceSlots = p.presenceSlots
		source := p.nullSource
		if candidate.SourceIndex >= 0 {
			source = p.sources[candidate.SourceIndex*p.width : (candidate.SourceIndex+1)*p.width]
			copy(p.frame[b.Merge.TargetWidth:], source)
			if p.projected != nil {
				evaluation.QueryProjectedVersions = p.projected[candidate.SourceIndex]
			}
			if p.raw != nil {
				evaluation.QueryRawXmax = p.raw[candidate.SourceIndex]
			}
			if p.state.IdentityReads {
				width := len(p.occurrences)
				evaluation.QueryProvenance = p.provenance[candidate.SourceIndex*width : (candidate.SourceIndex+1)*width]
			}
			if p.presenceCount != 0 {
				evaluation.Presence = p.presence[candidate.SourceIndex*p.presenceCount : (candidate.SourceIndex+1)*p.presenceCount]
			}
		}
		old, class := candidate.RowVersion, candidate.Class
		sourceAfter := false
		setFrame := func() {
			evaluation.PhysicalTable, evaluation.PhysicalVersion = b.Ref, old
			evaluation.OldPhysicalTable, evaluation.OldPhysicalVersion = b.Ref, old
			evaluation.PhysicalXmaxValid, evaluation.OldPhysicalXmaxValid = false, false
			switch class {
			case parser.MergeMatched:
				copy(p.frame, old.Values)
				evaluation.Row = p.frame
			case parser.MergeNotMatchedBySource:
				evaluation.Row = old.Values
			case parser.MergeNotMatched:
				evaluation.Row = source
			}
		}
		// Late MERGE resumes its matched-action selection, not the ordinary
		// UPDATE/DELETE below-prologue retry boundary.
		if late && class != parser.MergeNotMatched {
			strong, err := mergeFollowChainStrong(h, b)
			if err != nil {
				return err
			}
			request := MergeRowRequest(b.Ref, candidate.UpdateCandidate)
			if strong {
				request.GrantMode = lock.ForUpdate
				if err := h.AcquireUpdateRow(b.Ref, old, lock.ForUpdate); err != nil {
					return err
				}
			}
			current, err := h.LatestRow(request)
			if err != nil {
				return err
			}
			if !current.Live {
				class = parser.MergeNotMatched
			} else {
				old = current.Version
			}
		}
		setFrame()
		if class == parser.MergeMatched && (late || b.Merge.HasBySource) {
			matched, err := MergeMatches(evaluation, b.Merge.On)
			if err != nil {
				return err
			}
			if !matched {
				class = parser.MergeNotMatched
				if b.Merge.HasBySource {
					class, sourceAfter = parser.MergeNotMatchedBySource, b.Merge.HasByTarget
				}
				setFrame()
			}
		}
		arm, err := SelectMergeArm(b.Merge, evaluation, class)
		if err != nil {
			return err
		}
		initialArm, initialVersion := arm, old.Version
		var projected []sqltype.Value
		if arm != nil && arm.Action != parser.MergeNothingAction {
			projected, err = projectMergeRow(b, p.catalog, arm, old, evaluation)
			if err != nil {
				return err
			}
		}
		if class != parser.MergeNotMatched && !late && arm != nil && arm.Action != parser.MergeNothingAction {
			mode := lock.ForNoKeyUpdate
			if arm.Action == parser.MergeDeleteAction {
				mode = lock.ForUpdate
			}
			if arm.Action == parser.MergeUpdateAction && HasRowTriggers(b.Relation, stored.TriggerUpdate, stored.TriggerBefore) {
				mode, err = updatePrelockMode(h, b.Ref, arm.Input.Targets, nil)
				if err != nil {
					return err
				}
			}
			if err := h.AcquireUpdateRow(b.Ref, old, mode); err != nil {
				return err
			}
			request := MergeRowRequest(b.Ref, candidate.UpdateCandidate)
			request.GrantMode = mode
			current, err := h.LatestRow(request)
			if err != nil {
				return err
			}
			if current.Live && current.Version.Version != initialVersion {
				strong, err := mergeFollowChainStrong(h, b)
				if err != nil {
					return err
				}
				if strong && mode != lock.ForUpdate {
					if err := h.AcquireUpdateRow(b.Ref, current.Version, lock.ForUpdate); err != nil {
						return err
					}
					mode, request.GrantMode = lock.ForUpdate, lock.ForUpdate
					current, err = h.LatestRow(request)
					if err != nil {
						return err
					}
				}
				if current.Live {
					if err := h.PhysicalRowLock(b.Ref, current.Version, mode); err != nil {
						return err
					}
				}
			}
			if !current.Live {
				class = parser.MergeNotMatched
			} else if current.Version.Version != old.Version {
				old = current.Version
				setFrame()
				if class == parser.MergeMatched {
					matched, err := MergeMatches(evaluation, b.Merge.On)
					if err != nil {
						return err
					}
					if !matched {
						class = parser.MergeNotMatched
						if b.Merge.HasBySource {
							class, sourceAfter = parser.MergeNotMatchedBySource, b.Merge.HasByTarget
						}
					}
				}
			}
			if !current.Live || current.Version.Version != initialVersion {
				setFrame()
				arm, err = SelectMergeArm(b.Merge, evaluation, class)
				if err != nil {
					return err
				}
			}
		}
		for {
			if arm != nil && arm.Action != parser.MergeNothingAction {
				if arm != initialArm || old.Version != initialVersion {
					projected, err = projectMergeRow(b, p.catalog, arm, old, evaluation)
					if err != nil {
						return err
					}
				}
				beforeActions := len(rows.Actions)
				var pending *index.WriteBatch
				if rows != p.owner {
					pending = &p.owner.WriteBatch
				}
				if err := appendMergeAction(h, b, rows, pending, evaluation, p.schema, p.unique, arm, old, candidate.Deletion, projected, p.accepted, &p.excluded); err != nil {
					if errors.Is(err, ErrTupleRecheck) {
						late = true
						continue retryPair
					}
					return err
				}
				for i := beforeActions; i < len(rows.Actions); i++ {
					rows.Actions[i].PairIndex = pairIndex
				}
				if acceptNow && (b.Relation.HasTriggers() || p.physicalReturning) {
					_, err := stageMergeWindow(ctx, h, rows, b.Ref)
					if errors.Is(err, ErrTupleRecheck) {
						p.discardPair(h, rows, pairIndex)
						late = true
						continue retryPair
					}
					if err != nil {
						return err
					}
				}
				if output != nil && len(rows.Actions) > beforeActions {
					actualOld := old.Values
					var row []sqltype.Value
					if arm.Action != parser.MergeDeleteAction {
						row = rows.Values[len(rows.Values)-1]
					}
					if arm.Action == parser.MergeInsertAction {
						actualOld = nil
					}
					if p.physicalReturning {
						if arm.Action == parser.MergeDeleteAction {
							if err := captureDeletedHeader(h, b.Ref, old, evaluation); err != nil {
								return err
							}
						} else {
							evaluation.PhysicalTable, evaluation.PhysicalVersion = b.Ref, rows.Accepted[len(rows.Accepted)-1]
							if arm.Action == parser.MergeUpdateAction {
								evaluation.OldPhysicalVersion = evaluation.PhysicalVersion.PrivateHistory.Version
								evaluation.OldPhysicalVersion.DeleteFullXID = evaluation.PhysicalVersion.InsertFullXID
							}
						}
					}
					if err := output.Project(b, evaluation, arm.Action, actualOld, row, source, candidate.SourceIndex >= 0); err != nil {
						return err
					}
				}
			}
			if !sourceAfter {
				return nil
			}
			sourceAfter = false
			class = parser.MergeNotMatched
			setFrame()
			arm, err = SelectMergeArm(b.Merge, evaluation, class)
			if err != nil {
				return err
			}
			initialArm = nil // This second opportunity owns a distinct projection.
		}
	}
}

func projectMergeRow(b analyze.Binding, read catalog.Read, arm *analyze.MergeArm, old storage.RowVersion, evaluation *exec.Context) ([]sqltype.Value, error) {
	if arm.Action == parser.MergeDeleteAction || arm.Action == parser.MergeNothingAction {
		return nil, nil
	}
	if arm.Action == parser.MergeUpdateAction {
		return EvaluateUpdateAssignments(evaluation, arm.Input.Update, arm.Input.Targets, old.Values)
	}
	row := make([]sqltype.Value, len(b.Relation.Columns))
	for i, column := range b.Relation.Columns {
		row[i] = sqltype.Null(column.Usage.Type)
	}
	if err := EvaluateInsertValuesRow(read, &arm.Input, arm.Input.Expressions[0], arm.Syntax, row, false, evaluation); err != nil {
		return nil, err
	}
	return row, nil
}

// discardPair removes only this unaccepted source occurrence's proposals and
// cardinality/exclusion scratch. Accepted prefixes and unrelated pending RHS /
// RETURNING values are untouched. Rare splicing compacts/rebases payload slots;
// no tombstone inventory or normal-path copy is introduced.
func (p *mergeProducer) discardPair(h Host, rows *MergeRows, pairIndex int) (int, int) {
	first := rows.StagedActions
	for first < len(rows.Actions) && rows.Actions[first].PairIndex != pairIndex {
		first++
	}
	last := first
	for last < len(rows.Actions) && rows.Actions[last].PairIndex == pairIndex {
		last++
	}
	if first == last {
		return first, last
	}
	payloadFirst, payloadLast := len(rows.Values), -1
	for _, action := range rows.Actions[first:last] {
		if action.Action == parser.MergeDeleteAction {
			delete(p.accepted, action.Expected)
		} else {
			if action.Action == parser.MergeUpdateAction {
				delete(p.accepted, rows.Predecessors[action.Payload].Version)
			}
			candidate := rows.WriteBatch.Candidate(action.Payload)
			DiscardExclusion(h, &candidate)
			payloadFirst = min(payloadFirst, action.Payload)
			payloadLast = max(payloadLast, action.Payload+1)
		}
	}
	if payloadLast < 0 {
		for i := last; i < len(rows.Actions); i++ {
			if rows.Actions[i].Action != parser.MergeDeleteAction {
				payloadFirst = rows.Actions[i].Payload
				break
			}
		}
	}
	// Exclusion suppression belongs to actual predecessor identities only.
	p.excluded = slices.DeleteFunc(p.excluded, func(row storage.RowVersion) bool {
		for _, action := range rows.Actions[first:last] {
			if action.Action == parser.MergeDeleteAction && row.Version == action.Expected {
				return true
			}
			if action.Action == parser.MergeUpdateAction && row.Version == rows.Predecessors[action.Payload].Version {
				return true
			}
		}
		return false
	})
	if payloadLast >= 0 {
		rows.Values = slices.Delete(rows.Values, payloadFirst, payloadLast)
		rows.Predecessors = slices.Delete(rows.Predecessors, payloadFirst, payloadLast)
		if rows.States != nil {
			rows.States = slices.Delete(rows.States, payloadFirst, payloadLast)
		}
		if rows.Accepted != nil {
			rows.Accepted = slices.Delete(rows.Accepted, payloadFirst, payloadLast)
		}
		for i := last; i < len(rows.Actions); i++ {
			if rows.Actions[i].Action != parser.MergeDeleteAction {
				rows.Actions[i].Payload -= payloadLast - payloadFirst
			}
		}
	}
	rows.Actions = slices.Delete(rows.Actions, first, last)
	if rows.Returning != nil {
		width := len(rows.Returning.Columns)
		// Immediate acceptance has not projected its failed proposal yet.
		projectedLast := min(last, rows.Returning.Count)
		if first < projectedLast {
			rows.Returning.Values = slices.Delete(rows.Returning.Values, first*width, projectedLast*width)
			rows.Returning.Count -= projectedLast - first
		}
	}
	return first, payloadFirst
}

func (p *mergeProducer) recheck(ctx context.Context, h Host, rows *MergeRows, failed int) error {
	pairIndex := rows.Actions[failed].PairIndex
	first, payload := p.discardPair(h, rows, pairIndex)
	var scratch MergeRows
	if rows.Accepted != nil {
		scratch.Accepted = make([]storage.RowVersion, 0)
	}
	environment := p.state.Output
	if p.output != nil {
		environment = p.output.Rows.Output
	}
	output := newMergeReturningOutput(p.binding, environment)
	if output != nil {
		scratch.Returning = output.Rows
		defer scratch.Returning.Close()
	}
	if err := p.producePair(ctx, h, &scratch, output, pairIndex, true, false); err != nil {
		return err
	}
	// A DELETE-only occurrence had no payload; its replacement is inserted
	// before the next pending payload (or at the vector's end).
	if payload > len(rows.Values) {
		payload = len(rows.Values)
	}
	if first < len(rows.Actions) && rows.Actions[first].Action != parser.MergeDeleteAction {
		payload = min(payload, rows.Actions[first].Payload)
	}
	rows.Values = slices.Insert(rows.Values, payload, scratch.Values...)
	rows.Predecessors = slices.Insert(rows.Predecessors, payload, scratch.Predecessors...)
	if rows.States != nil || scratch.States != nil {
		if rows.States == nil {
			rows.States = make([]index.WriteState, len(rows.Values)-len(scratch.Values))
		}
		if scratch.States == nil {
			scratch.States = make([]index.WriteState, len(scratch.Values))
		}
		rows.States = slices.Insert(rows.States, payload, scratch.States...)
	}
	if rows.Accepted != nil {
		rows.Accepted = slices.Insert(rows.Accepted, payload, scratch.Accepted...)
	}
	for i := first; i < len(rows.Actions); i++ {
		if rows.Actions[i].Action != parser.MergeDeleteAction {
			rows.Actions[i].Payload += len(scratch.Values)
		}
	}
	for i := range scratch.Actions {
		if scratch.Actions[i].Action != parser.MergeDeleteAction {
			scratch.Actions[i].Payload += payload
		}
	}
	rows.Actions = slices.Insert(rows.Actions, first, scratch.Actions...)
	if rows.Returning != nil {
		width := len(rows.Returning.Columns)
		rows.Returning.Values = slices.Insert(rows.Returning.Values, first*width, scratch.Returning.Values...)
		rows.Returning.Count += scratch.Returning.Count
	}
	return nil
}

// A current private successor remains the same command's already modified
// target only through its genuine predecessor lineage, never through RowID.
func mergeAlreadyAccepted(accepted map[ids.VersionID]bool, row storage.RowVersion) bool {
	if accepted[row.Version] {
		return true
	}
	for prior := row.PrivateHistory; prior != nil; prior = prior.Next {
		if accepted[prior.Version.Version] {
			return true
		}
	}
	return false
}

// Only rare candidate replay needs the still-valid proposals on either side
// of the replaced occurrence. Ordinary assembly keeps its existing batch and
// allocation path; the discarded candidate is already absent from pending.
func mergeUniquePrefix(current index.WriteBatch, pending *index.WriteBatch) index.WriteBatch {
	if pending == nil {
		return current
	}
	if len(current.Values) == 0 {
		return *pending
	}
	prefix := index.WriteBatch{Values: make([][]sqltype.Value, 0, len(pending.Values)+len(current.Values))}
	prefix.Values = append(prefix.Values, pending.Values...)
	prefix.Values = append(prefix.Values, current.Values...)
	if pending.States != nil || current.States != nil {
		prefix.States = make([]index.WriteState, len(prefix.Values))
		copy(prefix.States, pending.States)
		copy(prefix.States[len(pending.Values):], current.States)
	}
	return prefix
}
