package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

type datetimeJustifyPlainStatement struct{ fixture.HeldStatement }

func (s datetimeJustifyPlainStatement) Query(_ string, args ...any) (*sql.Rows, error) {
	return s.Statement.Query(args...)
}
func (s datetimeJustifyPlainStatement) Exec(_ string, args ...any) (sql.Result, error) {
	return s.Statement.Exec(args...)
}
func (s datetimeJustifyPlainStatement) Prepare(q string) (*sql.Stmt, error) {
	return s.Executor.PrepareContext(context.Background(), q)
}

func datetimeJustifyHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, f string) {
	t.Helper()
	q := "SELECT " + f + "($1) AS v"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
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
			s := fixture.Select(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{value}}, arg)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { datetimeJustifyMetadata(t, b.PG, rows) }
			if nonContext {
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, datetimeJustifyPlainStatement{x}, b.Recorder, s)
			} else {
				fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
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
		d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
		if tc.portal {
			d.Context = "unnamed portal parameter $1 = '...'"
		}
		s := fixture.ErrorDiagnostic(q, d, tc.input)
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		recovery := fixture.Select(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{want}}, "1 mon -1 hour")
		recovery.CheckMetadata = func(t *testing.T, rows *sql.Rows) { datetimeJustifyMetadata(t, b.PG, rows) }
		fixture.RunStep(t, p.Ctx, x, b.Recorder, recovery)
	}
}

func TestReferenceDatetimeJustifyParametersMetadata(t *testing.T) {
	datetimeJustifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN j_interval AS interval", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE j_null(id integer,span interval)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO j_null VALUES(1,NULL)", 1))
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
				datetimeJustifyBoth(t, p, b, c, fixture.Select("SELECT "+f+"(span) AS v FROM j_null"+where, []string{"v"}, []string{"INTERVAL"}, rows))
			}
			datetimeJustifyBoth(t, p, b, c, fixture.Select("SELECT "+f+"($1) AS first,"+f+"("+f+"($1)) AS nested", []string{"first", "nested"}, []string{"INTERVAL", "INTERVAL"}, [][]any{{"00:00:00", "00:00:00"}}, "0"))
			datetimeJustifyHeld(t, p, b, c, f)
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT "+f+"($1) AS v", []string{"v"}, []string{"INTERVAL"}, [][]any{{"00:00:00"}}, "0")
				s.NonContext, s.Prepared = true, prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { datetimeJustifyMetadata(t, b.PG, rows) }
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		datetimeJustifyValue(t, p, b, c, "justify_hours(INTERVAL '24 hours' HOUR)", "1 day")
		datetimeJustifyValue(t, p, b, c, "justify_interval('1 mon -1 hour'::j_interval)", "29 days 23:00:00")
	})
}

func TestReferenceDatetimeJustifyStylesLifetime(t *testing.T) {
	datetimeJustifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT justify_interval($1) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ style, want string }{{"postgres", "29 days 23:00:00"}, {"postgres_verbose", "@ 29 days 23 hours"}, {"sql_standard", "29 23:00:00"}, {"iso_8601", "P29DT23H"}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle='"+tc.style+"'", 0))
			s := fixture.Select(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{tc.want}}, "1 mon -1 hour")
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { datetimeJustifyMetadata(t, b.PG, rows) }
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle='postgres'", 0))
		if !b.PG {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
			rows, err := x.QueryContext(ctx, q, "1 mon -1 hour")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET IntervalStyle='iso_8601'", 0))
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, timezoneAdmittedRows{Executor: c, rows: rows}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INTERVAL"}, [][]any{{"29 days 23:00:00"}}, "1 mon -1 hour"))
		}
	})
}
