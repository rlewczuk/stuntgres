package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// rewriteQueryRows borrows the live lexical scope for its entire lifetime.
// Only the nested rewrite consumer sees this demand; outer SELECT publication
// stays eager. Planning/open errors and row-demand errors remain distinct.
type rewriteQueryRows struct {
	state  exec.State
	stage  exec.Stage
	schema []catalog.Column
	ctx    context.Context
	closed bool
}

func openRewriteQueryRows(ctx context.Context, root plan.Node, source *exec.Source, parameters []sqltype.Value) (exec.RowResult, error) {
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		source.Close()
		return nil, fmt.Errorf("stuntgres: rewrite query lacks projection")
	}
	r := &rewriteQueryRows{schema: project.Schema(), ctx: ctx}
	r.state = exec.State{Source: source, SourceMode: exec.SourceReusable, Parameters: parameters, Locking: source.Locks != nil, HashFilter: exec.HashQueryFilter(root)}
	if scope := scopeOf(source.LiveHost()); scope != nil {
		r.state.Input, r.state.Output = scope.input, scope.output
	}
	stage, err := exec.OpenQueryProjectStage(ctx, &r.state, project)
	if err != nil {
		r.state.ReleaseMemberships()
		_ = exec.FinishForeignSources(ctx, source)
		source.Close()
		return nil, err
	}
	r.stage = stage
	return r, nil
}
func (r *rewriteQueryRows) Schema() []catalog.Column { return r.schema }
func (r *rewriteQueryRows) Next(ctx context.Context, row []sqltype.Value) (bool, error) {
	if r.closed {
		return false, fmt.Errorf("stuntgres: closed rewrite query rows")
	}
	return r.stage.NextProjected(ctx, row)
}
func (r *rewriteQueryRows) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	err := r.stage.Close()
	if failure := exec.FinishForeignSources(r.ctx, r.state.Source); err == nil {
		err = failure
	}
	r.state.ReleaseMemberships()
	r.state.Source.Close()
	return err
}
func (r *rewriteQueryRows) RegclassText(value sqltype.Value) (string, error) { return value.Text() }
func (r *rewriteQueryRows) OutputEnvironment() sqltype.OutputEnvironment     { return r.state.Output }

var _ exec.RowResult = (*rewriteQueryRows)(nil)
