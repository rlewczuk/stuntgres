package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

// CitextStringCapture carries installed identity, never cached permission.
// The original checked declaration remains opaque in Info.RoutineReference.
type CitextStringCapture struct {
 Ref ids.ObjectRef
 Namespace,Name string
 Member uint8
 Text sqltype.Usage
}
func NewCitextString(info expr.Info,name string,args []expr.Expr,capture CitextStringCapture) (String,error) {
 call:=String{Info:info,Args:args,Syntax:parser.OrdinaryStringSyntax,Citext:&capture}
 if len(args)<2||len(args)>3 {return call,fmt.Errorf("stuntgres: invalid citext string arity")}
 var usages [4]sqltype.Usage
 for i,arg:=range args {if arg==nil {return call,fmt.Errorf("stuntgres: missing citext string argument")};usages[i]=arg.ResultUsage();call.Textual[i]=name!="split_part"||i!=2}
 plan,err:=sqltype.BindCitextString(name,info.Usage,capture.Text,usages[:len(args)]);call.Plan=plan
 if err!=nil {return call,err}
 return call,call.validateCitext()
}
func (call String) validateCitext() error {
 if call.Citext==nil {if call.Plan.Citext() {return fmt.Errorf("stuntgres: missing citext string capture")};return nil}
 c:=call.Citext
 if !call.Plan.Citext()||call.RoutineReference==nil||c.Name==""||c.Namespace==""||c.Ref.Address.ClassID!=ids.ProcedureClassID||c.Ref.Address.ObjectID==0||c.Ref.Address.SubID!=0||c.Text!=call.Plan.TextUsage()||call.Syntax!=parser.OrdinaryStringSyntax {return fmt.Errorf("stuntgres: invalid captured citext string")}
 for i:=range call.Textual {
  want:=i<len(call.Args)&&(call.Plan.Name()!="split_part"||i!=2)
  if call.Textual[i]!=want {return fmt.Errorf("stuntgres: invalid citext string argument roles")}
 }
 return nil
}
func (call String) citextRendering() (expr.Rendering,error) {
 if err:=call.Validate();err!=nil {return expr.Rendering{},err}
 args:=make([]expr.RenderArg,len(call.Args))
 for i,arg:=range call.Args {args[i]=expr.RenderArg{Expr:arg,Transport:expr.TransportCast,TransportType:call.Plan.ArgumentUsage(i)}}
 return expr.Rendering{Tree:"CitextString "+call.Citext.Namespace+"."+call.Citext.Name,Inputs:call.Args,Form:expr.FormCall,Name:call.Citext.Name,Namespace:call.Citext.Namespace,Quote:expr.QuoteIdentifier,Args:args,Replay:expr.Replay{QuoteNamespace:true}},nil
}
func (call String) PreflightFold() expr.Preflight {
 if call.Citext!=nil {return expr.PreflightRegexNullOrEvaluate}
 return expr.PreflightSimplify
}
