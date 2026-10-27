package plan

import (
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

type InstalledPageinspectExpr struct{functions.Pageinspect;Procedure *catalog.Procedure;Ref ids.ObjectRef}
func(n InstalledPageinspectExpr)Eval(ctx expr.Context)(sqltype.Value,error){
 runtime,ok:=ctx.(interface{EvalPageinspect(InstalledPageinspectExpr)(sqltype.Value,error)})
 if !ok{return sqltype.Value{},fmt.Errorf("stuntgres: missing installed pageinspect runtime")};return runtime.EvalPageinspect(n)
}
func(n InstalledPageinspectExpr)OpenSource(ctx expr.Context)(expr.SourceCursor,error){
 runtime,ok:=ctx.(interface{OpenPageinspect(InstalledPageinspectExpr)(expr.SourceCursor,error)})
 if !ok{return expr.SourceCursor{},fmt.Errorf("stuntgres: missing installed pageinspect source runtime")};return runtime.OpenPageinspect(n)
}
func(n InstalledPageinspectExpr)Rebuild(info expr.Info,children []expr.Expr)(expr.Expr,error){
 if info.RoutineReference!=n.Procedure{return nil,fmt.Errorf("stuntgres: pageinspect lost installed identity")}
 call,err:=n.Pageinspect.Rebuild(info,children);if err!=nil{return nil,err};n.Pageinspect=call.(functions.Pageinspect);return n,nil
}
func(n InstalledPageinspectExpr)Substitute(info expr.Info,children []expr.Expr)(expr.Expr,error){return n.Rebuild(info,children)}
func(n InstalledPageinspectExpr)SameSourceOccurrence(other expr.Expr)bool{o,ok:=other.(InstalledPageinspectExpr);return ok && n.Ref==o.Ref && n.Procedure==o.Procedure && sqltype.SameRecordUsage(n.ResultUsage(),o.ResultUsage())}
func(n InstalledPageinspectExpr)ScalarSourceOutName()string{if n.Member=="brin_revmap_data"{return "pages"};return n.Procedure.Name()}
