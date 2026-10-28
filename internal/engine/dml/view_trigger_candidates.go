package dml

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// Freeze complete target/source occurrences before executing any body. Equal
// tuples and repeated matches are deliberately not physical identities.
func viewTriggerCandidates(h Host, in ViewTriggerCommand, state *exec.State, evaluation *exec.Context, accept func([]sqltype.Value, []sqltype.Value, []sqltype.Value) error) (err error) {
	b := in.Binding
	input, output := h.Environments()
	targetState := &exec.State{
		Source: in.Target, SourceMode: exec.SourceBorrowed, Parameters: in.Parameters,
		Input: input, Output: output, Ctes: h.CTEInvocation(),
	}
	defer targetState.ReleaseMemberships()
	defer func() { err = targetState.FinishSources(err) }()
	target, err := exec.OpenNode(h.Context(), targetState, in.TargetRoot)
	if err != nil {
		return err
	}
	defer func() {
		if failure := target.Close(); err == nil {
			err = failure
		}
	}()
	from, predicate, sourceWidth := b.Delete.Using, b.Predicate, b.Delete.SourceWidth
	if b.Update != nil {
		from, predicate, sourceWidth = b.Update.From, b.Update.Predicate, b.Update.SourceWidth
	}
	var source exec.Stage
	sourceCount := 1
	if from != nil {
		source, err = exec.OpenNode(h.Context(), state, from)
		if err != nil {
			return err
		}
		defer func() {
			if failure := source.Close(); err == nil {
				err = failure
			}
		}()
		if err = source.Drain(h.Context()); err != nil {
			return err
		}
		sourceCount = source.Count()
	}
	type candidate struct {
		old, frame []sqltype.Value
		index      int
	}
	var candidates []candidate
	width := len(b.Relation.Columns)
	scratch := make([]sqltype.Value, width)
	frame := scratch
	if from != nil {
		frame = make([]sqltype.Value, width+sourceWidth)
	}
	for {
		// Project every view output, not its unprojected source prefix. This is
		// essential for computed, joined, aggregate and source-free OLD rows.
		present, failure := target.Next(h.Context(), scratch)
		if failure != nil {
			return failure
		}
		if !present {
			break
		}
		var old []sqltype.Value
		for j := range sourceCount {
			if from != nil {
				copy(frame, scratch)
				copy(frame[width:], source.Source.Row(source.Index(j)))
				evaluation.BindQueryIdentity(source.Source, source.Index(j))
			}
			evaluation.Row = frame
			matches, failure := exec.UpdateMatches(evaluation, predicate)
			if failure != nil {
				return failure
			}
			if !matches {
				continue
			}
			if old == nil {
				old = make([]sqltype.Value, width)
				copy(old, scratch)
			}
			ownedFrame := old
			if from != nil {
				ownedFrame = append([]sqltype.Value(nil), frame...)
			}
			candidates = append(candidates, candidate{old: old, frame: ownedFrame, index: j})
		}
	}
	for _, candidate := range candidates {
		if err = h.Context().Err(); err != nil {
			return err
		}
		evaluation.Row = candidate.frame
		if from != nil {
			evaluation.BindQueryIdentity(source.Source, source.Index(candidate.index))
		}
		var row []sqltype.Value
		if b.Update != nil {
			row, err = EvaluateUpdateAssignments(evaluation, b.Update, b.Targets, candidate.old)
			if err != nil {
				return err
			}
		}
		if err = accept(candidate.old, row, candidate.frame); err != nil {
			return err
		}
	}
	return nil
}
