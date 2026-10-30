package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
)
func (ReplicationSlot) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (ReplicationSlot) PlanningFold() expr.Fold { return expr.FoldNever }
func (ReplicationSlot) StrictNull() expr.Strict { return expr.StrictNone }
func (ReplicationSlot) PreflightFold() expr.Preflight { return expr.PreflightChildren }
func (ReplicationSlot) LooksLikeFunction() bool { return true }
func (ReplicationSlot) CallCost() int { return 1 }
func (ReplicationSlot) ClosedDefault(func(expr.Expr) bool) bool { return false }
func (ReplicationSlot) SourceAdmission() (bool,bool) { return true,false }
func (n ReplicationSlot) Children() []expr.Expr { return n.Args[:n.Arity] }
func (n ReplicationSlot) OpenSource(ctx expr.Context) (expr.SourceCursor,error) { v,err:=n.Eval(ctx); if err!=nil { return expr.SourceCursor{},err }; return expr.ScalarSource(v),nil }
func (n ReplicationSlot) SameSourceOccurrence(other expr.Expr) bool { o,ok:=other.(ReplicationSlot); return ok && n.Kind==o.Kind && n.Arity==o.Arity && n.Slots==o.Slots && n.Labels==o.Labels && n.ResultUsage()==o.ResultUsage() }
func (n ReplicationSlot) SameGroup(other expr.Expr,same func(expr.Expr,expr.Expr)bool,_ bool) bool { o,ok:=other.(ReplicationSlot); if !ok || n.Kind!=o.Kind || n.Arity!=o.Arity || n.Slots!=o.Slots || n.Labels!=o.Labels { return false }; for i:=range int(n.Arity) { if !same(n.Args[i],o.Args[i]) { return false } }; return true }
func (n ReplicationSlot) Substitute(info expr.Info,children []expr.Expr) (expr.Expr,error) { if len(children)!=int(n.Arity) { return nil,fmt.Errorf("stuntgres: invalid slot reconstruction") }; n.Info=info; for i,c:=range children { if c==nil { return nil,fmt.Errorf("stuntgres: nil slot operand") }; n.Args[i]=c }; return n,nil }
func (n ReplicationSlot) Rebuild(info expr.Info,children []expr.Expr) (expr.Expr,error) { next,err:=n.Substitute(info,children); if err!=nil { return nil,err }; rebuilt:=next.(ReplicationSlot); if err:=rebuilt.Validate(); err!=nil { return nil,err }; return rebuilt,nil }
func (n ReplicationSlot) Render() (expr.Rendering,error) { if err:=n.Validate(); err!=nil { return expr.Rendering{},err }; args:=make([]expr.RenderArg,n.Arity); for i:=range args { args[i]=expr.RenderArg{Expr:n.Args[i],Label:n.Labels[i]} }; return expr.Rendering{Tree:"ReplicationSlot",Inputs:n.Children(),Form:expr.FormCall,Name:n.Kind.Name(),Args:args},nil }
func (n ReplicationSlot) Capture(kind expr.CaptureKind) (expr.CaptureOperands,bool,error) { if kind!=expr.CaptureFunction { return expr.CaptureOperands{},false,nil }; return expr.CaptureOperands{Count:int(n.Arity),Indexed:true},true,nil }
func (n ReplicationSlot) CaptureOperand(i int) (expr.Expr,error) { if i<0 || i>=int(n.Arity) { return nil,fmt.Errorf("stuntgres: invalid slot capture") }; return n.Args[i],nil }
func (n ReplicationSlot) ValidateStoredProvider() error { return n.Validate() }
