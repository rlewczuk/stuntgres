package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// HstoreArray retains installed declaration identity, never caller authority.
type HstoreArray struct {
	expr.Info
	Arg             expr.Expr
	Formal          *sqltype.Descriptor
	Matrix          bool
	Ref             ids.ObjectRef
	Member          uint8
	Namespace, Name string
}

func (e HstoreArray) Validate() error {
	if e.Arg == nil || !e.Formal.IsHstore() || e.Arg.ResultUsage().Type != e.Formal || e.Usage.Type == nil || e.Usage.Type.ID() != 1009 || !e.Usage.Type.IsArray() || e.Usage.Type.Element().ID() != sqltype.Text || e.RoutineReference == nil || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.ObjectID == 0 || e.Ref.Address.SubID != 0 || e.Namespace == "" || e.Name == "" || e.Member != 3 && e.Member != 4 || e.Matrix != (e.Member == 4) {
		return fmt.Errorf("stuntgres: invalid installed hstore array call")
	}
	return nil
}
func (e HstoreArray) Children() []expr.Expr { return []expr.Expr{e.Arg} }
func (e HstoreArray) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("HstoreArray", children, 1); err != nil {
		return nil, err
	}
	e.Info, e.Arg = info, children[0]
	return e, e.Validate()
}
func (e HstoreArray) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e HstoreArray) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	formal := env.Type(e.Formal)
	changed = changed || formal != e.Formal
	e.Formal = formal
	next, err := e.Rebuild(info, children)
	return next, changed, err
}
func (e HstoreArray) Eval(c expr.Context) (sqltype.Value, error) {
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
	return value.HstoreArray(ctx, e.Formal, e.Usage.Type, e.Matrix)
}
func (e HstoreArray) ValidateStoredProvider() error        { return e.Validate() }
func (e HstoreArray) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e HstoreArray) PlanningFold() expr.Fold              { return expr.FoldConstant }
func (e HstoreArray) StrictNull() expr.Strict              { return expr.StrictAfter }
func (e HstoreArray) PreflightFold() expr.Preflight        { return expr.PreflightRegexNullOrEvaluate }
func (e HstoreArray) LooksLikeFunction() bool              { return true }
func (e HstoreArray) ClosureTransparent()                  {}
func (e HstoreArray) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction && kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: e.Children(), Count: 1}, true, nil
}
func (e HstoreArray) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(HstoreArray)
	return ok && e.Formal == o.Formal && e.Matrix == o.Matrix && e.Ref == o.Ref && e.Member == o.Member && e.RoutineReference == o.RoutineReference && same(e.Arg, o.Arg)
}
func (e HstoreArray) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "HstoreArray " + e.Namespace + "." + e.Name, Inputs: e.Children(), Form: expr.FormCall, Name: e.Name, Namespace: e.Namespace, Quote: expr.QuoteIdentifier, Args: []expr.RenderArg{{Expr: e.Arg, Transport: expr.TransportCast, TransportType: e.Formal.Usage()}}, Replay: expr.Replay{QuoteNamespace: true}}, nil
}
