package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func withStatisticsAlter(t *testing.T, body func(*testing.T, ndistinctScenario)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"sa_actor", "sa_owner", "sa_recipient", "sa_other", "sa_table", "sa_schema"})
			fixture.SchemaReferenceCleanup(t, "sa_destination")
			fixture.SchemaReferenceCleanup(t, "sa_hidden")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(step fixture.Step) { step.Prepared = prepared; fixture.RunStep(t, p.Ctx, conn, b.Recorder, step) }
					body(t, ndistinctScenario{run: run, pg: b.PG, conn: conn, pair: p, prepared: prepared})
				})
			}
		})
	}
}

func statisticsAlterSetup(s ndistinctScenario) {
	for _, q := range []string{"CREATE ROLE sa_recipient", "CREATE SCHEMA sa_destination", "GRANT USAGE,CREATE ON SCHEMA sa_destination TO sa_recipient", "CREATE TABLE sa_input(a INT,b INT)", "CREATE STATISTICS public.sa_before (ndistinct,dependencies,mcv) ON a,b FROM sa_input"} {
		s.run(fixture.Exec(q, 0))
	}
	s.run(fixture.Exec("INSERT INTO sa_input VALUES(1,10),(1,10),(2,20),(2,20)", 4))
	s.run(fixture.Exec("ANALYZE sa_input", 0))
}

func statisticsAlterFacts(t *testing.T, s ndistinctScenario, name string) {
	t.Helper()
	q := "SELECT d.stxdndistinct,d.stxddependencies FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname='" + name + "'"
	s.run(ndistinctRawResult(s.pg, fixture.Select(q, []string{"stxdndistinct", "stxddependencies"}, []string{statisticsDatabaseTypeName(s.pg, "pg_ndistinct"), statisticsDatabaseTypeName(s.pg, "pg_dependencies")}, [][]any{{`{"1, 2": 2}`, dependenciesFull}}), 0, 1))
	mcvAssert(t, s, name, mcvExpected{"1,10", .5, .25}, mcvExpected{"2,20", .5, .25})
}

func statisticsAlterMetadata(name, schema, owner string, target any) fixture.Step {
	return fixture.Select("SELECT s.stxname,n.nspname,r.rolname,s.stxkeys,s.stxkind,s.stxstattarget FROM pg_statistic_ext s JOIN pg_namespace n ON n.oid=s.stxnamespace JOIN pg_roles r ON r.oid=s.stxowner WHERE s.stxname='"+name+"' AND n.nspname='"+schema+"'", []string{"stxname", "nspname", "rolname", "stxkeys", "stxkind", "stxstattarget"}, []string{"NAME", "NAME", "NAME", "INT2VECTOR", "_CHAR", "INT2"}, [][]any{{name, schema, owner, "1 2", "{d,f,m}", target}})
}

func statisticsAlterExpressionSummary(name string) fixture.Step {
	return fixture.Select("SELECT (stxdexpr[1]).stavalues1::text AS values,(stxdexpr[1]).stanumbers1::text AS frequencies"+expressionDataFrom, []string{"values", "frequencies"}, []string{"TEXT", "TEXT"}, [][]any{{"{11,21}", "{0.5,0.5}"}}, name)
}
