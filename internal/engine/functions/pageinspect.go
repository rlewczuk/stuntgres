package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

var pageinspectNames=[...]string{"brin_page_type","brin_metapage_info","brin_revmap_data","brin_page_items","bt_page_items","fsm_page_contents"}
type Pageinspect struct { expr.Info; Args []expr.Expr; Slots [2]uint8; Member string }
func pageinspectFormals(member string)([]string,[]sqltype.ID){
 if member=="brin_page_items" {return []string{"page","index_oid"},[]sqltype.ID{sqltype.Bytea,sqltype.Regclass}}
 return []string{"page"},[]sqltype.ID{sqltype.Bytea}
}
func bindPageinspect(b Binder,raw parser.FunctionExpr)(expr.Expr,error){
 operands:=make([]Argument,len(raw.Args))
 for i,child:=range raw.Args {v,err:=b.AnalyzeArgument(child,AnalysisRequest{Intent:ComparisonArgument});if err!=nil{return nil,err};operands[i]=v}
 if err:=ValidateCallSyntax(b,raw);err!=nil{return nil,err}
 member:=raw.Name.Parts[len(raw.Name.Parts)-1].Text
 labels,types:=pageinspectFormals(member);signature:="17";if len(types)==2{signature="17,2205"}
 routine,err:=b.InstalledRoutine(raw.Name,PageinspectExtension,signature);if err!=nil{return nil,err}
 slots,valid:=namedSlots(raw,labels,len(labels))
 if !valid || len(operands)!=len(types) || routine==nil || raw.ResolutionOnly || raw.Variadic{return nil,b.SignatureError(raw.Name,raw.Pos,installedArgumentNames(b,raw,operands))}
 if err:=routine.Validate();err!=nil{return nil,err}
 if err:=CallDecorationError(raw);err!=nil{return nil,err}
 if raw.Star{return nil,expr.ErrorAt("42809",member+"(*) specified, but "+member+" is not an aggregate function",raw.Pos)}
 call:=Pageinspect{Member:member,Args:make([]expr.Expr,len(operands))};properties:=expr.Properties{Volatility:expr.Volatile,StageDependent:true}
 for i,operand:=range operands {
  formal:=slots[i];target:=b.Type(types[formal]).Usage()
  if operand.Usage.Type!=nil {if _,err:=sqltype.PlanCoercion(operand.Usage,target,sqltype.Implicit);err!=nil{return nil,b.SignatureError(raw.Name,raw.Pos,installedArgumentNames(b,raw,operands))}}
  child,err:=b.CoerceArgument(operand,CoercionRequest{Intent:DirectFormalArgument,Target:target});if err!=nil{return nil,err}
  call.Args[i]=child;call.Slots[i]=uint8(formal);properties=expr.MergeProperties(properties,child.Properties())
 }
 usage:=b.Type(sqltype.Text).Usage()
 names:=[]string(nil);ids:=[]sqltype.ID(nil)
 switch member {
 case "brin_revmap_data":usage=b.Type(sqltype.Tid).Usage()
 case "brin_metapage_info":names=[]string{"magic","version","pagesperrange","lastrevmappage"};ids=[]sqltype.ID{sqltype.Text,sqltype.Int4,sqltype.Int4,sqltype.Int8}
 case "brin_page_items":names=[]string{"itemoffset","blknum","attnum","allnulls","hasnulls","placeholder","empty","value"};ids=[]sqltype.ID{sqltype.Int4,sqltype.Int8,sqltype.Int4,sqltype.Bool,sqltype.Bool,sqltype.Bool,sqltype.Bool,sqltype.Text}
 case "bt_page_items":names=[]string{"itemoffset","ctid","itemlen","nulls","vars","data","dead","htid","tids"};ids=[]sqltype.ID{sqltype.Int2,sqltype.Tid,sqltype.Int2,sqltype.Bool,sqltype.Bool,sqltype.Text,sqltype.Bool,sqltype.Tid,1010}
 }
 if names!=nil {
  members:=make([]sqltype.CompositeMember,len(names));for i,name:=range names{members[i]=sqltype.CompositeMember{Attribute:int32(i+1),Name:name,Usage:b.Type(ids[i]).Usage()}}
  shape,err:=sqltype.RecordShape(members);if err!=nil{return nil,err};usage=shape.Usage()
 }
 call.Info=b.Info(usage,raw.Pos,properties);return routine.Attach(call)
}
func namedPageinspect(b CallTypeContext,raw parser.FunctionExpr,args []Argument,declared []sqltype.Usage)([]sqltype.Usage,[]sqltype.Usage,bool){
 labels,types:=pageinspectFormals(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
 slots,ok:=namedSlots(raw,labels,len(labels));if !ok || raw.Variadic || len(args)!=len(types) || !declaredTypes(declared,types){return nil,nil,false}
 return namedInputs(b,args,declared,slots[:len(args)])
}
func(n Pageinspect)Eval(expr.Context)(sqltype.Value,error){return sqltype.Value{},fmt.Errorf("stuntgres: pageinspect requires installed invocation authority")}
func(n Pageinspect)Children()[]expr.Expr{return n.Args}
// ValidateArguments checks immutable operands without rebuilding or copying them.
func(n Pageinspect)ValidateArguments(children []expr.Expr)error{
 count:=1
 switch n.Member{
 case "brin_page_type","brin_metapage_info","brin_revmap_data","bt_page_items","fsm_page_contents":
 case "brin_page_items":count=2
 default:return fmt.Errorf("stuntgres: invalid pageinspect member")
 }
 if len(children)!=count{return fmt.Errorf("stuntgres: invalid pageinspect arguments")}
 seen:=uint8(0)
 for i,child:=range children{
  slot:=n.Slots[i]
  if int(slot)>=count || seen&(1<<slot)!=0 || child==nil || child.ResultUsage().Type==nil{return fmt.Errorf("stuntgres: invalid pageinspect argument")}
  expected:=sqltype.Bytea
  if slot==1{expected=sqltype.Regclass}
  if child.ResultUsage().Type.BaseType().ID()!=expected{return fmt.Errorf("stuntgres: invalid pageinspect argument")}
  seen|=1<<slot
 }
 return nil
}
func(n Pageinspect)Rebuild(info expr.Info,children []expr.Expr)(expr.Expr,error){
 if err:=n.ValidateArguments(children);err!=nil{return nil,err}
 n.Info=info;n.Args=append([]expr.Expr(nil),children...);return n,nil
}
func(n Pageinspect)Substitute(info expr.Info,children []expr.Expr)(expr.Expr,error){return n.Rebuild(info,children)}
func(Pageinspect)IntrinsicVolatility()expr.Volatility{return expr.Volatile}
func(Pageinspect)PlanningFold()expr.Fold{return expr.FoldNever}
func(Pageinspect)PreflightFold()expr.Preflight{return expr.PreflightChildren}
func(Pageinspect)LooksLikeFunction()bool{return true}
func(n Pageinspect)Capture(kind expr.CaptureKind)(expr.CaptureOperands,bool,error){return expr.CaptureOperands{Args:n.Args,Count:len(n.Args)},kind==expr.CaptureFunction,nil}
func(n Pageinspect)SourceAdmission()(bool,bool){return true,n.Member=="brin_page_items" || n.Member=="brin_revmap_data" || n.Member=="bt_page_items"}
func(n Pageinspect)ScalarSourceOutName()string{if n.Member=="fsm_page_contents"{return ""};if n.Member=="brin_revmap_data"{return "pages"};return n.Member}
