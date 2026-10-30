package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/sqltype"
)

// RelationStatistics owns only immutable operands and written/formal mapping.
// Every invocation resolves targets and current authority through its live host.
type RelationStatistics struct {
 expr.Info
 Args []expr.Expr
 Clear, Variadic bool
 Slots [2]uint8
 Labels [2]string
}
type RelationStatisticsRuntime interface {
 RelationStatistics(clear,variadic bool,values []sqltype.Value)(bool,error)
}
func(n RelationStatistics) name()string{if n.Clear{return "pg_clear_relation_stats"};return "pg_restore_relation_stats"}
func(n RelationStatistics) Eval(c expr.Context)(sqltype.Value,error){
 if _,err:=evaluationContext(c);err!=nil{return sqltype.Value{},err}
 var scratch [100]sqltype.Value
 // PostgreSQL lowers named fixed arguments to formal order before execution.
 // Keep Args/Labels in written order for rendering and capture.
 for slot:=range n.Args {
  written:=slot
  if n.Clear && n.Slots[0]==1 {written=1-slot}
  v,err:=n.Args[written].Eval(c);if err!=nil{return sqltype.Value{},err}
  scratch[slot]=v
 }
 runtime,ok:=c.(RelationStatisticsRuntime);if !ok{return sqltype.Value{},fmt.Errorf("stuntgres: missing relation statistics runtime")}
 restored,err:=runtime.RelationStatistics(n.Clear,n.Variadic,scratch[:len(n.Args)]);if err!=nil{return sqltype.Value{},err}
 if n.Clear{return sqltype.VoidValue(n.Usage.Type),nil}
 return sqltype.Boolean(n.Usage.Type,restored),nil
}
func(n RelationStatistics) Validate()error{
 if len(n.Args)==0||len(n.Args)>100||n.Usage.Type==nil{return fmt.Errorf("stuntgres: invalid relation statistics recipe")}
 if n.Clear {
  if len(n.Args)!=2||n.Variadic||n.Usage.Type.ID()!=sqltype.Void{return fmt.Errorf("stuntgres: invalid clear statistics signature")}
  var seen [2]bool
  for i,a:=range n.Args{slot:=n.Slots[i];if slot>=2||seen[slot]||a.ResultUsage().Type.ID()!=clearRelationStatisticsTypes[slot]{return fmt.Errorf("stuntgres: invalid clear statistics operand")};seen[slot]=true}
 } else {
  if n.Usage.Type.ID()!=sqltype.Bool||n.Variadic&&(len(n.Args)!=1||!n.Args[0].ResultUsage().Type.BaseType().IsArray()){return fmt.Errorf("stuntgres: invalid restore statistics signature")}
  for i,label:=range n.Labels{if label!=""&&(!n.Variadic||i!=0||label!="kwargs"){return fmt.Errorf("stuntgres: invalid restore statistics label")}}
 }
 return nil
}
func(n RelationStatistics) Children()[]expr.Expr{return n.Args}
func(n RelationStatistics) Rebuild(info expr.Info,children []expr.Expr)(expr.Expr,error){if err:=checkChildren("RelationStatistics",children,len(n.Args));err!=nil{return nil,err};n.Info,n.Args=info,append([]expr.Expr(nil),children...);return n,nil}
func(n RelationStatistics) Substitute(info expr.Info,children []expr.Expr)(expr.Expr,error){if err:=checkChildren("RelationStatistics",children,len(n.Args));err!=nil{return nil,err};n.Info,n.Args=info,children;return n,nil}
func(n RelationStatistics) IntrinsicVolatility()expr.Volatility{return expr.Volatile}
func(n RelationStatistics) PlanningFold()expr.Fold{return expr.FoldNever}
func(n RelationStatistics) PreflightFold()expr.Preflight{return expr.PreflightChildren}
func(n RelationStatistics) ValidateStoredProvider()error{return n.Validate()}
func(n RelationStatistics) ClosureTransparent(){}
func(n RelationStatistics) Retag(info expr.Info,children []expr.Expr,changed bool,_ expr.Retag)(expr.Expr,bool,error){n.Info,n.Args=info,children;return n,changed,nil}
func(n RelationStatistics) SameGroup(other expr.Expr,same func(expr.Expr,expr.Expr)bool,_ bool)bool{o,ok:=other.(RelationStatistics);if !ok||n.Clear!=o.Clear||n.Variadic!=o.Variadic||n.Slots!=o.Slots||len(n.Args)!=len(o.Args){return false};for i,a:=range n.Args{if !same(a,o.Args[i]){return false}};return true}
func(n RelationStatistics) Render()(expr.Rendering,error){
 spec:=variadicRendering("RelationStatistics",n.name(),n.Args,n.Variadic)
 for i:=range spec.Args{spec.Args[i].Transport=expr.TransportAmbiguousText;if n.Clear||n.Variadic{spec.Args[i].Label=n.Labels[i]}}
 spec.Replay.Validate=n.Validate;spec.Replay.CatalogSnapshot=catalogArguments(n.Args)
 return spec,nil
}

func(n RelationStatistics) StrictNull()expr.Strict{return expr.StrictNone}
func(n RelationStatistics) LooksLikeFunction()bool{return true}
func(n RelationStatistics) CallCost()int{return 1}
func(n RelationStatistics) ClosedDefault(func(expr.Expr)bool)bool{return false}
func(n RelationStatistics) SourceAdmission()(bool,bool){return true,false}
func(n RelationStatistics) OpenSource(ctx expr.Context)(expr.SourceCursor,error){v,err:=n.Eval(ctx);if err!=nil{return expr.SourceCursor{},err};return expr.ScalarSource(v),nil}
func(n RelationStatistics) SameSourceOccurrence(other expr.Expr)bool{o,ok:=other.(RelationStatistics);return ok&&n.Clear==o.Clear&&n.Variadic==o.Variadic&&n.Slots==o.Slots&&n.Labels==o.Labels&&n.ResultUsage()==o.ResultUsage()}
func(n RelationStatistics) Capture(kind expr.CaptureKind)(expr.CaptureOperands,bool,error){if kind!=expr.CaptureFunction{return expr.CaptureOperands{},false,nil};return expr.CaptureOperands{Count:len(n.Args),Indexed:true},true,nil}
func(n RelationStatistics) CaptureOperand(i int)(expr.Expr,error){if i<0||i>=len(n.Args){return nil,fmt.Errorf("stuntgres: invalid statistics capture")};return n.Args[i],nil}
