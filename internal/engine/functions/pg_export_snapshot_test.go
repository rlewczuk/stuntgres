package functions

import (
	"testing"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestSnapshotExportProviderProtocol(t *testing.T) {
	types:=temporalTestTypes()
	n:=snapshotExpression{Info:expr.Info{Usage:types[sqltype.Text].Usage(),Property:expr.Properties{Volatility:expr.Volatile}},kind:snapshotExport}
	if err:=n.ValidateStoredProvider();err!=nil{t.Fatal(err)}
	if len(n.Children())!=0||n.IntrinsicVolatility()!=expr.Volatile||n.PlanningFold()!=expr.FoldNever||n.PreflightFold()!=expr.PreflightChildren{t.Fatal("zero-argument volatile provider protocol")}
	if a,b,c:=n.SnapshotDemand(func(expr.Expr)bool{return false});a||b||!c||!n.ExportRootDemand(){t.Fatal("narrow root demand")}
	if nullable,known:=n.ResultNullability(expr.ResultSelect);nullable||!known{t.Fatal("TEXT result must be non-NULL")}
	rebuilt,err:=n.Rebuild(n.Info,nil);if err!=nil{t.Fatal(err)}
	if !n.SameGroup(rebuilt,func(a,b expr.Expr)bool{return a==b},false){t.Fatal("reconstruction lost identity")}
	render,err:=rebuilt.(expr.Renderable).Render();if err!=nil||render.Name!="pg_export_snapshot"||len(render.Inputs)!=0{t.Fatal(render,err)}
	if _,ok,err:=n.Capture(expr.CaptureFunction);err!=nil||!ok{t.Fatal("function capture",ok,err)}
	bad:=n;bad.legacy=true;if err:=bad.ValidateStoredProvider();err==nil{t.Fatal("accepted nonexistent legacy export")}
	bad=n;bad.Usage=types[sqltype.PgSnapshot].Usage();if err:=bad.ValidateStoredProvider();err==nil{t.Fatal("accepted snapshot rather than TEXT metadata")}
	if _,err:=n.Rebuild(n.Info,[]expr.Expr{temporalTestLiteral(sqltype.String(types[sqltype.Text],"x"))});err==nil{t.Fatal("accepted argument to noarg provider")}
}
