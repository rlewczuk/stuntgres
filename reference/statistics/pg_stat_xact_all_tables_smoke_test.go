package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatXactAllTablesFeatureSmoke(t *testing.T) {
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		tableStatCreate(t,p,b,c,"xat_smoke")
		fixture.StatisticsExec(t,p,b,c,"SET stats_fetch_consistency=none",0)
		maintenance:=tableStatGet(t,p,c,"xat_smoke")
		w,retire:=fixture.StatisticsReportingConn(t,p,b)
		fixture.StatisticsExec(t,p,b,w,"BEGIN",0)
		initial:=xactTableGet(t,p,w,"xat_smoke");xactTableCounts(t,initial,0,0,0);if initial.Index.Valid||initial.Fetch.Valid{t.Fatal("no-index SUM must be NULL",initial)}
		stmt,err:=w.PrepareContext(p.Ctx,xactTableSelect);if err!=nil{t.Fatal(err)}
		rows,err:=stmt.QueryContext(p.Ctx,"xat_smoke");if err!=nil{t.Fatal(err)};columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};xactTableMetadata(t,columns,b.PG)
		if !rows.Next(){t.Fatal("prepared row missing",rows.Err())};var held xactTableRow;if err:=rows.Scan(held.destinations()...);err!=nil{t.Fatal(err)};rows.Close();xactTableCounts(t,held,0,0,0)
		fixture.StatisticsExec(t,p,b,w,"INSERT INTO xat_smoke VALUES(1,'a'),(2,NULL)",2)
		xactTableCounts(t,xactTableGet(t,p,w,"xat_smoke"),2,0,0)
		if err:=stmt.QueryRowContext(p.Ctx,"xat_smoke").Scan(held.destinations()...);err!=nil{t.Fatal(err)};xactTableCounts(t,held,2,0,0);stmt.Close()
		xactTableCounts(t,xactTableGet(t,p,c,"xat_smoke"),0,0,0)
		if got:=tableStatGet(t,p,c,"xat_smoke");!got.equal(maintenance){t.Fatal("xact SELECT advanced cumulative maintenance",got,maintenance)}
		xactTableEnd(t,p,b,w,"ROLLBACK")
		fixture.StatisticsExec(t,p,b,w,"BEGIN",0);xactTableCounts(t,xactTableGet(t,p,w,"xat_smoke"),0,0,0);fixture.StatisticsExec(t,p,b,w,"ROLLBACK",0);retire()
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM xat_smoke",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
		published:=tableStatAwait(t,p,b,"xat_smoke","rolled-back executed inserts2",func(r tableStatRow)bool{return r.Inserts==2})
		if published.Analyze!=0||published.Vacuum!=0||published.LastAnalyze.Valid||published.LastVacuum.Valid{t.Fatal("view fabricated maintenance",published)}
		tableStatCreate(t,p,b,c,"xat_smoke_access");tableStatSeed(t,p,b,"xat_smoke_access");tableStatBuild(t,p,b,"xat_smoke_access",1,3,"CREATE INDEX xat_smoke_idx ON xat_smoke_access USING hash(n)")
		reader,retireReader:=fixture.StatisticsReportingConn(t,p,b);defer retireReader();tableStatSeqPrefer(t,p,b,reader);fixture.StatisticsExec(t,p,b,reader,"BEGIN",0)
		xactTableIndex(t,xactTableGet(t,p,reader,"xat_smoke_access"),0,0)
		q:="SELECT payload FROM xat_smoke_access WHERE n=2";tableStatSeqPlan(t,b,indexStatPlan(t,p,reader,q))
		fixture.RunStep(t,p.Ctx,reader,b.Recorder,fixture.Select(q,[]string{"payload"},[]string{"TEXT"},[][]any{{nil}}))
		access:=xactTableGet(t,p,reader,"xat_smoke_access");if access.Seq!=1||access.Read!=3{t.Fatal("actual xact scan1/read3",access)};xactTableIndex(t,access,0,0)
		xactTableEnd(t,p,b,reader,"ROLLBACK")
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.ErrorDiagnostic("UPDATE pg_stat_xact_all_tables SET seq_scan=9 WHERE FALSE",xactTableDiagnostic("UPDATE")))
	})
}
