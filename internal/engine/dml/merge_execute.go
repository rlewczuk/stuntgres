package dml

import (
	"context"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
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
func ExecuteMerge(h Host, in MergeInput) (Outcome, error) {
	ctx, b, target, inputs := in.Context, in.Binding, in.Target, in.Inputs
	defer target.Close()
	defer inputs.Close()
	unique, err := BindUniqueWrite(h, b.Ref)
	if err != nil {
		return Outcome{}, err
	}
	if err := exec.SelectQuerySources(ctx, inputs); err != nil {
		return Outcome{}, err
	}
	input, output := h.Environments()
	state := &exec.State{Source: inputs, Parameters: in.Parameters, Input: input, Output: output, Locking: inputs.Locks != nil, VirtualReads: MergeHasVirtualReads(b.Merge)}
	if b.Returning != nil {
		for _, expr := range b.Returning.Expressions {
			state.VirtualReads = state.VirtualReads || exec.BoundHasVirtualReads(expr)
		}
	}
	defer state.ReleaseMemberships()
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
	if err := StageMergeRows(h, rows, b.Ref); err != nil {
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
