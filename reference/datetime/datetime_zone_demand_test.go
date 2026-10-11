package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Concrete independent contracts confirmed by Main's pinned PostgreSQL report
// tests-2.md/artifact19. SQLSTATE, English fields and phase are not parity goldens.
func TestReferenceDatetimeZoneObservedDiagnosticsAndResolution(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			query        string
			want         fixture.Diagnostic
			prepareStage string
			args         []any
		}{
			{"SELECT timezone(NULL,INTERVAL '0') AS v FROM tz_witness", fixture.Diagnostic{Code: "42883", Message: "function timezone(unknown, interval) does not exist", Hint: datetimeZoneCastHint, Position: "8"}, "prepare", nil},
			{"SELECT timezone(NULL,TIMESTAMP 'nonsense') AS v FROM tz_witness", fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type timestamp: "nonsense"`, Position: "32"}, "prepare", nil},
			{"SELECT date_add(NULL::TIMESTAMPTZ,INTERVAL 'nonsense','UTC') AS v FROM tz_witness", fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type interval: "nonsense"`, Position: "44"}, "prepare", nil},
			{"SELECT /* é */ 1 AT LOCAL AS v FROM tz_witness", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.timezone(integer) does not exist", Hint: datetimeZoneCastHint}, "prepare", nil},
			{"SELECT /* é */ TIMESTAMP '2004-01-01' AT TIME ZONE TRUE AS v FROM tz_witness", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.timezone(boolean, timestamp without time zone) does not exist", Hint: datetimeZoneCastHint, Position: "39"}, "prepare", nil},
			{"SELECT timezone('bad\"zone',TIMESTAMP '2004-01-01') AS v FROM tz_witness", fixture.Diagnostic{Code: "22023", Message: `time zone "bad"zone" not recognized`}, "execute", nil},
			{"SELECT timezone(E'bad\\\\zone',TIMESTAMP '2004-01-01') AS v FROM tz_witness", fixture.Diagnostic{Code: "22023", Message: `time zone "bad\zone" not recognized`}, "execute", nil},
			{"SELECT timezone($1,$1::TIMESTAMP) AS v FROM tz_witness", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "timestamp without time zone versus text", Position: "17"}, "prepare", []any{"UTC"}},
			{"SELECT date_add($1::TIMESTAMPTZ,$1::INTERVAL) AS v FROM tz_witness", fixture.Diagnostic{Code: "42846", Message: "cannot cast type timestamp with time zone to interval", Position: "35"}, "prepare", []any{"2004-01-01+00"}},
			{"SELECT date_add($1::TIMESTAMPTZ,INTERVAL '0','UTC') AS v FROM tz_witness", fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type timestamp with time zone: "nonsense"`, Context: "unnamed portal parameter $1 = '...'"}, "execute", []any{"nonsense"}},
			{"SELECT date_subtract(TIMESTAMPTZ 'infinity',INTERVAL '-2562047788:00:54.775808','UTC') AS v FROM tz_witness", fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type interval: "-2562047788:00:54.775808"`, Position: "54"}, "prepare", nil},
			{"SELECT date_subtract(TIMESTAMPTZ 'infinity',INTERVAL '-2147483648 months','UTC') AS v FROM tz_witness", fixture.Diagnostic{Code: "22008", Message: "interval out of range"}, "execute", nil},
			{"SELECT date_subtract(TIMESTAMPTZ 'infinity',INTERVAL '-2147483648 days','UTC') AS v FROM tz_witness", fixture.Diagnostic{Code: "22008", Message: "interval out of range"}, "execute", nil},
			{"SELECT date_add(TIMESTAMPTZ 'infinity',INTERVAL '0','No/Such_Zone') AS v FROM tz_witness", fixture.Diagnostic{Code: "22023", Message: `time zone "No/Such_Zone" not recognized`}, "execute", nil},
			{"SELECT date_subtract(TIMESTAMPTZ 'infinity',INTERVAL 'infinity','UTC') AS v FROM tz_witness", fixture.Diagnostic{Code: "22008", Message: "timestamp out of range"}, "execute", nil},
		} {
			t.Run(tc.query, func(t *testing.T) {
				s := fixture.ImplicitError(tc.query, tc.want, tc.args...)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s.Prepared, s.ErrorStage = true, tc.prepareStage
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		datetimeArithmeticBoth(t, p, b, c, fixture.Select("SELECT timezone(NULL,NULL) AS v FROM tz_witness", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{nil}}))
		datetimeArithmeticBoth(t, p, b, c, fixture.Select("SELECT timezone($1,$2) AS v FROM tz_witness", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2004, 1, 1, 0, 0, 0, 0, time.UTC)}}, "UTC", "2004-01-01+00"))
		datetimeArithmeticBoth(t, p, b, c, fixture.Select("SELECT CASE WHEN TRUE THEN TIMESTAMP '2004-01-01' ELSE timezone('No/Such_Zone',TIMESTAMPTZ '2004-01-01+00') END AS v FROM tz_witness", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(2004, 1, 1, 0, 0, 0, 0, time.UTC)}}))
	})
}

func TestReferenceDatetimeZoneStrictChildDemand(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SEQUENCE dz_child_order", 0))
		run(fixture.Exec("ALTER TABLE tz_witness ADD COLUMN zone TEXT", 0))
		for _, tc := range []struct {
			expr   string
			called bool
		}{
			{"timezone(NULL::TEXT,TIMESTAMP '2004-01-01'+INTERVAL '1 second'*nextval('dz_child_order')::DOUBLE PRECISION)", false},
			{"timezone(zone,TIMESTAMP '2004-01-01'+INTERVAL '1 second'*nextval('dz_child_order')::DOUBLE PRECISION)", true},
			{"date_add(NULL::TIMESTAMPTZ,INTERVAL '0',nextval('dz_child_order')::TEXT)", false},
			{"date_add(z,INTERVAL '0',nextval('dz_child_order')::TEXT)", true},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER SEQUENCE dz_child_order RESTART WITH 1", 0))
					s := fixture.Select("SELECT "+tc.expr+" AS v FROM tz_witness WHERE id=1", []string{"v"}, []string{"TIMESTAMPTZ"}, [][]any{{nil}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT last_value,is_called FROM dz_child_order", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), tc.called}}))
				}
			})
		}
		// The constant interval failure happens during Execute planning for both
		// written forms, rather than entering the selected zone function body.
		for _, query := range []string{
			"SELECT timezone(CASE WHEN nextval('dz_child_order')>0 THEN 'No/Such_Zone' ELSE 'UTC' END,TIMESTAMP '2004-01-01'+INTERVAL '1 second'/0) AS v FROM tz_witness",
			"SELECT (TIMESTAMP '2004-01-01'+INTERVAL '1 second'/0) AT TIME ZONE CASE WHEN nextval('dz_child_order')>0 THEN 'No/Such_Zone' ELSE 'UTC' END AS v FROM tz_witness",
		} {
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			s.SelectRows, s.ErrorStage = true, "execute"
			datetimeArithmeticBoth(t, p, b, c, s)
		}
	})
}
