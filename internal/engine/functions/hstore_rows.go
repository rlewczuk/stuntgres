package functions

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// HstoreRows retains a selected declaration, never execution authority or state.
type HstoreRows struct {
	expr.Info
	Arg expr.Expr
	Formal *sqltype.Descriptor
	Plan sqltype.HstoreRowsPlan
	Ref ids.ObjectRef
	Member uint8
	Namespace, Name string
}

func (e HstoreRows) Validate() error {
	if e.Arg == nil || e.Formal == nil || !e.Formal.IsHstore() || e.Arg.ResultUsage().Type != e.Formal || e.RoutineReference == nil || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.ObjectID == 0 || e.Ref.Address.SubID != 0 || e.Namespace == "" || e.Name == "" || e.Plan.ResultUsage() != e.Usage {
		return fmt.Errorf("stuntgres: invalid installed hstore rows call")
	}
	// Installed roles are append-only; kinds follow the three selected roles.
	if e.Plan.Kind() > sqltype.HstoreRowPairs || e.Member != 54+uint8(e.Plan.Kind()) {
		return fmt.Errorf("stuntgres: invalid installed hstore rows member")
	}
	_, err := sqltype.BindHstoreRows(e.Plan.Kind(), e.Arg.ResultUsage(), e.Usage)
	return err
}
func (e HstoreRows) Children() []expr.Expr { return []expr.Expr{e.Arg} }
func (e HstoreRows) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("HstoreRows", children, 1); err != nil { return nil, err }
	e.Info, e.Arg = info, children[0]
	plan, err := sqltype.BindHstoreRows(e.Plan.Kind(), e.Arg.ResultUsage(), info.Usage)
	if err != nil { return nil, err }
	e.Plan = plan
	return e, e.Validate()
}
func (e HstoreRows) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) { return e.Rebuild(info, children) }
func (e HstoreRows) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	formal := env.Type(e.Formal)
	changed = changed || formal != e.Formal
	e.Formal = formal
	next, err := e.Rebuild(info, children)
	return next, changed, err
}
func (e HstoreRows) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil { return sqltype.Value{}, err }
	return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted hstore rows set call")
}
func (e HstoreRows) SourceAdmission() (bool, bool) { return true, true }
func (e HstoreRows) SameSourceOccurrence(other expr.Expr) bool {
	o, ok := other.(HstoreRows)
	return ok && e.Ref == o.Ref && e.Member == o.Member && e.Plan.Kind() == o.Plan.Kind() && sqltype.SameRecordUsage(e.Usage, o.Usage)
}
func (e HstoreRows) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	ctx, err := evaluationContext(c)
	if err != nil { return expr.SourceCursor{}, err }
	value, err := e.Arg.Eval(c)
	if err != nil { return expr.SourceCursor{}, err }
	cursor, err := e.Plan.Open(ctx, value)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.NewSourceCursor(expr.SourceCursor{Type: e.Usage.Type, State: &cursor, Advance: nextHstoreRowsSource, Release: closeHstoreRowsSource}), nil
}
func nextHstoreRowsSource(ctx context.Context, source *expr.SourceCursor) (sqltype.Value, bool, error) { return source.State.(*sqltype.HstoreRowsCursor).Next(ctx) }
func closeHstoreRowsSource(state any) { state.(*sqltype.HstoreRowsCursor).Close() }
func (e HstoreRows) ValidateStoredProvider() error { return e.Validate() }
func (e HstoreRows) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e HstoreRows) LooksLikeFunction() bool { return true }
func (e HstoreRows) ClosureTransparent() {}
func (e HstoreRows) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction { return expr.CaptureOperands{}, false, nil }
	return expr.CaptureOperands{Args: e.Children(), Count: 1}, true, nil
}
func (e HstoreRows) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(HstoreRows)
	return ok && e.Formal == o.Formal && e.Ref == o.Ref && e.Member == o.Member && e.Plan.Kind() == o.Plan.Kind() && e.RoutineReference == o.RoutineReference && same(e.Arg, o.Arg)
}
func (e HstoreRows) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil { return expr.Rendering{}, err }
	return expr.Rendering{Tree: "HstoreRows " + e.Namespace + "." + e.Name, Inputs: e.Children(), Form: expr.FormCall, Name: e.Name, Namespace: e.Namespace, Quote: expr.QuoteIdentifier, Args: []expr.RenderArg{{Expr: e.Arg, Transport: expr.TransportCast, TransportType: e.Formal.Usage()}}, Replay: expr.Replay{QuoteNamespace: true}}, nil
}
