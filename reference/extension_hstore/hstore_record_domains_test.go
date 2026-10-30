package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// A stored domain value was already admitted. Reading it introduces no input
// cast/check, so sequence changes isolate the replacement's result validation.
func TestReferenceHstoreRecordResultDomainChecks(t *testing.T) {
	for _, operator := range []bool{false, true} {
		spelling := "function"
		if operator {
			spelling = "operator"
		}
		for _, route := range []string{"direct", "fresh", "held"} {
			t.Run(spelling+"/"+route, func(t *testing.T) {
				hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					hstoreRecordSetup(t, p, b, c)
					for _, q := range []string{
						"CREATE SEQUENCE hs_record_domain_calls",
						"CREATE DOMAIN hs_record_counted_pair AS hs_record_pair CONSTRAINT hs_record_counted_check CHECK(nextval('hs_record_domain_calls'::regclass)>0)",
						"CREATE TABLE hs_record_domain_base(r hs_record_counted_pair)",
					} {
						hstoreRun(t, p, b, c, fixture.Exec(q, 0))
					}
					hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_record_domain_base VALUES ('(7,keep)')", 1))
					q := "SELECT " + hstoreRecordCall(operator, "r", "$1::hstore") + "::text AS v FROM hs_record_domain_base"
					var executor fixture.Executor = c
					if route == "held" {
						statement, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer statement.Close()
						executor = fixture.HeldStatement{Executor: c, Statement: statement, SQL: q}
					}
					for _, tc := range []struct {
						name   string
						source any
						delta  int64
					}{
						{"null", nil, 0}, {"empty", "", 0}, {"unmatched_nonempty", "unknown=>oops", 1},
					} {
						t.Run(tc.name, func(t *testing.T) {
							var before int64
							if err := c.QueryRowContext(p.Ctx, "SELECT currval('hs_record_domain_calls'::regclass)").Scan(&before); err != nil {
								t.Fatal(err)
							}
							step := hstoreRecordText(q, "(7,keep)", tc.source)
							if route == "fresh" {
								step = fixture.Prepared(step)
							}
							hstoreRun(t, p, b, executor, step)
							hstoreRun(t, p, b, c, fixture.Select("SELECT currval('hs_record_domain_calls'::regclass) AS v", []string{"v"}, []string{"INT8"}, [][]any{{before + tc.delta}}))
							hstoreRun(t, p, b, c, hstoreRecordText("SELECT r::text AS v FROM hs_record_domain_base", "(7,keep)"))
						})
					}
				})
			})
		}
	}
}
