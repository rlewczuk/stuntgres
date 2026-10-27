package analyze

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

func (b *Binder) bindSelectedHstoreComparison(raw parser.FunctionExpr,selected sqlCallableSelection,symbol string)(expr.Expr,error){
 p:=selected.Procedure;view:=selected.auth.ReadCatalog()
 if err:=extension.StockHstore.ValidateProcedure(view,p);err!=nil{return nil,err}
 if err:=functions.ValidateCallSyntax(b,raw);err!=nil{return nil,err};if err:=functions.CallDecorationError(raw);err!=nil{return nil,err}
 kind:=uint8(9);for i,name:=range extension.HstoreComparisonNames{if name==symbol{kind=uint8(i);break}}
 if kind==9{return nil,fmt.Errorf("stuntgres: invalid installed hstore operation")}
 count:=2;if kind==7{count=1}
 if len(raw.Args)!=count||len(selected.Inputs)!=count||len(raw.ArgNames)!=0||raw.Variadic{return nil,b.functionSignatureError(raw)}
 args:=make([]expr.Expr,count);properties:=expr.Properties{Volatility:expr.Immutable}
 for i:=range args {
  args[i]=selected.args[i]
  if args[i]==nil {operand,err:=b.comparisonOperand(raw.Args[i]);if err!=nil{return nil,err};args[i],err=b.CoerceConditional(operand,selected.Inputs[i],"function");if err!=nil{return nil,err}}
  properties=expr.MergeProperties(properties,args[i].Properties())
 }
 info:=b.Info(b.Type(sqltype.ID(p.Result)).Usage(),raw.Pos,properties);info.RoutineReference=p
 ref:=view.Owner().Domain.Ref(view.Epoch(),p.Address());b.dependency(ref,p.Revision())
 node:=plan.HstoreComparisonExpr{Info:info,Ref:ref,Args:args,Kind:kind,Namespace:view.Get(p.Namespace()).Name()}
 return node.Rebuild(info,args)
}
