package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

type datetimeJustifyPlainStatement struct{ referenceHeldStatement }

func (s datetimeJustifyPlainStatement) Query(_ string, args ...any) (*sql.Rows, error) {
	return s.statement.Query(args...)
}
func (s datetimeJustifyPlainStatement) Exec(_ string, args ...any) (sql.Result, error) {
	return s.statement.Exec(args...)
}
func (s datetimeJustifyPlainStatement) Prepare(q string) (*sql.Stmt, error) {
	return s.referenceExecutor.PrepareContext(context.Background(), q)
}

func datetimeJustifyHeld(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, f string) {
	t.Helper()
	q := "SELECT " + f + "($1) AS v"
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
	want := "1 mon -01:00:00"
	overflow := "2147483647 months 30 days"
	if f == "justify_hours" {
		want, overflow = "1 mon -01:00:00", "2147483647 days 24 hours"
	}
	if f == "justify_interval" {
		want = "29 days 23:00:00"
	}
	for _, nonContext := range []bool{false, true} {
		for _, arg := range []any{"1 mon -1 hour", []byte("1 mon -1 hour"), nil} {
			value := any(want)
			if arg == nil {
				value = nil
			}
			s := referenceSelect(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{value}}, arg)
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) { datetimeJustifyMetadata(t, b.pg, rows) }
			if nonContext {
				s.nonContext = true
				referenceRunStep(t, p.ctx, datetimeJustifyPlainStatement{x}, b.recorder, s)
			} else {
				referenceRunStep(t, p.ctx, x, b.recorder, s)
			}
		}
	}
	for _, tc := range []struct {
		input, code, message string
		portal               bool
	}{
		{"invalid", "22007", `invalid input syntax for type interval: "invalid"`, true},
		{"2147483648 days", "22015", `interval field value out of range: "2147483648 days"`, true},
		{overflow, "22008", "interval out of range", false},
	} {
		d := controlsReferenceDiagnostic(tc.code, tc.message)
		if tc.portal {
			d.Context = "unnamed portal parameter $1 = '...'"
		}
		s := referenceErrorDiagnostic(q, d, tc.input)
		s.selectRows, s.errorStage = true, "execute"
		referenceRunStep(t, p.ctx, x, b.recorder, s)
		recovery := referenceSelect(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{want}}, "1 mon -1 hour")
		recovery.checkMetadata = func(t *testing.T, rows *sql.Rows) { datetimeJustifyMetadata(t, b.pg, rows) }
		referenceRunStep(t, p.ctx, x, b.recorder, recovery)
	}
}

func TestReferenceDatetimeJustifyParametersMetadata(t *testing.T) {
	datetimeJustifyScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE DOMAIN j_interval AS interval", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE j_null(id integer,span interval)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO j_null VALUES(1,NULL)", 1))
		for _, f := range []string{"justify_days", "justify_hours", "justify_interval"} {
			for _, arg := range []string{"NULL", "NULL::interval", "'0'::j_interval"} {
				want := any(nil)
				if arg == "'0'::j_interval" {
					want = "00:00:00"
				}
				datetimeJustifyValue(t, p, b, c, f+"("+arg+")", want)
			}
			for _, where := range []string{"", " WHERE false"} {
				rows := [][]any{{nil}}
				if where != "" {
					rows = nil
				}
				datetimeJustifyBoth(t, p, b, c, referenceSelect("SELECT "+f+"(span) AS v FROM j_null"+where, []string{"v"}, []string{"INTERVAL"}, rows))
			}
			datetimeJustifyBoth(t, p, b, c, referenceSelect("SELECT "+f+"($1) AS first,"+f+"("+f+"($1)) AS nested", []string{"first", "nested"}, []string{"INTERVAL", "INTERVAL"}, [][]any{{"00:00:00", "00:00:00"}}, "0"))
			datetimeJustifyHeld(t, p, b, c, f)
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT "+f+"($1) AS v", []string{"v"}, []string{"INTERVAL"}, [][]any{{"00:00:00"}}, "0")
				s.nonContext, s.prepared = true, prepared
				s.checkMetadata = func(t *testing.T, rows *sql.Rows) { datetimeJustifyMetadata(t, b.pg, rows) }
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
		}
		datetimeJustifyValue(t, p, b, c, "justify_hours(INTERVAL '24 hours' HOUR)", "1 day")
		datetimeJustifyValue(t, p, b, c, "justify_interval('1 mon -1 hour'::j_interval)", "29 days 23:00:00")
	})
}

func TestReferenceDatetimeJustifyStylesLifetime(t *testing.T) {
	datetimeJustifyScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		q := "SELECT justify_interval($1) AS v"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct{ style, want string }{{"postgres", "29 days 23:00:00"}, {"postgres_verbose", "@ 29 days 23 hours"}, {"sql_standard", "29 23:00:00"}, {"iso_8601", "P29DT23H"}} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET IntervalStyle='"+tc.style+"'", 0))
			s := referenceSelect(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.want}}, "1 mon -1 hour")
			s.checkMetadata = func(t *testing.T, rows *sql.Rows) { datetimeJustifyMetadata(t, b.pg, rows) }
			referenceRunStep(t, p.ctx, x, b.recorder, s)
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET IntervalStyle='postgres'", 0))
		if !b.pg {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
			rows, err := x.QueryContext(ctx, q, "1 mon -1 hour")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET IntervalStyle='iso_8601'", 0))
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, timezoneAdmittedRows{referenceExecutor: c, rows: rows}, b.recorder, referenceSelect(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{"29 days 23:00:00"}}, "1 mon -1 hour"))
		}
	})
}
