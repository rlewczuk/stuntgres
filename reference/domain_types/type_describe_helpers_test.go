package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations start with the PostgreSQL manual and REL_18_6 source, not native output.
func typeDescribeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	for _, schema := range []string{"td", "td_hidden"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RegtypeRunStep(t, p.Ctx, c, b.Recorder, s) }
			body(t, p, b, c, run)
		})
	}
}

func typeDescribeBoth(run func(fixture.Step), s fixture.Step) {
	run(s)
	run(fixture.Prepared(s))
}

func typeDescribeValue(run func(fixture.Step), expression, typ string, value any) {
	typeDescribeBoth(run, fixture.Select("SELECT "+expression+" AS v", []string{"v"}, []string{typ}, [][]any{{value}}))
}

func typeDescribeFailure(run func(fixture.Step), query string, d fixture.Diagnostic, analysis bool, args ...any) {
	for _, prepared := range []bool{false, true} {
		for _, queryRows := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(query, d, args...)
			s.SelectRows, s.Prepared, s.ErrorStage = queryRows, prepared, "execute"
			if prepared && analysis {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
	}
}

func typeDescribeGraph(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE SCHEMA td",
		"CREATE DOMAIN td.inner_domain AS numeric(5,2)",
		"CREATE DOMAIN td.outer_domain AS td.inner_domain",
		"CREATE DOMAIN td.mytext AS text",
		"CREATE DOMAIN td.names AS name[]",
	} {
		run(fixture.Exec(q, 0))
	}
}

func typeDescribeGraphProof(run func(fixture.Step)) {
	typeDescribeValue(run, "pg_basetype('td.outer_domain'::regtype)::oid=1700::oid", "BOOL", true)
	typeDescribeValue(run, "pg_typeof(NULL::td.outer_domain)::oid='td.outer_domain'::regtype::oid", "BOOL", true)
	typeDescribeValue(run, "pg_basetype('td.mytext'::regtype)", "REGTYPE", "text")
	typeDescribeValue(run, "format_type('td.outer_domain'::regtype::oid,NULL)", "TEXT", "td.outer_domain")
}
