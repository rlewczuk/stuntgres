package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGStatAllTablesFeatureSmoke(t *testing.T){
	fixture.StatisticsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		tableStatCreate(t,p,b,c,"sat_smoke");tableStatSeed(t,p,b,"sat_smoke")
		fixture.StatisticsExec(t,p,b,c,"SET stats_fetch_consistency=none",0)
		initial:=tableStatGet(t,p,c,"sat_smoke")
		if initial.Schema!="public"||initial.Name!="sat_smoke"||initial.ID<=0||initial.Inserts!=3||initial.Seq!=0||initial.Read!=0||initial.LastSeq.Valid||initial.Index.Valid||initial.Fetch.Valid||initial.LastIndex.Valid{t.Fatal("fresh published ALL contract",initial)}
		var oid int64;if err:=c.QueryRowContext(p.Ctx,"SELECT oid FROM pg_class WHERE oid='sat_smoke'::regclass").Scan(&oid);err!=nil||oid!=initial.ID{t.Fatal("real catalog relationship",oid,initial.ID,err)}
		rows,err:=c.QueryContext(p.Ctx,"SELECT * FROM pg_catalog.pg_stat_all_tables WHERE relid=$1",oid);if err!=nil{t.Fatal(err)}
		columns,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};tableStatMetadata(t,columns,b.PG);if !rows.Next(){t.Fatal("30-column smoke row missing",rows.Err())};var retained tableStatRow;if err:=rows.Scan(retained.destinations()...);err!=nil{t.Fatal(err)};rows.Close()
		seq,retireSeq:=fixture.StatisticsReportingConn(t,p,b);tableStatSeqPrefer(t,p,b,seq)
		q:="SELECT payload FROM sat_smoke WHERE n=$1"
		tableStatSeqPlan(t,b,indexStatPlan(t,p,seq,q,2))
		fixture.RunStep(t,p.Ctx,seq,b.Recorder,fixture.Select(q,[]string{"payload"},[]string{"TEXT"},[][]any{{nil}},2));retireSeq()
		sequential:=tableStatAwait(t,p,b,"sat_smoke","actual scan1/read3",func(r tableStatRow)bool{return r.Seq==1&&r.Read==3})
		if !sequential.LastSeq.Valid||sequential.Inserts!=3{t.Fatal("actual sequential event",sequential)}
		built:=tableStatBuild(t,p,b,"sat_smoke",3,9,"CREATE INDEX sat_smoke_hash ON sat_smoke USING hash(n)","CREATE INDEX sat_smoke_tree ON sat_smoke(n) WHERE n<0")
		tableStatIndexWant(t,tableStatGet(t,p,c,"sat_smoke"),0,0)
		indexed,retireIndex:=fixture.StatisticsReportingConn(t,p,b);indexStatPrefer(t,p,b,indexed)
		for _,arg:=range []int{2,99}{if !indexStatSimple(t,b,indexStatPlan(t,p,indexed,q,arg)){t.Fatal("smoke did not choose actual simple index")}}
		stmt,err:=indexed.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)}
		for _,tc:=range []struct{arg int;rows [][]any}{{2,[][]any{{nil}}},{99,nil}}{step:=fixture.Select(q,[]string{"payload"},[]string{"TEXT"},tc.rows,tc.arg);fixture.RunStep(t,p.Ctx,fixture.HeldStatement{Executor:indexed,Statement:stmt,SQL:q},b.Recorder,step)};stmt.Close();retireIndex()
		positive:=tableStatAwait(t,p,b,"sat_smoke","prepared hit+miss",func(r tableStatRow)bool{return r.Index.Valid&&r.Index.Int64==2})
		tableStatIndexWant(t,positive,2,1);if positive.Seq!=built.Seq||positive.Read!=built.Read||!positive.LastSeq.Time.Equal(built.LastSeq.Time){t.Fatal("index accesses fabricated sequential activity",positive,built)}
		indexStatWant(t,indexStatGet(t,p,c,"sat_smoke_tree"),0,0,0)
		aborted,retireAbort:=fixture.StatisticsReportingConn(t,p,b)
		fixture.StatisticsExec(t,p,b,aborted,"BEGIN",0);fixture.StatisticsExec(t,p,b,aborted,"INSERT INTO sat_smoke VALUES(4,NULL),(5,'x')",2)
		if got:=tableStatGet(t,p,c,"sat_smoke");got.Inserts!=3{t.Fatal("unresolved inserts published",got)}
		fixture.StatisticsExec(t,p,b,aborted,"ROLLBACK",0);retireAbort()
		tableStatAwait(t,p,b,"sat_smoke","aborted inserts cumulative5",func(r tableStatRow)bool{return r.Inserts==5})
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM sat_smoke",[]string{"count"},[]string{"INT8"},[][]any{{int64(3)}}))
		fixture.StatisticsExec(t,p,b,c,"ANALYZE sat_smoke",0)
		analyzed:=tableStatAwait(t,p,b,"sat_smoke","actual ANALYZE1",func(r tableStatRow)bool{return r.Analyze==1})
		if !analyzed.LastAnalyze.Valid||analyzed.Mods!=0||analyzed.Inserts!=5{t.Fatal("ANALYZE completion/reset",analyzed)}
		fixture.StatisticsExec(t,p,b,c,"VACUUM sat_smoke",0)
		maintained:=tableStatAwait(t,p,b,"sat_smoke","actual VACUUM1",func(r tableStatRow)bool{return r.Vacuum==1})
		if !maintained.LastVacuum.Valid||maintained.Since!=0||maintained.Analyze!=1||maintained.Inserts!=5{t.Fatal("VACUUM completion/reset",maintained)}
		tableStatAutomatic(t,maintained)
		// Only maintenance fields are compared here: the genuine count(*) above
		// may still publish its own read batch through the ordinary throttle.
		for range 2{got:=tableStatGet(t,p,c,"sat_smoke");if got.Vacuum!=maintained.Vacuum||got.Analyze!=maintained.Analyze||got.AutoVacuum!=maintained.AutoVacuum||got.AutoAnalyze!=maintained.AutoAnalyze||!got.LastVacuum.Time.Equal(maintained.LastVacuum.Time)||!got.LastAnalyze.Time.Equal(maintained.LastAnalyze.Time)||got.LastAutoVacuum!=maintained.LastAutoVacuum||got.LastAutoAnalyze!=maintained.LastAutoAnalyze||got.VacuumTime!=maintained.VacuumTime||got.AnalyzeTime!=maintained.AnalyzeTime||got.AutoVacuumTime!=maintained.AutoVacuumTime||got.AutoAnalyzeTime!=maintained.AutoAnalyzeTime{t.Fatal("ALL SELECT changed maintenance",got,maintained)}}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.ErrorDiagnostic("UPDATE pg_stat_all_tables SET seq_scan=0 WHERE FALSE",tableStatDiagnostic("UPDATE")))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM sat_smoke",[]string{"count"},[]string{"INT8"},[][]any{{int64(3)}}))
		fixture.StatisticsExec(t,p,b,c,"DROP TABLE sat_smoke",0)
		if _,err:=tableStatRead(p.Ctx,c,"sat_smoke");err!=sql.ErrNoRows{t.Fatal("DROP retained ALL row",err)}
		if retained.Inserts!=3||retained.Index.Valid||retained.Seq!=0{t.Fatal("retained initial row mutated",retained)};tableStatMetadata(t,columns,b.PG)
	})
}
