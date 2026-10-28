package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

// LtreePath owns bounded immutable operands and formals, never invocation authority.
type LtreePath struct {
 expr.Info
 Args [8]expr.Expr
 Formals [8]*sqltype.Descriptor
 Count int
 Operation sqltype.LtreePathOperation
 Ref ids.ObjectRef
 Member uint8
 Namespace, Name string
}

func (e LtreePath) Validate() error {
 if e.Count == 0 || e.Count != e.Operation.Arity() || e.RoutineReference == nil || e.Ref.Address.ClassID != ids.ProcedureClassID || e.Ref.Address.ObjectID == 0 || e.Ref.Address.SubID != 0 || e.Namespace == "" || e.Name == "" || int(e.Member) != ltreeHierarchyMemberStart+2*int(sqltype.LtreeHierarchyOperationCount)+int(e.Operation) { return fmt.Errorf("stuntgres: invalid installed ltree path call") }
 for i, arg := range e.Args { if i < e.Count { if arg == nil || arg.ResultUsage().Type != e.Formals[i] { return fmt.Errorf("stuntgres: invalid ltree path operand") } } else if arg != nil { return fmt.Errorf("stuntgres: inactive ltree path operand") } }
 return e.Operation.Validate(e.Formals,e.Count,e.Usage.Type)
}
func (e LtreePath) Children() []expr.Expr { return append([]expr.Expr(nil),e.Args[:e.Count]...) }
func (e LtreePath) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr,error) {
 if err := checkChildren("LtreePath",children,e.Count); err != nil { return nil,err }
 e.Info = info; copy(e.Args[:e.Count],children)
 return e,e.Validate()
}
func (e LtreePath) Substitute(info expr.Info, children []expr.Expr) (expr.Expr,error) { return e.Rebuild(info,children) }
func (e LtreePath) Retag(info expr.Info, children []expr.Expr, changed bool, env expr.Retag) (expr.Expr,bool,error) {
 for i, old := range e.Formals[:e.Count] { e.Formals[i] = env.Type(old); changed = changed || old != e.Formals[i] }
 next,err := e.Rebuild(info,children); return next,changed,err
}
func (e LtreePath) Eval(c expr.Context) (sqltype.Value,error) {
 ctx,err := evaluationContext(c); if err != nil { return sqltype.Value{},err }
 for _, arg := range e.Args[:e.Count] { if c.FunctionLiteralNull(arg) { return sqltype.Null(e.Usage.Type),nil } }
 var values [8]sqltype.Value
 for i, arg := range e.Args[:e.Count] { value,err := arg.Eval(c); if err != nil { return sqltype.Value{},err }; values[i] = value }
 return e.Operation.Eval(ctx,values,e.Formals,e.Count,e.Usage.Type)
}

// Selected path calls produce exactly one scalar row, including strict NULL.
// The shared source cursor owns only the evaluated value, never this call/frame.
func (e LtreePath) SourceAdmission() (bool,bool) { return true,false }
func (e LtreePath) OpenSource(c expr.Context) (expr.SourceCursor,error) {
 value,err:=e.Eval(c)
 if err!=nil { return expr.SourceCursor{},err }
 return expr.ScalarSource(value),nil
}
func (e LtreePath) ValidateStoredProvider() error { return e.Validate() }
func (e LtreePath) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e LtreePath) PlanningFold() expr.Fold { return expr.FoldConstant }
func (e LtreePath) StrictNull() expr.Strict { return expr.StrictAfter }
func (e LtreePath) PreflightFold() expr.Preflight { return expr.PreflightRegexNullOrEvaluate }
func (e LtreePath) LooksLikeFunction() bool { return true }
func (e LtreePath) ClosureTransparent() {}
func (e LtreePath) Capture(kind expr.CaptureKind) (expr.CaptureOperands,bool,error) {
 if kind != expr.CaptureFunction && kind != expr.CaptureOperator { return expr.CaptureOperands{},false,nil }
 return expr.CaptureOperands{Args:e.Children(),Count:e.Count},true,nil
}
func (e LtreePath) SameGroup(other expr.Expr, same func(expr.Expr,expr.Expr)bool, definition bool) bool {
 o,ok := other.(LtreePath)
 if !ok || e.Formals != o.Formals || e.Count != o.Count || e.Operation != o.Operation || e.Ref != o.Ref || e.Member != o.Member || e.RoutineReference != o.RoutineReference { return false }
 for i, arg := range e.Args[:e.Count] { if !same(arg,o.Args[i]) { return false } }; return true
}
func (e LtreePath) Render() (expr.Rendering,error) {
 if err := e.Validate(); err != nil { return expr.Rendering{},err }
 args := make([]expr.RenderArg,e.Count)
 for i, arg := range e.Args[:e.Count] { args[i] = expr.RenderArg{Expr:arg,Transport:expr.TransportCast,TransportType:e.Formals[i].Usage()} }
 return expr.Rendering{Tree:"LtreePath "+e.Namespace+"."+e.Name,Inputs:e.Children(),Form:expr.FormCall,Name:e.Name,Namespace:e.Namespace,Quote:expr.QuoteIdentifier,Args:args,Replay:expr.Replay{QuoteNamespace:true}},nil
}
