package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregateViews(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE VIEW os_view AS SELECT percentile_cont(.25) WITHIN GROUP(ORDER BY x) AS lo,percentile_cont(.75) WITHIN GROUP(ORDER BY x) AS hi,percentile_disc('{0,1}'::float8[]) WITHIN GROUP(ORDER BY x) AS edges,mode() WITHIN GROUP(ORDER BY x DESC) AS m FROM os_input",0))
		orderedSetBoth(t,p,c,b,fixture.Select("SELECT lo,hi,edges::text,m FROM os_view",[]string{"lo","hi","edges","m"},[]string{"FLOAT8","FLOAT8","TEXT","INT4"},[][]any{{float64(17.5),float64(32.5),"{10,40}",int64(40)}}))
		var definition string
		if err:=c.QueryRowContext(p.Ctx,"SELECT pg_get_viewdef('os_view'::regclass)").Scan(&definition);err!=nil {t.Fatal(err)}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE VIEW os_rebound AS "+definition,0))
		orderedSetBoth(t,p,c,b,fixture.Select("SELECT lo,hi,edges::text,m FROM os_rebound",[]string{"lo","hi","edges","m"},[]string{"FLOAT8","FLOAT8","TEXT","INT4"},[][]any{{float64(17.5),float64(32.5),"{10,40}",int64(40)}}))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE VIEW os_group_view AS SELECT p,percentile_cont(p) WITHIN GROUP(ORDER BY x::float8) FILTER(WHERE x IS NOT NULL) AS v,percentile_disc('[-1:0]={1,0}'::float8[]) WITHIN GROUP(ORDER BY x DESC) AS edges,mode() WITHIN GROUP(ORDER BY x DESC) AS m FROM os_input GROUP BY p",0))
		grouped:=fixture.Select("SELECT p,v,edges::text AS edges,m FROM os_group_view ORDER BY p NULLS LAST",[]string{"p","v","edges","m"},[]string{"FLOAT8","FLOAT8","TEXT","INT4"},[][]any{{float64(.25),float64(12.5),"[-1:0]={10,20}",int64(20)},{float64(.75),float64(37.5),"[-1:0]={30,40}",int64(40)},{nil,nil,nil,nil}})
		orderedSetBoth(t,p,c,b,grouped)
		if err:=c.QueryRowContext(p.Ctx,"SELECT pg_get_viewdef('os_group_view'::regclass)").Scan(&definition);err!=nil {t.Fatal(err)}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE VIEW os_group_rebound AS "+definition,0))
		grouped.Query="SELECT p,v,edges::text AS edges,m FROM os_group_rebound ORDER BY p NULLS LAST"
		orderedSetBoth(t,p,c,b,grouped)
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT pg_catalog.\"percentile_cont\"(.5) WITHIN GROUP(ORDER BY x) AS v FROM os_input","FLOAT8",float64(25)))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE within(mode INT)",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO within VALUES(7)",1))
		orderedSetBoth(t,p,c,b,fixture.Select("SELECT mode FROM within",[]string{"mode"},[]string{"INT4"},[][]any{{int64(7)}}))
	})
}

func TestReferenceOrderedSetAggregateConsumers(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		q:="WITH a AS (SELECT p,percentile_cont(p) WITHIN GROUP(ORDER BY x) AS v FROM os_input WHERE p IS NOT NULL GROUP BY p HAVING percentile_cont(p) WITHIN GROUP(ORDER BY x)>0) SELECT v FROM a ORDER BY v"
		orderedSetBoth(t,p,c,b,fixture.Select(q,[]string{"v"},[]string{"FLOAT8"},[][]any{{float64(12.5)},{float64(37.5)}}))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_dest(v FLOAT8)",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO os_dest SELECT percentile_cont(p) WITHIN GROUP(ORDER BY x) FROM os_input WHERE p IS NOT NULL GROUP BY p",2))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO os_dest SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x) FROM os_input WHERE FALSE",1))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO os_dest SELECT percentile_cont(p) WITHIN GROUP(ORDER BY x) FROM os_input WHERE FALSE GROUP BY p",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO os_input VALUES(60,1.1)",1))
		bad:="INSERT INTO os_dest SELECT percentile_cont(p) WITHIN GROUP(ORDER BY x) FROM os_input WHERE p IS NOT NULL GROUP BY p"
		s:=orderedSetDiagnostic(bad,"22003","percentile value 1.1 is not between 0 and 1","","");s.SelectRows=false;s.ErrorStage="execute"
		orderedSetBoth(t,p,c,b,s)
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT count(*) AS v FROM os_dest","INT8",int64(3)))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_ctas AS SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x) AS v FROM os_input",1))
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT v FROM os_ctas","FLOAT8",float64(30)))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE MATERIALIZED VIEW os_mat AS SELECT percentile_disc(.5) WITHIN GROUP(ORDER BY x) AS v FROM os_input",1))
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT v FROM os_mat","INT4",int64(30)))
	})
}

func TestReferenceOrderedSetAggregatePrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"os_reader"})
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE ROLE os_reader",0))
		if b.PG {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("GRANT USAGE ON SCHEMA public TO os_reader",0))}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("GRANT SELECT(x) ON os_input TO os_reader",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("SET SESSION AUTHORIZATION os_reader",0))
		defer func(){if _,err:=c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION");err!=nil {t.Error(err)}}()
		for _, q:=range []string{
			"SELECT percentile_cont(p) WITHIN GROUP(ORDER BY x) FROM os_input WHERE FALSE GROUP BY p",
			"SELECT mode() WITHIN GROUP(ORDER BY p) FILTER(WHERE FALSE) FROM os_input",
			"SELECT mode() WITHIN GROUP(ORDER BY x) FILTER(WHERE p>0) FROM os_input HAVING FALSE",
		} {
			s:=orderedSetDiagnostic(q,"42501","permission denied for table os_input","","");s.ErrorStage="execute"
			orderedSetBoth(t,p,c,b,s)
		}
	})
}
