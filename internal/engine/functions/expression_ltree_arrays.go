package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

// LtreeArray retains the selected declaration and exact formals, not authority.
type LtreeArray struct {
 expr.Info
 Args [2]expr.Expr
 Formals [2]*sqltype.Descriptor
 Operation sqltype.LtreeArrayOperation
 Ref ids.ObjectRef
 Member uint8
 Namespace, Name string
}

func (e LtreeArray) Validate() error {
 if e.Args[0] == nil || e.Args[1] == nil || e.Args[0].ResultUsage().Type != e.Formals[0] || e.Args[1].ResultUsage().Type != e.Formals[1] || e.RoutineReference == nil || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.ObjectID == 0 || e.Ref.Address.SubID != 0 || e.Namespace == "" || e.Name == "" || int(e.Member) != 9+int(e.Operation) { return fmt.Errorf("stuntgres: invalid installed ltree array call") }
 return e.Operation.Validate(e.Formals[0], e.Formals[1], e.Usage.Type)
}
func (e LtreeArray) Children() []expr.Expr { return []expr.Expr{e.Args[0], e.Args[1]} }
func (e LtreeArray) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
 if err := checkChildren("LtreeArray", children, 2); err != nil { return nil, err }
 e.Info, e.Args = info, [2]expr.Expr{children[0], children[1]}
 return e, e.Validate()
}
func (e LtreeArray) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) { return e.Rebuild(info, children) }
func (e LtreeArray) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr, bool, error) {
 for i, old := range e.Formals { e.Formals[i] = env.Type(old); changed = changed || old != e.Formals[i] }
 next, err := e.Rebuild(info, children); return next, changed, err
}
func (e LtreeArray) Eval(c expr.Context) (sqltype.Value, error) {
 ctx, err := evaluationContext(c); if err != nil { return sqltype.Value{}, err }
 for _, arg := range e.Args { if c.FunctionLiteralNull(arg) { return sqltype.Null(e.Usage.Type), nil } }
 left, err := e.Args[0].Eval(c); if err != nil { return sqltype.Value{}, err }
 right, err := e.Args[1].Eval(c); if err != nil { return sqltype.Value{}, err }
 return e.Operation.Eval(ctx, left, right, e.Formals[0], e.Formals[1], e.Usage.Type)
}
func (e LtreeArray) ValidateStoredProvider() error { return e.Validate() }
func (e LtreeArray) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e LtreeArray) PlanningFold() expr.Fold { return expr.FoldConstant }
func (e LtreeArray) StrictNull() expr.Strict { return expr.StrictAfter }
func (e LtreeArray) PreflightFold() expr.Preflight { return expr.PreflightRegexNullOrEvaluate }
func (e LtreeArray) LooksLikeFunction() bool { return true }
func (e LtreeArray) ClosureTransparent() {}
func (e LtreeArray) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
 if kind != expr.CaptureFunction && kind != expr.CaptureOperator { return expr.CaptureOperands{}, false, nil }
 return expr.CaptureOperands{Args: e.Children(), Count: 2}, true, nil
}
func (e LtreeArray) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
 o, ok := other.(LtreeArray)
 return ok && e.Formals == o.Formals && e.Operation == o.Operation && e.Ref == o.Ref && e.Member == o.Member && e.RoutineReference == o.RoutineReference && same(e.Args[0], o.Args[0]) && same(e.Args[1], o.Args[1])
}
func (e LtreeArray) Render() (expr.Rendering, error) {
 if err := e.Validate(); err != nil { return expr.Rendering{}, err }
 return expr.Rendering{Tree: "LtreeArray "+e.Namespace+"."+e.Name, Inputs: e.Children(), Form: expr.FormCall, Name: e.Name, Namespace: e.Namespace, Quote: expr.QuoteIdentifier, Args: []expr.RenderArg{{Expr: e.Args[0], Transport: expr.TransportCast, TransportType: e.Formals[0].Usage()}, {Expr: e.Args[1], Transport: expr.TransportCast, TransportType: e.Formals[1].Usage()}}, Replay: expr.Replay{QuoteNamespace: true}}, nil
}
