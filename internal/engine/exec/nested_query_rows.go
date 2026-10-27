package exec

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// nestedQueryRows borrows the live lexical scope for its entire lifetime.
// Private all-row consumers share this producer; ordinary publication stays
// eager. Planning/open errors and row-demand errors remain distinct.
type nestedQueryRows struct {
	state  State
	stage  Stage
	schema []catalog.Column
	ctx    context.Context
	closed bool
}

// OpenNestedQueryRows opens the private all-row producer over a projected
// root. It owns source from the call on, and closes it on an open failure.
func OpenNestedQueryRows(ctx context.Context, root plan.Node, source *Source, parameters []sqltype.Value) (RowResult, error) {
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		source.Close()
		return nil, fmt.Errorf("stuntgres: nested query lacks projection")
	}
	AdmitScan(root, source)
	r := &nestedQueryRows{schema: project.Schema(), ctx: ctx}
	r.state = State{Source: source, SourceMode: SourceReusable, Parameters: parameters, Locking: source.Locks != nil, HashFilter: HashQueryFilter(root)}
	if host := source.LiveHost(); host != nil {
		r.state.Input, r.state.Output = host.Environments()
	}
	stage, err := OpenQueryProjectStage(ctx, &r.state, project)
	if err != nil {
		r.state.ReleaseMemberships()
		_ = FinishForeignSources(ctx, source)
		source.Close()
		return nil, err
	}
	r.stage = stage
	return r, nil
}
func (r *nestedQueryRows) Schema() []catalog.Column { return r.schema }
func (r *nestedQueryRows) Next(ctx context.Context, row []sqltype.Value) (bool, error) {
	if r.closed {
		return false, fmt.Errorf("stuntgres: closed nested query rows")
	}
	return r.stage.NextProjected(ctx, row)
}
func (r *nestedQueryRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	err := r.stage.Close()
	if failure := FinishForeignSources(r.ctx, r.state.Source); err == nil {
		err = failure
	}
	r.state.ReleaseMemberships()
	r.state.Source.Close()
	return err
}
func (r *nestedQueryRows) RegclassText(value sqltype.Value) (string, error) { return value.Text() }
func (r *nestedQueryRows) OutputEnvironment() sqltype.OutputEnvironment     { return r.state.Output }

var _ RowResult = (*nestedQueryRows)(nil)
