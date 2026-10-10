package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

// Extraction does not add domain constraints. Retain the original scenarios as
// exact backend capabilities, separately from plain-domain extraction consumers.
func TestReferenceDatetimeExtractDomainConstraintBoundary(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				for _, tc := range []struct{ name, ddl string }{
					{"dx_source_domain", "CREATE DOMAIN dx_source_domain AS timestamp CHECK(VALUE>=TIMESTAMP '2000-01-01')"},
					{"dx_year", "CREATE DOMAIN dx_year AS numeric CHECK(EXTRACT(year FROM DATE '2000-01-01')=VALUE)"},
				} {
					t.Run(tc.name, func(t *testing.T) {
						if !b.pg {
							d := controlsReferenceDiagnostic("0A000", "domain constraints and defaults are unsupported")
							// The existing domain owner annotates the CHECK keyword.
							d.Position = referencePosition(strings.Index(tc.ddl, "CHECK") + 1)
							s := referenceErrorDiagnostic(tc.ddl, d)
							s.prepared, s.errorStage = prepared, "execute"
							referenceRunStep(t, p.ctx, c, b.recorder, s)
							return
						}
						s := referenceExec(tc.ddl, 0)
						s.prepared = prepared
						referenceRunStep(t, p.ctx, c, b.recorder, s)
						d := controlsReferenceDiagnostic("23514", `value for domain dx_source_domain violates check constraint "dx_source_domain_check"`)
						d.Context, d.Schema, d.DataType, d.Constraint = "unnamed portal parameter $1 = '...'", "public", "dx_source_domain", "dx_source_domain_check"
						failure := referenceErrorDiagnostic("SELECT date_part(NULL::text,$1::dx_source_domain)", d, "1999-01-01")
						failure.selectRows = true
						if tc.name == "dx_year" {
							referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE dx_domain(v dx_year)", 0))
							referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO dx_domain VALUES(2000)", 1))
							d = controlsReferenceDiagnostic("23514", `value for domain dx_year violates check constraint "dx_year_check"`)
							d.Schema, d.DataType, d.Constraint = "public", "dx_year", "dx_year_check"
							failure = referenceErrorDiagnostic("INSERT INTO dx_domain VALUES(2000),(2001)", d)
						}
						failure.prepared, failure.errorStage = prepared, "execute"
						referenceRunStep(t, p.ctx, c, b.recorder, failure)
						if tc.name == "dx_year" {
							referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT v FROM dx_domain", []string{"v"}, []string{"NUMERIC"}, [][]any{{"2000"}}))
							referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("DROP TABLE dx_domain", 0))
						}
						referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("DROP DOMAIN "+tc.name, 0))
					})
				}
			})
		}
	})
}

func TestReferenceDatetimeExtractPartialIndexBoundary(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE dx_stored(s timestamp)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO dx_stored VALUES(TIMESTAMP '2000-02-29'),(TIMESTAMP '2001-01-01'),(TIMESTAMP 'infinity')", 3))
		for _, predicate := range []string{"date_part('month',s)>0", "date_part('month',s) IS NOT NULL"} {
			for _, prepared := range []bool{false, true} {
				t.Run(predicate+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					q := "CREATE INDEX dx_expr ON dx_stored ((EXTRACT(year FROM s))) WHERE " + predicate
					if !b.pg {
						d := controlsReferenceDiagnostic("0A000", "partial indexes are unsupported")
						d.Position = referencePosition(strings.Index(q, "WHERE") + 1)
						s := referenceErrorDiagnostic(q, d)
						s.prepared, s.errorStage = prepared, "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, c, b.recorder, s)
						referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT relname FROM pg_class WHERE relname='dx_expr'", []string{"relname"}, []string{"NAME"}, nil))
					} else {
						s := referenceExec(q, 0)
						s.prepared = prepared
						referenceRunStep(t, p.ctx, c, b.recorder, s)
						// Assert a genuine partial descriptor, not an ordinary index
						// substituted for the excluded native feature.
						referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT i.indpred IS NOT NULL AS partial FROM pg_index i JOIN pg_class c ON i.indexrelid=c.oid WHERE c.relname='dx_expr'", []string{"partial"}, []string{"BOOL"}, [][]any{{true}}))
						referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT EXTRACT(year FROM s) AS year FROM dx_stored WHERE "+predicate+" ORDER BY EXTRACT(year FROM s)", []string{"year"}, []string{"NUMERIC"}, [][]any{{"2000"}, {"2001"}}))
						referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("DROP INDEX dx_expr", 0))
					}
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT count(*) AS n FROM dx_stored", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
				})
			}
		}
	})
}
