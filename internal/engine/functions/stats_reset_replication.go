package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

// StatsResetReplicationKind retains only the released operation identity.
type StatsResetReplicationKind uint8
const (
 ResetReplicationSlot StatsResetReplicationKind = iota
 ResetSubscriptionStats
)
var statsResetReplicationNames = [...]string{"pg_stat_reset_replication_slot", "pg_stat_reset_subscription_stats"}
func (k StatsResetReplicationKind) Name() string { if int(k)>=len(statsResetReplicationNames) { return "" }; return statsResetReplicationNames[k] }
func (k StatsResetReplicationKind) ArgumentType() sqltype.ID { if k==ResetReplicationSlot { return sqltype.Text }; if k==ResetSubscriptionStats { return sqltype.Oid }; return 0 }

type StatsResetReplication struct {
 expr.Info
 Kind StatsResetReplicationKind
 Argument expr.Expr
}
type StatsResetReplicationRuntime interface {
 StatsResetReplication(StatsResetReplicationKind, sqltype.Value, *sqltype.Descriptor) (sqltype.Value,error)
}
func bindStatsResetReplication(b Binder, raw parser.FunctionExpr) (expr.Expr,error) {
 var operand Argument
 var inline [1]string
 names:=inline[:min(len(raw.Args),1)]
 if len(raw.Args)>1 && len(raw.Args)<=100 { names=make([]string,len(raw.Args)) }
 for i,child:=range raw.Args {
  arg,err:=b.AnalyzeArgument(child,AnalysisRequest{Intent:ComparisonArgument}); if err!=nil { return nil,err }
  if i==0 { operand=arg }
  if i<len(names) { name:="unknown"; if arg.Usage.Type!=nil { name=b.DiagnosticTypeName(arg.Usage.Type) }; names[i]=NamedArgumentType(raw,i,name) }
 }
 if len(raw.Args)>100 { return nil,expr.ErrorAt("54023","cannot pass more than 100 arguments to a function",raw.Pos) }
 if err:=ValidateCallSyntax(b,raw); err!=nil { return nil,err }
 visible,err:=b.BuiltinVisible(raw.Name); if err!=nil { return nil,err }
 name:=raw.Name.Parts[len(raw.Name.Parts)-1].Text
 kind:=StatsResetReplicationKind(len(statsResetReplicationNames))
 for i,n:=range statsResetReplicationNames { if name==n { kind=StatsResetReplicationKind(i); break } }
 named:=false; for _,label:=range raw.ArgNames { named=named||label.Text!="" }
 if !visible || kind.Name()=="" || len(raw.Args)!=1 || named || raw.Variadic || raw.ResolutionOnly { return nil,b.SignatureError(raw.Name,raw.Pos,names) }
 target:=b.Type(kind.ArgumentType()).Usage()
 if operand.Usage.Type!=nil { if _,failure:=sqltype.PlanCoercion(operand.Usage,target,sqltype.Implicit); failure!=nil { return nil,b.SignatureError(raw.Name,raw.Pos,names) } }
 argument,err:=b.CoerceArgument(operand,CoercionRequest{Intent:ExactFormalArgument,Target:target}); if err!=nil { return nil,err }
 if raw.Star { return nil,expr.ErrorAt("42809",name+"(*) specified, but "+name+" is not an aggregate function",raw.Pos) }
 if err:=CallDecorationError(raw); err!=nil { return nil,err }
 info,err:=b.BuiltinRoutineInfo(b.Info(b.Type(sqltype.Void).Usage(),raw.Pos,expr.MergeProperties(expr.Properties{Volatility:expr.Volatile},argument.Properties())),CatalogRoutineProvider,name,kind.ArgumentType())
 if err!=nil { return nil,err }
 return StatsResetReplication{Info:info,Kind:kind,Argument:argument},nil
}
func (n StatsResetReplication) Validate() error {
 if n.Kind.Name()=="" || n.Usage.Type==nil || n.Usage.Type.ID()!=sqltype.Void || n.Argument==nil || n.Argument.ResultUsage().Type==nil || n.Argument.ResultUsage().Type.ID()!=n.Kind.ArgumentType() { return fmt.Errorf("stuntgres: invalid replication statistics reset signature") }; return nil
}
func (n StatsResetReplication) Eval(ctx expr.Context) (sqltype.Value,error) {
 if _,err:=evaluationContext(ctx); err!=nil { return sqltype.Value{},err }
 if err:=n.Validate(); err!=nil { return sqltype.Value{},err }
 value,err:=n.Argument.Eval(ctx); if err!=nil { return sqltype.Value{},err }
 runtime,ok:=ctx.(StatsResetReplicationRuntime); if !ok { return sqltype.Value{},fmt.Errorf("stuntgres: missing replication statistics reset runtime") }
 return runtime.StatsResetReplication(n.Kind,value,n.Usage.Type)
}
func (StatsResetReplication) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (StatsResetReplication) PlanningFold() expr.Fold { return expr.FoldNever }
func (StatsResetReplication) StrictNull() expr.Strict { return expr.StrictNone }
func (StatsResetReplication) PreflightFold() expr.Preflight { return expr.PreflightKeep }
func (StatsResetReplication) LooksLikeFunction() bool { return true }
func (StatsResetReplication) ClosedDefault(func(expr.Expr)bool) bool { return false }
func (StatsResetReplication) SourceAdmission() (bool,bool) { return true,false }
func (n StatsResetReplication) Children() []expr.Expr { return []expr.Expr{n.Argument} }
func (n StatsResetReplication) OpenSource(ctx expr.Context) (expr.SourceCursor,error) { v,err:=n.Eval(ctx); if err!=nil { return expr.SourceCursor{},err }; return expr.ScalarSource(v),nil }
func (n StatsResetReplication) SameSourceOccurrence(other expr.Expr) bool { o,ok:=other.(StatsResetReplication); return ok && n.Kind==o.Kind && n.ResultUsage()==o.ResultUsage() }
func (n StatsResetReplication) SameGroup(other expr.Expr,same func(expr.Expr,expr.Expr)bool,_ bool) bool { o,ok:=other.(StatsResetReplication); return ok && n.Kind==o.Kind && same(n.Argument,o.Argument) }
func (n StatsResetReplication) Substitute(info expr.Info,children []expr.Expr) (expr.Expr,error) { if len(children)!=1 || children[0]==nil { return nil,fmt.Errorf("stuntgres: invalid replication statistics reset reconstruction") }; n.Info,n.Argument=info,children[0]; return n,nil }
func (n StatsResetReplication) Rebuild(info expr.Info,children []expr.Expr) (expr.Expr,error) { v,err:=n.Substitute(info,children); if err!=nil { return nil,err }; rebuilt:=v.(StatsResetReplication); return rebuilt,rebuilt.Validate() }
func (n StatsResetReplication) Render() (expr.Rendering,error) { if err:=n.Validate(); err!=nil { return expr.Rendering{},err }; return expr.Rendering{Tree:"StatsResetReplication",Inputs:n.Children(),Form:expr.FormCall,Name:n.Kind.Name(),Args:[]expr.RenderArg{{Expr:n.Argument}}},nil }
func (n StatsResetReplication) Capture(kind expr.CaptureKind) (expr.CaptureOperands,bool,error) { return expr.CaptureOperands{Count:1,Indexed:true},kind==expr.CaptureFunction,nil }
func (n StatsResetReplication) CaptureOperand(i int) (expr.Expr,error) { if i!=0 { return nil,fmt.Errorf("stuntgres: invalid replication statistics reset capture") }; return n.Argument,nil }
func (n StatsResetReplication) ValidateStoredProvider() error { return n.Validate() }
func (StatsResetReplication) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.StatsResetReplication") }
