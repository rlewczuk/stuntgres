package functions

import (
 "fmt"
 "stuntgres/internal/engine/expr"
)
func(ServerSignal) IntrinsicVolatility()expr.Volatility{return expr.Volatile}
func(ServerSignal) PlanningFold()expr.Fold{return expr.FoldNever}
func(ServerSignal) StrictNull()expr.Strict{return expr.StrictAfterBare}
func(ServerSignal) PreflightFold()expr.Preflight{return expr.PreflightNullBare}
func(ServerSignal) LooksLikeFunction()bool{return true}
func(ServerSignal) CallCost()int{return 1}
func(ServerSignal) ClosedDefault(func(expr.Expr)bool)bool{return false}
func(ServerSignal) SourceAdmission()(bool,bool){return true,false}
func(n ServerSignal) Children()[]expr.Expr{if n.Kind==SignalMemory{return n.Args[:]};return nil}
func(n ServerSignal) SameGroup(other expr.Expr,same func(expr.Expr,expr.Expr)bool,_ bool)bool{o,ok:=other.(ServerSignal);return ok && n.Kind==o.Kind && (n.Kind!=SignalMemory || same(n.Args[0],o.Args[0]))}
func(n ServerSignal) Substitute(info expr.Info,children []expr.Expr)(expr.Expr,error){
 if len(children)!=len(n.Children()){return nil,fmt.Errorf("stuntgres: invalid server signal reconstruction")}
 n.Info=info;if n.Kind==SignalMemory{n.Args[0]=children[0]};if err:=n.Validate();err!=nil{return nil,err};return n,nil
}
func(n ServerSignal) Rebuild(info expr.Info,children []expr.Expr)(expr.Expr,error){return n.Substitute(info,children)}
func(n ServerSignal) Render()(expr.Rendering,error){
 if err:=n.Validate();err!=nil{return expr.Rendering{},err}
 var args []expr.RenderArg;if n.Kind==SignalMemory{args=[]expr.RenderArg{{Expr:n.Args[0]}}}
 return expr.Rendering{Tree:"ServerSignal",Inputs:n.Children(),Form:expr.FormCall,Name:n.Kind.Name(),Args:args},nil
}
func(n ServerSignal) Capture(kind expr.CaptureKind)(expr.CaptureOperands,bool,error){if err:=n.Validate();err!=nil{return expr.CaptureOperands{},false,err};return expr.CaptureOperands{Count:len(n.Children()),Indexed:true},kind==expr.CaptureFunction,nil}
func(n ServerSignal) CaptureOperand(written int)(expr.Expr,error){if n.Kind!=SignalMemory || written!=0{return nil,fmt.Errorf("stuntgres: invalid server signal capture")};return n.Args[0],nil}
func(n ServerSignal) ValidateStoredProvider()error{return n.Validate()}
