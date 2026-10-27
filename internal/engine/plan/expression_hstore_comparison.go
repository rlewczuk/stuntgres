package plan

import (
 "fmt"
 "strconv"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

// HstoreComparisonExpr owns its child slice and retains the selected immutable
// installed declaration. Kind denotes six predicates, cmp, hash or seeded hash.
type HstoreComparisonExpr struct {
 expr.Info
 Ref ids.ObjectRef
 Args []expr.Expr
 Comparison sqltype.Comparison
 Kind uint8
 Namespace string
}
func (e HstoreComparisonExpr) Eval(ctx expr.Context) (sqltype.Value,error) {
 var values [2]sqltype.Value
 null:=false
 for i,a:=range e.Args {v,err:=a.Eval(ctx);if err!=nil{return sqltype.Value{},err};values[i]=v;null=null||v.IsNull()}
 if err:=ctx.FunctionContext().Err();err!=nil{return sqltype.Value{},err}
 if null{return sqltype.Null(e.Usage.Type),nil}
 if e.Kind>=7 {
  seed:=uint64(0);if e.Kind==8 {n,err:=values[1].Integer();if err!=nil{return sqltype.Value{},err};seed=uint64(n)}
  hash,err:=sqltype.HstoreHash(ctx.FunctionContext(),values[0],seed);if err!=nil{return sqltype.Value{},err}
  if e.Kind==7{return sqltype.Integer(e.Usage.Type,int64(int32(uint32(hash))))}
  return sqltype.Integer(e.Usage.Type,int64(hash))
 }
 n,err:=e.Comparison.Compare(ctx.FunctionContext(),values[0],values[1],ctx.FunctionInput());if err!=nil{return sqltype.Value{},err}
 if e.Kind==6{return sqltype.Integer(e.Usage.Type,int64(n))}
 truth:=false
 switch e.Kind {case 0:truth=n==0;case 1:truth=n!=0;case 2:truth=n<0;case 3:truth=n<=0;case 4:truth=n>0;case 5:truth=n>=0;default:return sqltype.Value{},fmt.Errorf("stuntgres: invalid hstore routine kind")}
 return sqltype.Boolean(e.Usage.Type,truth),nil
}
func (e HstoreComparisonExpr) Children() []expr.Expr {return e.Args}
func (e HstoreComparisonExpr) Rebuild(info expr.Info,children []expr.Expr)(expr.Expr,error){
 e.Info=info;e.Args=append([]expr.Expr(nil),children...)
 if err:=e.ValidateStoredProvider();err!=nil{return nil,err}
 if e.Kind<7 {c,err:=sqltype.BindComparison(children[0].ResultUsage().Type,children[1].ResultUsage().Type,sqltype.CollationRef{},false);if err!=nil{return nil,err};e.Comparison=c}
 return e,nil
}
func (e HstoreComparisonExpr) ValidateStoredProvider() error {
 invalid:=func()error{return fmt.Errorf("stuntgres: invalid captured hstore routine")}
 p,ok:=e.RoutineReference.(*catalog.Procedure)
 count:=2;if e.Kind==7{count=1}
 if !ok||p==nil||e.Kind>=9||len(e.Args)!=count||!p.Strict||p.RoutineKind!=catalog.FunctionRoutine||p.Volatility!=expr.Immutable||p.Implementation==nil||p.Builtin!=nil||p.SQL!=nil||p.Foreign!=nil||e.Ref.Address!=p.Address()||p.Revision()==0{return invalid()}
 for _,a:=range e.Args{if a==nil{return invalid()}}
 typ:=e.Args[0].ResultUsage().Type;if typ==nil||!typ.IsHstore(){return invalid()}
 signature:=strconv.FormatUint(uint64(typ.ID()),10)
 result:=sqltype.Bool
 if e.Kind<7 {if e.Args[1].ResultUsage().Type!=typ{return invalid()};signature+=","+signature;if e.Kind==6{result=sqltype.Int4}}else if e.Kind==7{result=sqltype.Int4}else{signature+=",20";result=sqltype.Int8;if e.Args[1].ResultUsage().Type.ID()!=sqltype.Int8{return invalid()}}
 if string(p.Signature)!=signature||sqltype.ID(p.Result)!=result||e.Usage.Type==nil||e.Usage.Type.ID()!=result{return invalid()}
 return nil
}
func (e HstoreComparisonExpr) IntrinsicVolatility()expr.Volatility{return expr.Immutable}
func (e HstoreComparisonExpr) StrictNull()expr.Strict{return expr.StrictAfter}
func (e HstoreComparisonExpr) SameGroup(other expr.Expr,same func(expr.Expr,expr.Expr)bool,_ bool)bool{
 o,ok:=other.(HstoreComparisonExpr);if !ok||e.RoutineReference!=o.RoutineReference||e.Kind!=o.Kind||len(e.Args)!=len(o.Args){return false};for i:=range e.Args{if !same(e.Args[i],o.Args[i]){return false}};return true
}
func (e HstoreComparisonExpr) Render()(expr.Rendering,error){
 if err:=e.ValidateStoredProvider();err!=nil{return expr.Rendering{},err}
 p:=e.RoutineReference.(*catalog.Procedure);name:=catalog.DependencyIdentifier(e.Namespace)+"."+catalog.DependencyIdentifier(p.Name())
 args:=make([]expr.RenderArg,len(e.Args));pieces:=[]expr.Piece{expr.TextPiece(name+"(")}
 for i,a:=range e.Args{args[i]=expr.RenderArg{Expr:a,Transport:expr.TransportCast,TransportType:a.ResultUsage()};if i>0{pieces=append(pieces,expr.TextPiece(", "))};pieces=append(pieces,expr.ArgPiece(i))};pieces=append(pieces,expr.TextPiece(")"))
 return expr.Rendering{Tree:"HstoreComparison "+name,Inputs:e.Args,Form:expr.FormCall,Name:name,Quote:expr.QuoteVerbatim,Args:args,Replay:expr.Replay{Pieces:pieces}},nil
}
func (e HstoreComparisonExpr) Capture(kind expr.CaptureKind)(expr.CaptureOperands,bool,error){
 if kind!=expr.CaptureFunction{return expr.CaptureOperands{},false,nil};if err:=e.ValidateStoredProvider();err!=nil{return expr.CaptureOperands{},false,err}
 var args [2]expr.Expr;copy(args[:],e.Args);return expr.CaptureOperands{Inline:args,Count:len(e.Args),SelectedImplicit:true},true,nil
}
