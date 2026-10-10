package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDatetimeExtractStrictSourceBoundaries(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, source := range []string{"DATE '2000-01-01'", "TIME '12:00'", "TIMETZ '12:00+00'", "TIMESTAMP '2000-01-01'", "TIMESTAMPTZ '2000-01-01+00'", "INTERVAL '1 day'", "DATE '400000-01-01'"} {
			datetimeExtractBoth(t, p, b, c, referenceSelect("SELECT date_part(NULL::text,"+source+")", []string{"date_part"}, []string{"FLOAT8"}, [][]any{{nil}}))
		}
		for _, tc := range []struct{ source, typ string }{{"DATE 'infinity'", "date"}, {"TIMESTAMP '-infinity'", "timestamp without time zone"}, {"TIMESTAMPTZ 'infinity'", "timestamp with time zone"}, {"INTERVAL '-infinity'", "interval"}} {
			for _, api := range []string{"extract", "date_part"} {
				typ := tc.typ
				if api == "date_part" && typ == "date" {
					typ = "timestamp without time zone"
				}
				d := datetimeExtractFieldDiagnostic("22023", "bogus", typ)
				if api == "date_part" && tc.typ == "date" {
					d = datetimeExtractDateWrapperDiagnostic(d)
				}
				datetimeExtractFailure(t, p, b, c, "SELECT "+datetimeExtractSQL(api, "bogus", tc.source), d, "execute")
			}
		}
	})
}

func TestReferenceDatetimeExtractLateSourceAssignmentConstraint(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE dx_late(id integer,v integer,CONSTRAINT dx_late_positive CHECK(v>0))", 0))
		run(referenceExec("INSERT INTO dx_late VALUES(1,2000)", 1))
		want := referenceSelect("SELECT id,v FROM dx_late ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2000)}})
		constraint := controlsReferenceDiagnostic("23514", `new row for relation "dx_late" violates check constraint "dx_late_positive"`)
		constraint.Detail, constraint.Schema, constraint.Table, constraint.Constraint = "Failing row contains (3, -1).", "public", "dx_late", "dx_late_positive"
		for _, tc := range []struct {
			query string
			d     referenceDiagnostic
			args  []any
		}{
			{"INSERT INTO dx_late VALUES(2,date_part('year',$1::date)),(3,date_part('year',$2::date))", datetimeExtractDateWrapperDiagnostic(controlsReferenceDiagnostic("22008", "date out of range for timestamp")), []any{"2001-01-01", "400000-01-01"}},
			{"INSERT INTO dx_late VALUES(2,EXTRACT(year FROM DATE '2001-01-01')),(3,EXTRACT(epoch FROM INTERVAL '1000000000 days'))", controlsReferenceDiagnostic("22003", "integer out of range"), nil},
			{"INSERT INTO dx_late VALUES(2,EXTRACT(year FROM DATE '2001-01-01')),(3,EXTRACT(year FROM DATE '0001-01-01 BC'))", constraint, nil},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					s := referenceErrorDiagnostic(tc.query, tc.d, tc.args...)
					s.prepared, s.errorStage = prepared, "execute"
					referenceRunStep(t, p.ctx, c, b.recorder, s)
					referenceRunStep(t, p.ctx, c, b.recorder, want)
				})
			}
		}
	})
}
