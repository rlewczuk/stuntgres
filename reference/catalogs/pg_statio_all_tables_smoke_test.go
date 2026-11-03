package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioAllTablesFeatureSmoke(t *testing.T) {
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		a,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
		statioSequenceExec(t,p,b,a,"CREATE TABLE statio_tables_smoke(k integer)")
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Exec("INSERT INTO statio_tables_smoke VALUES(1),(2)",2))
		oid:=statioSequenceOID(t,p,a,"public.statio_tables_smoke")
		statioSequenceExec(t,p,b,observer,"SET stats_fetch_consistency=none")
		stmt,err:=observer.PrepareContext(p.Ctx,statioTableProbe);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:observer,Statement:stmt,SQL:statioTableProbe}
		for _,e:=range []fixture.Executor{observer,held} {
			r:=statioTableOne(t,p,b,e,oid)
			if r.schema!="public" || r.name!="statio_tables_smoke" || r.blocks[2].Valid || r.blocks[3].Valid {t.Fatal("eleven-column actual identity and empty SUM",r)}
			statioTableAbsentToast(t,r);statioTableHeap(t,b,r,0)
		}
		statioTableWork(t,p,b,nil,"SELECT k FROM statio_tables_smoke ORDER BY k",[][]any{{int64(1)},{int64(2)}})
		statioTableHeap(t,b,statioTableOne(t,p,b,held,oid),1)
		statioSequenceExec(t,p,b,a,"CREATE INDEX statio_tables_smoke_i ON statio_tables_smoke USING hash(k)")
		statioTableWork(t,p,b,[]string{"SET enable_seqscan=off"},"SELECT k FROM statio_tables_smoke WHERE k=1",[][]any{{int64(1)}})
		r:=statioTableIndexSum(t,p,b,observer,oid)
		statioTableAbsentToast(t,r)
		if !r.blocks[2].Valid || !r.blocks[3].Valid {t.Fatal("real index numeric aggregate",r)}
		predicate:="schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast'"
		for _,c:=range []struct{filter string;count int64}{{"NOT ("+predicate+")",1},{"("+predicate+")",0}} {
			fixture.RunStep(t,p.Ctx,observer,b.Recorder,fixture.Select("SELECT count(*) FROM pg_statio_all_tables WHERE relid=$1::oid AND "+c.filter,[]string{"count"},[]string{"INT8"},[][]any{{c.count}},oid))
		}
		want:=statioTableWriteDiagnostic("update")
		_,err=a.ExecContext(p.Ctx,"UPDATE pg_statio_all_tables SET heap_blks_hit=0 WHERE false");fixture.AssertDiagnostic(t,err,&want)
		fixture.RunStep(t,p.Ctx,a,b.Recorder,fixture.Select("SELECT k FROM statio_tables_smoke ORDER BY k",[]string{"k"},[]string{"INT4"},[][]any{{int64(1)},{int64(2)}}))
		statioSequenceExec(t,p,b,a,"DROP TABLE statio_tables_smoke")
		if got:=statioTableRows(t,p,b,held,statioTableProbe,oid);len(got)!=0 {t.Fatal("held query retained dropped identity",got)}
	})}
}
