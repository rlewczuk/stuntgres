package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// ViewTriggerCommand owns only this activation's SELECT inputs. No row identity,
// storage capability or trigger frame can be retained by its analyzed binding.
type ViewTriggerCommand struct {
	Binding        analyze.Binding
	TargetRoot     plan.Node
	Target, Inputs *exec.Source
	Parameters     []sqltype.Value
	Evaluation     exec.Context
	QueryRoot      plan.Node
	Catalog        catalog.Read
	Expressions    [][]expr.Expr
}

func ExecuteViewTrigger(h Host, in ViewTriggerCommand) (outcome Outcome, err error) {
	b, inputs := in.Binding, in.Inputs
	defer func() {
		inputs.Close()
		if in.Target != nil {
			in.Target.Close()
		}
	}()
	event := stored.TriggerInsert
	if b.Update != nil {
		event = stored.TriggerUpdate
	} else if in.Target != nil {
		event = stored.TriggerDelete
	}
	if err = RegisterAfterStatement(h, b, event); err != nil {
		return Outcome{}, err
	}
	defer CompleteAfterStatement(h, b, event, &outcome, &err)
	input, output := h.Environments()
	state := &exec.State{
		Source: inputs, SourceMode: exec.SourceReusable, Parameters: in.Parameters,
		Input: input, Output: output, Ctes: h.CTEInvocation(), Locking: inputs.Locks != nil,
	}
	defer state.ReleaseMemberships()
	defer outcome.finishSources(state, &err)
	if err = exec.SelectQuerySources(h.Context(), inputs); err != nil {
		return Outcome{}, err
	}
	inputs = view.FreezeWriteSources(inputs, b.QuerySources, h.QueryAuthority(), h)
	state.Source = inputs
	evaluation := in.Evaluation
	evaluation.Query, evaluation.Scope, evaluation.Frame, evaluation.Host = state, 1, 1, h
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
	var count int64
	accept := func(old, row, frame []sqltype.Value) error {
		evaluation.Row = frame
		request := BeforeRow{Relation: b.Ref, Event: BeforeEvent(event), Old: old, New: row, Instead: true}
		result, failure := h.BeforeRow(request)
		if failure != nil {
			return failure
		}
		if !result.Proceed {
			return nil
		}
		actual := result.Row
		if event == stored.TriggerDelete {
			actual = old
		} else if failure = CheckViewRow(h, &b, actual, &evaluation); failure != nil {
			return failure
		}
		if failure = ProjectReturning(h, b, rows, actual, old, &evaluation); failure != nil {
			return failure
		}
		count++
		return nil
	}
	if event == stored.TriggerInsert {
		err = viewTriggerInsert(h, in, state, &evaluation, func(row []sqltype.Value) error {
			return accept(nil, row, row)
		})
	} else {
		err = viewTriggerCandidates(h, in, state, &evaluation, accept)
	}
	if err != nil {
		return Outcome{}, err
	}
	result, failure := finishMutationResult(h, b, rows, count)
	keep = failure == nil
	return result, failure
}
