package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregateOwnership(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		orderedSetBoth(t,p,c,b,orderedSetValue("SELECT (SELECT percentile_cont(.25) WITHIN GROUP(ORDER BY o.x)) AS v FROM os_input o","FLOAT8",float64(17.5)))
		for _,q:=range []string{
			"SELECT p,(SELECT percentile_cont(o.p) WITHIN GROUP(ORDER BY i.x) FROM os_input i) AS v FROM os_input o GROUP BY p ORDER BY p NULLS LAST",
			"SELECT p,(SELECT percentile_cont(o.p) WITHIN GROUP(ORDER BY o.x)) AS v FROM os_input o GROUP BY p ORDER BY p NULLS LAST",
		} {
			rows:=[][]any{{float64(.25),float64(17.5)},{float64(.75),float64(32.5)},{nil,nil}}
			if q=="SELECT p,(SELECT percentile_cont(o.p) WITHIN GROUP(ORDER BY o.x)) AS v FROM os_input o GROUP BY p ORDER BY p NULLS LAST" {rows=[][]any{{float64(.25),float64(12.5)},{float64(.75),float64(37.5)},{nil,nil}}}
			orderedSetBoth(t,p,c,b,fixture.Select(q,[]string{"p","v"},[]string{"FLOAT8","FLOAT8"},rows))
		}
		q:="SELECT (SELECT percentile_cont(i.p) WITHIN GROUP(ORDER BY o.x) FROM os_input i LIMIT 1) FROM os_input o"
		orderedSetBoth(t,p,c,b,orderedSetDiagnostic(q,"42803","outer-level aggregate cannot contain a lower-level variable in its direct arguments","i.p",""))
	})
}

func TestReferenceOrderedSetAggregateCollations(t *testing.T) {
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE COLLATION os_alias FROM pg_catalog.pg_unicode_fast",0))
		for _,collation:=range []string{"C","POSIX","pg_c_utf8","pg_unicode_fast","os_alias"} {
			for _,direction:=range []string{"ASC","DESC"} {
				want:="A";if direction=="DESC" {want="é"}
				for _,family:=range []string{"mode()","percentile_disc(0)"} {
					q:="SELECT "+family+" WITHIN GROUP(ORDER BY v COLLATE \""+collation+"\" "+direction+") AS v FROM (VALUES('é'::text),('a'),('Z'),('A')) AS a(v)"
					orderedSetBoth(t,p,c,b,orderedSetValue(q,"TEXT",want))
				}
			}
		}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_collation(a TEXT COLLATE \"C\",b TEXT COLLATE \"POSIX\")",0))
		for _,family:=range []string{"mode()","percentile_disc(.5)"} {
			q:="SELECT "+family+" WITHIN GROUP(ORDER BY COALESCE(a,b)) FROM os_collation"
			s:=orderedSetDiagnostic(q,"42P21","collation mismatch between implicit collations \"C\" and \"POSIX\"","b))","You can choose the collation by applying the COLLATE clause to one or both expressions.")
			orderedSetBoth(t,p,c,b,s)
			q="SELECT "+family+" WITHIN GROUP(ORDER BY COALESCE(a,b) COLLATE \"C\") AS v FROM os_collation"
			orderedSetBoth(t,p,c,b,orderedSetValue(q,"TEXT",nil))
		}
	})
}

func TestReferenceOrderedSetAggregateHeldRights(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"os_held_reader"})
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		orderedSetSetup(t,p,c,b)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE ROLE os_held_reader",0))
		if b.PG {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("GRANT USAGE ON SCHEMA public TO os_held_reader",0))}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("GRANT SELECT(x,p) ON os_input TO os_held_reader",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("SET SESSION AUTHORIZATION os_held_reader",0))
		defer func(){if _,err:=c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION");err!=nil {t.Error(err)}}()
		q:="SELECT percentile_cont(p) WITHIN GROUP(ORDER BY x) FILTER(WHERE FALSE) AS v FROM os_input WHERE FALSE GROUP BY p"
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		want:=fixture.Select(q,[]string{"v"},[]string{"FLOAT8"},[][]any{})
		fixture.RunStep(t,p.Ctx,held,b.Recorder,want)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("RESET SESSION AUTHORIZATION",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("REVOKE SELECT(p) ON os_input FROM os_held_reader",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("SET SESSION AUTHORIZATION os_held_reader",0))
		s:=orderedSetDiagnostic(q,"42501","permission denied for table os_input","","");s.ErrorStage="execute"
		fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("RESET SESSION AUTHORIZATION",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("GRANT SELECT(p) ON os_input TO os_held_reader",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("SET SESSION AUTHORIZATION os_held_reader",0))
		fixture.RunStep(t,p.Ctx,held,b.Recorder,want)
	})
}
