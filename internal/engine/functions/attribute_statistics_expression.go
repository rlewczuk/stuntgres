package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/sqltype"
)

// AttributeStatistics owns only immutable operands and written/formal mapping.
// Every invocation resolves targets and current authority through its live host.
type AttributeStatistics struct {
 expr.Info
 Args []expr.Expr
 Clear, Variadic bool
 Slots [4]uint8
 Labels [4]string
}
type AttributeStatisticsRuntime interface {
 AttributeStatistics(clear,variadic bool,values []sqltype.Value)(bool,error)
}
func(n AttributeStatistics) name()string{if n.Clear{return "pg_clear_attribute_stats"};return "pg_restore_attribute_stats"}
func(n AttributeStatistics) Eval(c expr.Context)(sqltype.Value,error){
 if _,err:=evaluationContext(c);err!=nil{return sqltype.Value{},err}
 var scratch [100]sqltype.Value
 for i,arg:=range n.Args {v,err:=arg.Eval(c);if err!=nil{return sqltype.Value{},err};slot:=i;if n.Clear{slot=int(n.Slots[i])};scratch[slot]=v}
 runtime,ok:=c.(AttributeStatisticsRuntime);if !ok{return sqltype.Value{},fmt.Errorf("stuntgres: missing attribute statistics runtime")}
 restored,err:=runtime.AttributeStatistics(n.Clear,n.Variadic,scratch[:len(n.Args)]);if err!=nil{return sqltype.Value{},err}
 if n.Clear{return sqltype.VoidValue(n.Usage.Type),nil}
 return sqltype.Boolean(n.Usage.Type,restored),nil
}
func(n AttributeStatistics) Validate()error{
 if len(n.Args)==0||len(n.Args)>100||n.Usage.Type==nil{return fmt.Errorf("stuntgres: invalid attribute statistics recipe")}
 if n.Clear {
  if len(n.Args)!=4||n.Variadic||n.Usage.Type.ID()!=sqltype.Void{return fmt.Errorf("stuntgres: invalid clear statistics signature")}
  var seen [4]bool
  for i,a:=range n.Args{slot:=n.Slots[i];if slot>=4||seen[slot]||a.ResultUsage().Type.ID()!=clearStatisticsTypes[slot]{return fmt.Errorf("stuntgres: invalid clear statistics operand")};seen[slot]=true}
 } else {
  if n.Usage.Type.ID()!=sqltype.Bool||n.Variadic&&(len(n.Args)!=1||!n.Args[0].ResultUsage().Type.BaseType().IsArray()){return fmt.Errorf("stuntgres: invalid restore statistics signature")}
  for i,label:=range n.Labels{if label!=""&&(!n.Variadic||i!=0||label!="kwargs"){return fmt.Errorf("stuntgres: invalid restore statistics label")}}
 }
 return nil
}
func(n AttributeStatistics) Children()[]expr.Expr{return n.Args}
func(n AttributeStatistics) Rebuild(info expr.Info,children []expr.Expr)(expr.Expr,error){if err:=checkChildren("AttributeStatistics",children,len(n.Args));err!=nil{return nil,err};n.Info,n.Args=info,append([]expr.Expr(nil),children...);return n,nil}
func(n AttributeStatistics) Substitute(info expr.Info,children []expr.Expr)(expr.Expr,error){if err:=checkChildren("AttributeStatistics",children,len(n.Args));err!=nil{return nil,err};n.Info,n.Args=info,children;return n,nil}
func(n AttributeStatistics) IntrinsicVolatility()expr.Volatility{return expr.Volatile}
func(n AttributeStatistics) PlanningFold()expr.Fold{return expr.FoldNever}
func(n AttributeStatistics) PreflightFold()expr.Preflight{return expr.PreflightChildren}
func(n AttributeStatistics) ValidateStoredProvider()error{return n.Validate()}
func(n AttributeStatistics) ClosureTransparent(){}
func(n AttributeStatistics) Retag(info expr.Info,children []expr.Expr,changed bool,_ expr.Retag)(expr.Expr,bool,error){n.Info,n.Args=info,children;return n,changed,nil}
func(n AttributeStatistics) SameGroup(other expr.Expr,same func(expr.Expr,expr.Expr)bool,_ bool)bool{o,ok:=other.(AttributeStatistics);if !ok||n.Clear!=o.Clear||n.Variadic!=o.Variadic||n.Slots!=o.Slots||len(n.Args)!=len(o.Args){return false};for i,a:=range n.Args{if !same(a,o.Args[i]){return false}};return true}
func(n AttributeStatistics) Render()(expr.Rendering,error){
 spec:=variadicRendering("AttributeStatistics",n.name(),n.Args,n.Variadic)
 for i:=range spec.Args{spec.Args[i].Transport=expr.TransportAmbiguousText;if n.Clear||n.Variadic{spec.Args[i].Label=n.Labels[i]}}
 spec.Replay.Validate=n.Validate;spec.Replay.CatalogSnapshot=catalogArguments(n.Args)
 return spec,nil
}

func(n AttributeStatistics) StrictNull()expr.Strict{return expr.StrictNone}
func(n AttributeStatistics) LooksLikeFunction()bool{return true}
func(n AttributeStatistics) CallCost()int{return 1}
func(n AttributeStatistics) ClosedDefault(func(expr.Expr)bool)bool{return false}
func(n AttributeStatistics) SourceAdmission()(bool,bool){return true,false}
func(n AttributeStatistics) OpenSource(ctx expr.Context)(expr.SourceCursor,error){v,err:=n.Eval(ctx);if err!=nil{return expr.SourceCursor{},err};return expr.ScalarSource(v),nil}
func(n AttributeStatistics) SameSourceOccurrence(other expr.Expr)bool{o,ok:=other.(AttributeStatistics);return ok&&n.Clear==o.Clear&&n.Variadic==o.Variadic&&n.Slots==o.Slots&&n.Labels==o.Labels&&n.ResultUsage()==o.ResultUsage()}
func(n AttributeStatistics) Capture(kind expr.CaptureKind)(expr.CaptureOperands,bool,error){if kind!=expr.CaptureFunction{return expr.CaptureOperands{},false,nil};return expr.CaptureOperands{Count:len(n.Args),Indexed:true},true,nil}
func(n AttributeStatistics) CaptureOperand(i int)(expr.Expr,error){if i<0||i>=len(n.Args){return nil,fmt.Errorf("stuntgres: invalid statistics capture")};return n.Args[i],nil}
