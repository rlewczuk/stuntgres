package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioSysTablesFeatureSmoke(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		a,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
		statioSequenceExec(t,p,b,a,"CREATE TABLE statio_sys_smoke(k integer)")
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_sys_smoke VALUES(1),(2)",2))
		oid:=statioSequenceOID(t,p,a,"statio_sys_smoke")
		statioTableWork(t,p,b,nil,"SELECT k FROM statio_sys_smoke ORDER BY k",[][]any{{int64(1)},{int64(2)}})
		held,stmt:=statioSysTableHeld(t,p,observer);defer stmt.Close()
		allStmt,err:=observer.PrepareContext(p.Ctx,statioTableProbe);if err!=nil {t.Fatal(err)};defer allStmt.Close()
		allHeld:=fixture.HeldStatement{Executor:observer,Statement:allStmt,SQL:statioTableProbe}
		statioSequenceExec(t,p,b,observer,"BEGIN");defer observer.ExecContext(context.Background(),"ROLLBACK")
		statioSequenceExec(t,p,b,observer,"SET LOCAL stats_fetch_consistency=snapshot")
		for _,e:=range []fixture.Executor{observer,allHeld} {r:=statioTableOne(t,p,b,e,oid);if r.schema!="public" || r.name!="statio_sys_smoke" {t.Fatal("user identity",r)};statioTableHeap(t,b,r,1);statioTableAbsentToast(t,r);if r.blocks[2].Valid || r.blocks[3].Valid {t.Fatal("empty index sums",r)}}
		for _,e:=range []fixture.Executor{observer,held} {statioSysTableEmpty(t,p,b,e,oid);sys:=statioSysTablePositive(t,p,b,e,1259);if all:=statioTableOne(t,p,b,allHeld,1259);all!=sys {t.Fatal("nonvacuous shared ALL/SYS snapshot",all,sys)}}
		statioSequenceExec(t,p,b,observer,"ROLLBACK")
		want:=statioTableWriteDiagnostic("update")
		_,err=a.ExecContext(p.Ctx,"UPDATE pg_catalog.pg_statio_sys_tables SET heap_blks_hit=0 WHERE false");fixture.AssertDiagnostic(t,err,&want)
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT k FROM statio_sys_smoke ORDER BY k",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
		statioSequenceExec(t,p,b,a,"DROP TABLE statio_sys_smoke")
		statioSysTableEmpty(t,p,b,held,oid)
		if rows:=statioTableRows(t,p,b,allHeld,statioTableProbe,oid);len(rows)!=0 {t.Fatal("dropped identity retained",rows)}
		statioSysTablePositive(t,p,b,held,1259)
	})}
}
