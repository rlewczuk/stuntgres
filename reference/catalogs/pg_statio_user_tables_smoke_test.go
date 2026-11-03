package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioUserTablesFeatureSmoke(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		a,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
		statioSequenceExec(t,p,b,a,"CREATE TABLE statio_user_smoke(k integer)")
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_user_smoke VALUES(1),(2)",2))
		oid:=statioSequenceOID(t,p,a,"public.statio_user_smoke")
		held,stmt:=statioUserTableHeld(t,p,observer);defer stmt.Close()
		statioTableWork(t,p,b,nil,"SELECT k FROM statio_user_smoke ORDER BY k",[][]any{{int64(1)},{int64(2)}})
		statioSequenceExec(t,p,b,observer,"BEGIN");defer observer.ExecContext(context.Background(),"ROLLBACK")
		statioSequenceExec(t,p,b,observer,"SET LOCAL stats_fetch_consistency=snapshot")
		for _,e:=range []fixture.Executor{observer,held} {
			r:=statioUserTableOne(t,p,b,e,oid)
			if r.schema!="public" || r.name!="statio_user_smoke" {t.Fatal("actual user identity",r)}
			statioTableHeap(t,b,r,1);statioTableAbsentToast(t,r)
			if r.blocks[2].Valid || r.blocks[3].Valid {t.Fatal("absent index SUM is NULL",r)}
			if got:=statioUserTableCompanions(t,p,b,observer,oid);got!=r {t.Fatal("shared snapshot",got,r)}
			if rows:=statioTableRows(t,p,b,e,statioUserTableProbe,int64(0));len(rows)!=0 {t.Fatal("typed missing identity",rows)}
		}
		if rows:=statioTableRows(t,p,b,observer,"SELECT * FROM pg_statio_user_tables WHERE false");len(rows)!=0 {t.Fatal("typed empty",rows)}
		want:=statioTableWriteDiagnostic("update")
		_,err:=a.ExecContext(p.Ctx,"UPDATE pg_statio_user_tables SET heap_blks_hit=0 WHERE false");fixture.AssertDiagnostic(t,err,&want)
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT k FROM statio_user_smoke ORDER BY k",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
		statioSequenceExec(t,p,b,observer,"ROLLBACK")
		statioSequenceExec(t,p,b,a,"DROP TABLE statio_user_smoke")
		if rows:=statioTableRows(t,p,b,held,statioUserTableProbe,oid);len(rows)!=0 {t.Fatal("held USER retained dropped identity",rows)}
		for _,q:=range []string{statioTableProbe,"SELECT * FROM pg_statio_sys_tables WHERE relid=$1::oid"} {if rows:=statioTableRows(t,p,b,observer,q,oid);len(rows)!=0 {t.Fatal("dropped companion identity",q,rows)}}
	})}
}
