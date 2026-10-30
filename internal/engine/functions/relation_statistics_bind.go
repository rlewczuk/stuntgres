package functions

import (
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

var relationStatisticsNames = []string{"pg_restore_relation_stats", "pg_clear_relation_stats"}
var clearRelationStatisticsTypes = []sqltype.ID{sqltype.Text,sqltype.Text}
var clearRelationStatisticsLabels = []string{"schemaname","relname"}

func bindRelationStatistics(b Binder, raw parser.FunctionExpr) (expr.Expr,error) {
 name:=raw.Name.Parts[len(raw.Name.Parts)-1].Text
 clear:=name=="pg_clear_relation_stats"
 operands:=make([]Argument,min(100,len(raw.Args)))
 names:=make([]string,len(operands))
 for i,child:=range raw.Args {
  intent:=ProjectionArgument; if clear {intent=ComparisonArgument}
  a,err:=b.AnalyzeArgument(child,AnalysisRequest{Intent:intent});if err!=nil{return nil,err}
  if i<len(operands){operands[i]=a;names[i]="unknown";if a.Usage.Type!=nil{names[i]=b.DiagnosticTypeName(a.Usage.Type)};names[i]=NamedArgumentType(raw,i,names[i])}
 }
 if len(raw.Args)>100{return nil,expr.ErrorAt("54023","cannot pass more than 100 arguments to a function",raw.Pos)}
 if err:=ValidateCallSyntax(b,raw);err!=nil{return nil,err}
 visible,err:=b.BuiltinVisible(raw.Name);if err!=nil{return nil,err}
 var slots [7]int
 valid:=len(raw.Args)>0
 if clear {slots,valid=namedSlots(raw,clearRelationStatisticsLabels,2);valid=valid&&len(raw.Args)==2&&!raw.Variadic} else {
  if raw.Variadic {slots,valid=namedSlots(raw,[]string{"kwargs"},1)} else {
   for _,label:=range raw.ArgNames {if label.Text!="" {valid=false}}
  }
 }
 if !visible||!valid||raw.ResolutionOnly{return nil,b.SignatureError(raw.Name,raw.Pos,names)}
 node:=RelationStatistics{Clear:clear,Variadic:raw.Variadic,Args:make([]expr.Expr,len(operands))}
 properties:=expr.Properties{Volatility:expr.Volatile}
 for i,a:=range operands {
  arg:=a.Expression
  if clear {
   target:=b.Type(clearRelationStatisticsTypes[slots[i]]).Usage()
   if a.Usage.Type!=nil {if _,failure:=sqltype.PlanCoercion(a.Usage,target,sqltype.Implicit);failure!=nil{return nil,b.SignatureError(raw.Name,raw.Pos,names)}}
   arg,err=b.CoerceArgument(a,CoercionRequest{Intent:ExactFormalArgument,Target:target})
   node.Slots[i]=uint8(slots[i])
  } else if a.Usage.Type==nil && !b.RetainUntypedParameter(arg) {
   arg,err=b.CoerceArgument(a,CoercionRequest{Intent:FinalizeArgument,Target:b.Type(sqltype.Text).Usage()})
  }
  if err!=nil{return nil,err}
  if (clear||raw.Variadic)&&len(raw.ArgNames)!=0{node.Labels[i]=raw.ArgNames[i].Text}
  node.Args[i]=arg;properties=expr.MergeProperties(properties,arg.Properties())
 }
 if raw.Variadic && (node.Args[0].ResultUsage().Type==nil||!node.Args[0].ResultUsage().Type.BaseType().IsArray()){return nil,expr.ErrorAt("42804","VARIADIC argument must be an array",raw.Args[0].SourcePosition())}
 if raw.Star{return nil,expr.ErrorAt("42809",name+"(*) specified, but "+name+" is not an aggregate function",raw.Pos)}
 if err:=CallDecorationError(raw);err!=nil{return nil,err}
 result:=b.Type(sqltype.Bool);formal:=[]sqltype.ID{2276}
 provider:=VariadicRoutineProvider
 if clear{result=b.Type(sqltype.Void);formal=clearRelationStatisticsTypes;provider=CatalogRoutineProvider}
 node.Info,err=b.BuiltinRoutineInfo(b.Info(result.Usage(),raw.Pos,properties),provider,name,formal...)
 return node,err
}
func namedRelationStatistics(b CallTypeContext,raw parser.FunctionExpr,args []Argument,declared []sqltype.Usage)([]sqltype.Usage,[]sqltype.Usage,bool){
 name:=raw.Name.Parts[len(raw.Name.Parts)-1].Text
 if name=="pg_clear_relation_stats"&&declaredTypes(declared,clearRelationStatisticsTypes){return fixedNamedInputs(b,raw,args,declared,clearRelationStatisticsLabels,2)}
 if name=="pg_restore_relation_stats"&&raw.Variadic&&declaredTypes(declared,[]sqltype.ID{2276}){return fixedNamedInputs(b,raw,args,declared,[]string{"kwargs"},1)}
 return nil,nil,false
}
