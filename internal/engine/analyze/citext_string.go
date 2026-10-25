package analyze

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

func (b *Binder) bindSelectedCitextString(raw parser.FunctionExpr,selected sqlCallableSelection,symbol string) (expr.Expr,error) {
 procedure:=selected.Procedure;view:=selected.auth.ReadCatalog()
 if err:=extension.StockCitext.ValidateProcedure(view,procedure);err!=nil {return nil,err}
 // The shared selector has transformed operands/FILTER and rejected scalar
 // decorations before coercing the selected formals.
 if err:=functions.ValidateCallSyntax(b,raw);err!=nil {return nil,err}
 if len(raw.ArgNames)!=0||raw.Variadic {return nil,b.functionSignatureError(raw)}
 args:=make([]expr.Expr,len(raw.Args));properties:=expr.Properties{Volatility:expr.Immutable}
 for i,rawArg:=range raw.Args {
  argument:=selected.args[i]
  if argument==nil {
   operand,err:=b.comparisonOperand(rawArg);if err!=nil {return nil,err}
   argument,err=b.CoerceConditional(operand,selected.Inputs[i],"function");if err!=nil {return nil,err}
  }
  args[i]=argument;properties=expr.MergeProperties(properties,argument.Properties())
 }
 installation,ok:=view.Extension(procedure.Extension);if !ok {return nil,fmt.Errorf("stuntgres: missing citext string installation")}
 member:=-1;for i,address:=range installation.Members {if address==procedure.Address() {member=i;break}}
 if member<extension.CitextStringProcedureStart||member>=extension.CitextMemberCount {return nil,fmt.Errorf("stuntgres: invalid citext string member")}
 input,err:=b.CollatedInfo(b.Info(args[0].ResultUsage(),raw.Pos,properties),args...);if err!=nil {return nil,err}
 info:=b.Info(b.Type(sqltype.ID(procedure.Result)).Usage(),raw.Pos,properties)
 if info.Usage.Collation.ID!=0 {info.Usage.Collation=input.Usage.Collation;info.CollationConflict=input.CollationConflict}
 info.RoutineReference=procedure
 ref:=view.Owner().Domain.Ref(view.Epoch(),procedure.Address());b.dependency(ref,procedure.Revision())
 capture:=functions.CitextStringCapture{Ref:ref,Namespace:view.Get(procedure.Namespace()).Name(),Name:procedure.Name(),Member:uint8(member),Text:b.Type(sqltype.Text).Usage()}
 return functions.NewCitextString(info,symbol,args,capture)
}
