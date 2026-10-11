package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func datetimeBinHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ q, typ, inputName string }{
		{"SELECT date_bin($1,$2,$3) AS result", "TIMESTAMPTZ", "timestamp with time zone"},
		{"SELECT date_bin($1::interval,$2::timestamp,$3::timestamp) AS result", "TIMESTAMP", "timestamp"},
		{"SELECT date_bin($1::interval,$2::timestamptz,$3::timestamptz) AS result", "TIMESTAMPTZ", "timestamp with time zone"},
	} {
		stmt, err := c.PrepareContext(p.Ctx, tc.q)
		if err != nil {
			t.Fatal(err)
		}
		x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
		good := fixture.Select(tc.q, []string{"result"}, []string{tc.typ}, [][]any{{datetimeTruncInstant("2000-01-02T00:02:30Z")}}, "15 minutes", "2000-01-02 00:07", "2000-01-01 00:02:30")
		fixture.RunStep(t, p.Ctx, x, b.Recorder, good)
		for _, args := range [][]any{{nil, "2000-01-02", "2000-01-01"}, {"0", nil, "infinity"}, {"1 mon", "2000-01-02", nil}, {nil, nil, nil}} {
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(tc.q, []string{"result"}, []string{tc.typ}, [][]any{{nil}}, args...))
		}
		for _, bad := range []struct {
			args                  []any
			code, message, portal string
		}{
			{[]any{"0", "2000-01-02", "2000-01-01"}, "22008", "stride must be greater than zero", ""},
			{[]any{nil, "not-a-timestamp", "2000-01-01"}, "22007", "invalid input syntax for type " + tc.inputName + ": \"not-a-timestamp\"", "unnamed portal parameter $2 = '...'"},
			{[]any{"not-an-interval", nil, "2000-01-01"}, "22007", "invalid input syntax for type interval: \"not-an-interval\"", "unnamed portal parameter $1 = '...'"},
		} {
			d := fixture.ControlsReferenceDiagnostic(bad.code, bad.message)
			d.Context = bad.portal
			s := fixture.ErrorDiagnostic(tc.q, d, bad.args...)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, x, b.Recorder, good)
		}
		for _, args := range [][]any{
			{[]byte("15 minutes"), []byte("2000-01-02 00:07"), []byte("2000-01-01 00:02:30")},
			{"15 minutes", datetimeTruncInstant("2000-01-02T00:07:00Z"), datetimeTruncInstant("2000-01-01T00:02:30Z")},
		} {
			s := good
			s.Args = args
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		}
		stmt.Close()
	}
	datetimeTruncValue(t, p, b, c, "date_bin($1::interval,$2::timestamp,$2::timestamp)", "TIMESTAMP", "2000-01-02T00:07:00Z", "15 minutes", "2000-01-02 00:07")
	datetimeTruncValue(t, p, b, c, "date_bin($1,$2,$2)", "TIMESTAMPTZ", "2000-01-02T00:07:00Z", "15 minutes", "2000-01-02 00:07")
	for _, tc := range []struct {
		q, message, portal string
		args               []any
	}{
		{"SELECT date_bin($1::interval,$2::timestamp,$3::timestamp)", `invalid input syntax for type timestamp: "not-a-timestamp"`, "unnamed portal parameter $2 = '...'", []any{nil, "not-a-timestamp", "2000-01-01"}},
		{"SELECT date_bin($1::interval,$2::timestamp,$3::timestamp)", `invalid input syntax for type interval: "not-an-interval"`, "unnamed portal parameter $1 = '...'", []any{"not-an-interval", nil, "2000-01-01"}},
	} {
		d := fixture.ControlsReferenceDiagnostic("22007", tc.message)
		d.Context = tc.portal
		datetimeExtractFailure(t, p, b, c, tc.q, d, "execute", tc.args...)
	}
	for _, typ := range []string{"timestamp", "timestamptz"} {
		resultType := "TIMESTAMP"
		if typ == "timestamptz" {
			resultType = "TIMESTAMPTZ"
		}
		for _, expr := range []string{
			"date_bin(NULL::interval,'2000-01-02'::" + typ + ",'2000-01-01'::" + typ + ")",
			"date_bin('0',NULL::" + typ + ",'infinity'::" + typ + ")",
			"date_bin('1 mon','2000-01-02'::" + typ + ",NULL::" + typ + ")",
			"date_bin(NULL::interval,NULL::" + typ + ",NULL::" + typ + ")",
		} {
			datetimeTruncValue(t, p, b, c, expr, resultType, nil)
		}
	}
	datetimeTruncValue(t, p, b, c, "date_bin(NULL,NULL,NULL)", "TIMESTAMPTZ", nil)
}

func datetimeBinPublication(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE db_batch(id integer,stride interval,s timestamp,o timestamp)", 0))
	run(fixture.Exec("INSERT INTO db_batch VALUES(1,INTERVAL '15 minutes',TIMESTAMP '2000-01-01 00:07',TIMESTAMP '2000-01-01'),(2,INTERVAL '0',TIMESTAMP '2000-01-01 00:07',TIMESTAMP '2000-01-01')", 2))
	run(fixture.Exec("CREATE TABLE db_target(id integer,v timestamp)", 0))
	run(fixture.Exec("INSERT INTO db_target VALUES(9,TIMESTAMP '1999-01-01')", 1))
	want := fixture.Select("SELECT id,v::text FROM db_target ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "1999-01-01 00:00:00"}})
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("INSERT INTO db_target SELECT id,date_bin(stride,s,o) FROM db_batch ORDER BY id", fixture.ControlsReferenceDiagnostic("22008", "stride must be greater than zero"))
		s.Prepared, s.ErrorStage = prepared, "execute"
		run(s)
		run(want)
	}
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("SAVEPOINT db_save", 0))
	s := fixture.ErrorDiagnostic("INSERT INTO db_target SELECT id,date_bin(stride,s,o) FROM db_batch ORDER BY id", fixture.ControlsReferenceDiagnostic("22008", "stride must be greater than zero"))
	s.ErrorStage = "execute"
	run(s)
	run(fixture.ErrorDiagnostic("SELECT count(*) FROM db_target", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
	run(fixture.Exec("ROLLBACK TO SAVEPOINT db_save", 0))
	run(fixture.Exec("INSERT INTO db_target SELECT id,date_bin(stride,s,o) FROM db_batch WHERE id=1", 1))
	run(fixture.Exec("COMMIT", 0))
	run(fixture.Select("SELECT id,v::text FROM db_target ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "2000-01-01 00:00:00"}, {int64(9), "1999-01-01 00:00:00"}}))
	if b.PG {
		for _, prepared := range []bool{false, true} {
			fixture.SessionCancel(t, p.Ctx, c, prepared)
		}
	} else {
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, prepared := range []bool{false, true} {
			q := "INSERT INTO db_target VALUES(4,date_bin('1 day',TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-01'))"
			var err error
			if prepared {
				stmt, e := c.PrepareContext(p.Ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				_, err = stmt.ExecContext(ctx)
				stmt.Close()
			} else {
				_, err = c.ExecContext(ctx, q)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled bin write: %v", err)
			}
		}
	}
	run(fixture.Select("SELECT count(*) FROM db_target", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
}

func TestReferenceDatetimeBinStrictnessParameters(t *testing.T) {
	t.Run("held-and-null", func(t *testing.T) { datetimeBinScenario(t, datetimeBinHeld) })
	t.Run("publication", func(t *testing.T) { datetimeBinScenario(t, datetimeBinPublication) })
}
