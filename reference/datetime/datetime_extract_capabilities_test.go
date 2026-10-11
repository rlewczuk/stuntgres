package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Extraction does not add domain constraints. Retain the original scenarios as
// exact backend capabilities, separately from plain-domain extraction consumers.
func TestReferenceDatetimeExtractDomainConstraintBoundary(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				for _, tc := range []struct{ name, ddl string }{
					{"dx_source_domain", "CREATE DOMAIN dx_source_domain AS timestamp CHECK(VALUE>=TIMESTAMP '2000-01-01')"},
					{"dx_year", "CREATE DOMAIN dx_year AS numeric CHECK(EXTRACT(year FROM DATE '2000-01-01')=VALUE)"},
				} {
					t.Run(tc.name, func(t *testing.T) {
						if !b.PG {
							d := fixture.ControlsReferenceDiagnostic("0A000", "domain constraints and defaults are unsupported")
							// The existing domain owner annotates the CHECK keyword.
							d.Position = fixture.Position(strings.Index(tc.ddl, "CHECK") + 1)
							s := fixture.ErrorDiagnostic(tc.ddl, d)
							s.Prepared, s.ErrorStage = prepared, "execute"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
							return
						}
						s := fixture.Exec(tc.ddl, 0)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						d := fixture.ControlsReferenceDiagnostic("23514", `value for domain dx_source_domain violates check constraint "dx_source_domain_check"`)
						d.Context, d.Schema, d.DataType, d.Constraint = "unnamed portal parameter $1 = '...'", "public", "dx_source_domain", "dx_source_domain_check"
						failure := fixture.ErrorDiagnostic("SELECT date_part(NULL::text,$1::dx_source_domain)", d, "1999-01-01")
						failure.SelectRows = true
						if tc.name == "dx_year" {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE dx_domain(v dx_year)", 0))
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dx_domain VALUES(2000)", 1))
							d = fixture.ControlsReferenceDiagnostic("23514", `value for domain dx_year violates check constraint "dx_year_check"`)
							d.Schema, d.DataType, d.Constraint = "public", "dx_year", "dx_year_check"
							failure = fixture.ErrorDiagnostic("INSERT INTO dx_domain VALUES(2000),(2001)", d)
						}
						failure.Prepared, failure.ErrorStage = prepared, "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, failure)
						if tc.name == "dx_year" {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM dx_domain", []string{"v"}, []string{"NUMERIC"}, [][]any{{"2000"}}))
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE dx_domain", 0))
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP DOMAIN "+tc.name, 0))
					})
				}
			})
		}
	})
}

func TestReferenceDatetimeExtractPartialIndex(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE dx_stored(s timestamp)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dx_stored VALUES(TIMESTAMP '2000-02-29'),(TIMESTAMP '2001-01-01'),(TIMESTAMP 'infinity')", 3))
		for _, predicate := range []string{"date_part('month',s)>0", "date_part('month',s) IS NOT NULL"} {
			for _, prepared := range []bool{false, true} {
				t.Run(predicate+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					q := "CREATE INDEX dx_expr ON dx_stored ((EXTRACT(year FROM s))) WHERE " + predicate
					s := fixture.Exec(q, 0)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					if b.PG {
						// The broad pg_index SQL source remains PostgreSQL-only.
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT i.indpred IS NOT NULL AS partial FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid WHERE c.relname='dx_expr'", []string{"partial"}, []string{"BOOL"}, [][]any{{true}}))
					}
					var definition string
					if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_indexdef('dx_expr'::regclass)").Scan(&definition); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(definition, " WHERE ") {
						t.Fatalf("missing partial predicate in index definition: %s", definition)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT EXTRACT(year FROM s) AS year FROM dx_stored WHERE "+predicate+" ORDER BY EXTRACT(year FROM s)", []string{"year"}, []string{"NUMERIC"}, [][]any{{"2000"}, {"2001"}}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP INDEX dx_expr", 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM dx_stored", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
				})
			}
		}
	})
}
