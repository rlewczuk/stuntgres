package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckSQLExecute(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE DOMAIN domain_s.sql_positive AS integer CONSTRAINT sql_positive_check CHECK(VALUE>0)", 0))
		run(fixture.Exec("PREPARE domain_check_sql(domain_s.sql_positive) AS SELECT $1 AS v", 0))
		defer c.ExecContext(p.Ctx, "DEALLOCATE domain_check_sql")
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				actual string
				want   any
			}{{"1", int64(1)}, {"2", int64(2)}, {"NULL", nil}} {
				s := fixture.Select("EXECUTE domain_check_sql("+tc.actual+")", []string{"v"}, []string{"INT4"}, [][]any{{tc.want}})
				s.Prepared = prepared
				run(s)
			}
			run(domainCheckFailure("EXECUTE domain_check_sql(0)", "sql_positive", "sql_positive_check", true, prepared))
			s := fixture.Select("EXECUTE domain_check_sql(3)", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}})
			s.Prepared = prepared
			run(s)
		}
	})
}
