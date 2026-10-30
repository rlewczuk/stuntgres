package dml

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// MergeInput is one MERGE execution: the captured target Source and query
// Inputs, the canonical parameters, the target schema name and the catalog
// the INSERT arms' value errors are presented with. ExecuteMerge closes
// Target and Inputs.
type MergeInput struct {
	Context    context.Context
	Binding    analyze.Binding
	Target     *exec.Source
	Inputs     *exec.Source
	Parameters []sqltype.Value
	Schema     string
	Catalog    catalog.Read
}

// ExecuteMerge runs MERGE after the root has captured the target and its
// sources and released Engine.mu: it completes the source, assembles every
// action in candidate order (row locks, latest reads and validation), then
// stages them.
func ExecuteMerge(h Host, in MergeInput) (outcome Outcome, err error) {
	ctx, b, target, inputs := in.Context, in.Binding, in.Target, in.Inputs
	defer target.Close()
	// Freezing can replace the bundle head; cleanup follows its current owner.
	defer func() { inputs.Close() }()
	events := StatementEvents(b, nil)
	for _, event := range [...]stored.TriggerEvent{stored.TriggerInsert, stored.TriggerUpdate, stored.TriggerDelete} {
		if events&event == 0 {
			continue
		}
		binding := b
		if event == stored.TriggerUpdate {
			binding.Targets = b.Merge.UpdateTargets
		}
		if err := BeforeStatement(h, binding, event); err != nil {
			return Outcome{}, err
		}
	}
	unique, err := BindUniqueWrite(h, b.Ref)
	if err != nil {
		return Outcome{}, err
	}
	if err := exec.SelectQuerySources(ctx, inputs); err != nil {
		return Outcome{}, err
	}
	inputs = view.FreezeWriteSources(inputs, b.QuerySources, h.QueryAuthority(), h)
	input, output := h.Environments()
	state := &exec.State{Source: inputs, SourceMode: exec.SourceReusable, Parameters: in.Parameters, Input: input, Output: output, Locking: inputs.Locks != nil, IdentityReads: MergeHasIdentityReads(b.Merge)}
	state.Methods = h.AdmittedPlannerMethods()
	state.CapturePlannerAdmission()
	if b.Returning != nil {
		for _, expr := range b.Returning.Expressions {
			state.IdentityReads = state.IdentityReads || exec.BoundHasIdentityReads(expr)
		}
	}
	defer state.ReleaseMemberships()
	defer outcome.finishSources(state, &err)
	cursor, err := exec.OpenNode(ctx, state, b.Merge.Source)
	if err != nil {
		return Outcome{}, err
	}
	rows, err := AssembleMergeRows(h, in, &cursor, state, len(unique.Keys) != 0 || unique.Checked)
	if err != nil {
		return Outcome{}, err
	}
	complete := false
	defer func() {
		if !complete && rows.Returning != nil {
			rows.Returning.Close()
		}
	}()
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	if err := StageMergeRows(ctx, h, rows, b.Ref); err != nil {
		return Outcome{}, err
	}
	if rows.Returning != nil && b.Returning.RegclassOutput && rows.Returning.Count != 0 {
		if err := h.CaptureRegclassOutput(rows.Returning); err != nil {
			return Outcome{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, err
	}
	complete = true
	return Outcome{Count: int64(len(rows.Actions)), Rows: rows.Returning}, nil
}
