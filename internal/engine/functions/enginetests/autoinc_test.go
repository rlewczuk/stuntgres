package enginetests_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestAutoincDirectRecipe(t *testing.T) {
	e:=engine.New()
	op,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
	for _,q:=range []string{"CREATE EXTENSION autoinc","CREATE TABLE strict_null_rows(v integer)"} { enginetest.RoleExecute(t,e,op,q) }
	scope,err:=e.StartStatement(op);if err!=nil { t.Fatal(err) };defer scope.Finish(nil)
	r,err:=scope.Execute(enginetest.Statement(t,"SELECT 1"),0,nil);if err!=nil { t.Fatal(err) };if r.Rows!=nil { r.Rows.Close() }
	node,b,evaluation:=strictNullUnitBind(t,e,op,scope,"autoinc()")
	n,ok:=node.(plan.InstalledAutoincExpr);if !ok { t.Fatalf("installed carrier lost: %T",node) }
	d,err:=exec.ExpressionRoutineDescriptor(n)
	if err!=nil || d!=n.Procedure || extension.AutoincProviderOf(d)!=extension.StockAutoinc || d.Result!=2279 || d.Strict || d.Volatility!=expr.Volatile { t.Fatal("selected declaration",d,err) }
	read:=e.CatalogRead()
	typeAddress:=ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:2279}
	typeDependency:=catalog.ExpressionDependency{Ref:read.Owner().Domain.Ref(read.Epoch(),typeAddress),Revision:read.Get(typeAddress).Revision(),Purpose:catalog.DefinitionDependency}
	routineDependency:=catalog.ExpressionDependency{Ref:n.Ref,Revision:d.Revision(),Purpose:catalog.DefinitionDependency}
	if len(b.Dependencies)!=2 || b.Dependencies[0]!=typeDependency || b.Dependencies[1]!=routineDependency { t.Fatal("missing exact result-type/routine dependency inventory",b.Dependencies) }
	if n.PlanningFold()!=expr.FoldNever || n.PreflightFold()!=expr.PreflightKeep || n.IntrinsicVolatility()!=expr.Volatile || len(n.Children())!=0 { t.Fatal("fold/child protocol") }
	info:=n.Info;info.Occurrence++
	rebuilt,err:=n.Rebuild(info,nil)
	if err!=nil { t.Fatal(err) }
	got,ok:=rebuilt.(plan.InstalledAutoincExpr)
	if !ok || got.Ref!=n.Ref || got.Procedure!=n.Procedure || got.Info!=info { t.Fatal("rebuild lost immutable authority",rebuilt) }
	if _,err:=n.Rebuild(info,[]expr.Expr{n});err==nil { t.Fatal("hidden child accepted") }
	retagged,changed,err:=stored.RetagTypes(e.CatalogRead(),n,func(d *sqltype.Descriptor)*sqltype.Descriptor{return d})
	if err!=nil || changed || !reflect.DeepEqual(retagged,n) { t.Fatal("retag changed unchanged recipe",retagged,changed,err) }
	capture,ok,err:=n.Capture(expr.CaptureFunction)
	if err!=nil || !ok || capture.Count!=0 { t.Fatal(capture,ok,err) }
	render,err:=n.Render();if err!=nil || render.Name!="autoinc" { t.Fatal(render,err) }
	want:=sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"XX000",Message:"not fired by trigger manager"}
	before:=n
	for _,source:=range []bool{false,true} {
		if source { _,err=n.OpenSource(&evaluation) } else { _,err=n.Eval(&evaluation) }
		var diagnostic *sqlstate.Error
		if !errors.As(err,&diagnostic) || *diagnostic!=want || n!=before { t.Fatal("scalar borrowed activation or changed recipe",err) }
	}
	forged:=*d;forged.Origin=nil
	bad:=n;bad.Procedure=&forged;bad.RoutineReference=&forged
	if _,err:=bad.Eval(&evaluation);err==nil { t.Fatal("forged origin evaluated") }
	state:=catalog.CloneState(e.CatalogRead().State());state.SetDescriptor(d.Address(),&forged)
	if err:=stored.ValidateInstalledAutoinc(bad,catalog.NewRead(e.CatalogRead().Owner(),state));err==nil { t.Fatal("forged stored origin accepted") }
}
