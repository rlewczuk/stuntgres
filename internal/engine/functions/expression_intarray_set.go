package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// IntarraySet retains the selected declaration and exact formals, not authority.
type IntarraySet struct {
	expr.Info
	Args            [2]expr.Expr
	Formals         [2]*sqltype.Descriptor
	Operation       sqltype.IntarraySetOperation
	Ref             ids.ObjectRef
	Member          uint8
	Namespace, Name string
}

func (e IntarraySet) Validate() error {
	if e.Args[0] == nil || e.Args[1] == nil || e.Args[0].ResultUsage().Type != e.Formals[0] || e.Args[1].ResultUsage().Type != e.Formals[1] || e.RoutineReference == nil || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.ObjectID == 0 || e.Ref.Address.SubID != 0 || e.Namespace == "" || e.Name == "" || int(e.Member) != 6+int(e.Operation) {
		return fmt.Errorf("stuntgres: invalid installed intarray set call")
	}
	return e.Operation.Validate(e.Formals[0], e.Formals[1], e.Usage.Type)
}
func (e IntarraySet) Children() []expr.Expr { return []expr.Expr{e.Args[0], e.Args[1]} }
func (e IntarraySet) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("IntarraySet", children, 2); err != nil {
		return nil, err
	}
	e.Info, e.Args = info, [2]expr.Expr{children[0], children[1]}
	return e, e.Validate()
}
func (e IntarraySet) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e IntarraySet) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
	for i, old := range e.Formals {
		e.Formals[i] = env.Type(old)
		changed = changed || old != e.Formals[i]
	}
	next, err := e.Rebuild(info, children)
	return next, changed, err
}
func (e IntarraySet) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	left, err := e.Args[0].Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	right, err := e.Args[1].Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	arguments := [2]sqltype.Value{left, right}
	body, err := e.EnterRoutine(c, arguments[:])
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	return e.Operation.Eval(ctx, left, right, e.Formals[0], e.Formals[1], e.Usage.Type)
}
func (e IntarraySet) ValidateStoredProvider() error        { return e.Validate() }
func (e IntarraySet) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e IntarraySet) PlanningFold() expr.Fold              { return expr.FoldConstant }
func (e IntarraySet) StrictNull() expr.Strict              { return expr.StrictAfter }
func (e IntarraySet) PreflightFold() expr.Preflight        { return expr.PreflightNullOrEvaluate }
func (e IntarraySet) LooksLikeFunction() bool              { return true }
func (e IntarraySet) ClosureTransparent()                  {}
func (e IntarraySet) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction && kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: e.Children(), Count: 2}, true, nil
}
func (e IntarraySet) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(IntarraySet)
	return ok && e.Formals == o.Formals && e.Operation == o.Operation && e.Ref == o.Ref && e.Member == o.Member && e.RoutineReference == o.RoutineReference && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e IntarraySet) Render() (expr.Rendering, error) {
	if err := e.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	return expr.Rendering{Tree: "IntarraySet " + e.Namespace + "." + e.Name, Inputs: e.Children(), Form: expr.FormCall, Name: e.Name, Namespace: e.Namespace, Quote: expr.QuoteIdentifier, Args: []expr.RenderArg{{Expr: e.Args[0], Transport: expr.TransportCast, TransportType: e.Formals[0].Usage()}, {Expr: e.Args[1], Transport: expr.TransportCast, TransportType: e.Formals[1].Usage()}}, Replay: expr.Replay{QuoteNamespace: true}}, nil
}

func (e IntarraySet) SourceAdmission() (bool, bool) { return true, false }
func (e IntarraySet) OpenSource(c expr.Context) (expr.SourceCursor, error) {
	value, err := e.Eval(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
