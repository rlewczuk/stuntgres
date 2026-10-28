package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceLtreeLqueryFeatureSmoke(t *testing.T){
	ltreeLqueryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		ltreeInstall(t,p,b,c)
		run:=func(s fixture.Step){ltreeLqueryRun(t,p,b,c,s)}
		run(fixture.Exec("CREATE TABLE lq_smoke(id integer NOT NULL,path ltree)",0))
		run(fixture.Exec("INSERT INTO lq_smoke VALUES (1,'Top.A.Child'),(2,'Top.A.B.Child'),(3,'Top.Child'),(4,NULL)",4))
		projection:=ltreeLqueryProjection
		want:=[][]any{{int64(1),true,true,true},{int64(2),true,true,true},{int64(3),false,false,false},{int64(4),nil,nil,nil}}
		check:=func(){
			s:=fixture.Select("SELECT id,"+projection+" FROM lq_smoke ORDER BY id",[]string{"id","forward","reverse","alternative"},[]string{"INT4","BOOL","BOOL","BOOL"},want)
			s.CheckMetadata=func(t *testing.T,rows *sql.Rows){columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};if len(columns)!=4{t.Fatal("smoke metadata count")};ltreeArrayColumnMetadata(t,b,columns[1:],[]string{"BOOL","BOOL","BOOL"},nil)}
			run(s);run(fixture.Prepared(s))
		}
		check()
		stmt,err:=c.PrepareContext(p.Ctx,ltreeLqueryHeldSQL);if err!=nil{t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:ltreeLqueryHeldSQL}
		ltreeLqueryHeldChecks(t,p,b,held)
		s:=fixture.ErrorDiagnostic(ltreeLqueryHeldSQL,fixture.ControlsReferenceDiagnostic("42601","lquery syntax error at character 3"),"Top.A.Child","a..b");s.SelectRows=true;s.ErrorStage="execute";ltreeLqueryRun(t,p,b,held,s)
		ltreeLqueryRun(t,p,b,held,ltreeLqueryPair(b,ltreeLqueryHeldSQL,true,"Top.A.Child","Top.*{1,2}.Child"))
		ltreeLqueryUpdateAtomicity(t,p,b,c,"lq_smoke_atomic")
		run(fixture.Exec("CREATE VIEW lq_smoke_view AS SELECT id,"+projection+" FROM lq_smoke",0))
		if !b.PG{
			dump:=ltreeHierarchyDump(t,p)
			if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
			if err:=stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump));err!=nil{t.Fatal(err)}
			check();ltreeLqueryHeldChecks(t,p,b,held)
		}
		run(fixture.Select("SELECT id,forward,reverse,alternative FROM lq_smoke_view ORDER BY id",[]string{"id","forward","reverse","alternative"},[]string{"INT4","BOOL","BOOL","BOOL"},want))
	})
}
