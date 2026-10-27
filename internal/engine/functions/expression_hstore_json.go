package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// HstoreJSON retains installed declaration identity, never caller authority.
type HstoreJSON struct {
	expr.Info
	Arg             expr.Expr
	Formal          *sqltype.Descriptor
	Loose           bool
	Ref             ids.ObjectRef
	Member          uint8
	Namespace, Name string
}

func (e HstoreJSON) Validate() error {
	if e.Arg == nil || !e.Formal.IsHstore() || e.Arg.ResultUsage().Type != e.Formal || e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.JSON || !e.Usage.Type.Concrete() || e.Usage.Type.IsDomain() || e.RoutineReference == nil || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.ObjectID == 0 || e.Ref.Address.SubID != 0 || e.Namespace == "" || e.Name == "" || e.Member != 5 && e.Member != 6 || e.Loose != (e.Member == 6) {
		return fmt.Errorf("stuntgres: invalid installed hstore JSON call")
	}
	return nil
}
func (e HstoreJSON) Children() []expr.Expr { return []expr.Expr{e.Arg} }
func (e HstoreJSON) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("HstoreJSON", children, 1); err != nil {
		return nil, err
	}
	e.Info, e.Arg = info, children[0]
	return e, e.Validate()
}
func (e HstoreJSON) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e HstoreJSON) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	formal := env.Type(e.Formal)
	changed = changed || formal != e.Formal
	e.Formal = formal
	next, err := e.Rebuild(info, children)
	return next, changed, err
}
func (e HstoreJSON) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if c.FunctionLiteralNull(e.Arg) {
		return sqltype.Null(e.Usage.Type), nil
	}
	value, err := e.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return value.HstoreJSON(ctx, e.Formal, e.Usage.Type, e.Loose)
}
func (e HstoreJSON) ValidateStoredProvider() error        { return e.Validate() }
func (e HstoreJSON) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e HstoreJSON) PlanningFold() expr.Fold              { return expr.FoldConstant }
func (e HstoreJSON) StrictNull() expr.Strict              { return expr.StrictAfter }
func (e HstoreJSON) PreflightFold() expr.Preflight        { return expr.PreflightRegexNullOrEvaluate }
func (e HstoreJSON) LooksLikeFunction() bool              { return true }
func (e HstoreJSON) ClosureTransparent()                  {}
func (e HstoreJSON) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: e.Children(), Count: 1}, true, nil
}
func (e HstoreJSON) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(HstoreJSON)
	return ok && e.Formal == o.Formal && e.Loose == o.Loose && e.Ref == o.Ref && e.Member == o.Member && e.RoutineReference == o.RoutineReference && same(e.Arg, o.Arg)
}
func (e HstoreJSON) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "HstoreJSON " + e.Namespace + "." + e.Name, Inputs: e.Children(), Form: expr.FormCall, Name: e.Name, Namespace: e.Namespace, Quote: expr.QuoteIdentifier, Args: []expr.RenderArg{{Expr: e.Arg, Transport: expr.TransportCast, TransportType: e.Formal.Usage()}}, Replay: expr.Replay{QuoteNamespace: true}}, nil
}
