package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func fmtDatetimeArgs(args []any) string { return fmt.Sprint(args) }

func datetimeArithmeticRuntimeError(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q, code, message string, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(code, message), args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	datetimeArithmeticBoth(t, p, b, x, s)
}

func TestReferenceDatetimeArithmeticDiagnostics(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ left, op, right, signature string }{
			{"DATE '2000-01-01'", "*", "2", "date * integer"},
			{"DATE '2000-01-01'", "+", "DATE '2000-01-01'", "date + date"},
			{"TIMESTAMP '2000-01-01'", "+", "TIMESTAMP '2000-01-01'", "timestamp without time zone + timestamp without time zone"},
			{"TIMETZ '01:00+00'", "-", "TIMETZ '00:00+00'", "time with time zone - time with time zone"},
			{"NULL::TIMETZ", "-", "NULL::TIMETZ", "time with time zone - time with time zone"},
			{"1::DOUBLE PRECISION", "/", "INTERVAL '1 hour'", "double precision / interval"},
			{"INTERVAL '1 hour'", "-", "DATE '2000-01-01'", "interval - date"},
			{"DATE '2000-01-01'", "+", "1::BIGINT", "date + bigint"},
		} {
			for _, prefix := range []string{"SELECT ", "/*é*/ SELECT "} {
				q := prefix + tc.left + " " + tc.op + " " + tc.right
				want := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+tc.signature)
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				want.Position = fixture.Position(utf8.RuneCountInString(prefix+tc.left+" ") + 1)
				t.Run(q, func(t *testing.T) {
					s := fixture.ErrorDiagnostic(q, want)
					s.SelectRows = true
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					s.ErrorStage = "prepare"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				})
			}
		}
		for _, tc := range []struct {
			q, message string
			args       []any
		}{
			{"SELECT $1::DATE+1", "date out of range", []any{"5874897-12-31"}},
			{"SELECT $1::DATE-1", "date out of range", []any{"4714-11-24 BC"}},
			{"SELECT $1::DATE+INTERVAL '0'", "date out of range for timestamp", []any{"5874897-12-31"}},
			{"SELECT $1::DATE+TIME '24:00'", "date out of range for timestamp", []any{"5874897-12-31"}},
			{"SELECT $1::TIMESTAMP+INTERVAL '1 microsecond'", "timestamp out of range", []any{"294276-12-31 23:59:59.999999"}},
			{"SELECT $1::TIMESTAMP-INTERVAL '1 microsecond'", "timestamp out of range", []any{"4714-11-24 00:00 BC"}},
			{"SELECT $1::TIMESTAMPTZ+INTERVAL '1 microsecond'", "timestamp out of range", []any{"294276-12-31 23:59:59.999999+00"}},
			{"SELECT $1::DATE+TIME '24:00'", "timestamp out of range", []any{"294276-12-31"}},
			{"SELECT $1::TIMESTAMP+INTERVAL '1 month -31 days'", "timestamp out of range", []any{"294276-12-01"}},
			{"SELECT $1::TIMESTAMP-$2::TIMESTAMP", "interval out of range", []any{"294276-12-31 23:59:59.999999", "4714-11-24 00:00 BC"}},
		} {
			t.Run(tc.q+fmtDatetimeArgs(tc.args), func(t *testing.T) { datetimeArithmeticRuntimeError(t, p, b, c, tc.q, "22008", tc.message, tc.args...) })
		}
	})
}

func TestReferenceDatetimeArithmeticLateFailureAtomicity(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE dt_atomic(id INTEGER,v INTERVAL,d DATE)", 0))
		run(fixture.Exec("INSERT INTO dt_atomic VALUES(1,'1 hour','2024-01-01'),(2,'2 hours','2024-01-02')", 2))
		sentinel := fixture.Select("SELECT id,v,d::TEXT AS d FROM dt_atomic ORDER BY id", []string{"id", "v", "d"}, []string{"INT4", "INTERVAL", "TEXT"}, [][]any{{int64(1), "01:00:00", "2024-01-01"}, {int64(2), "02:00:00", "2024-01-02"}})
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q, code, message string
				args             []any
			}{
				{"INSERT INTO dt_atomic VALUES(3,INTERVAL '1 hour'/1,DATE '2024-01-01'+1),(4,INTERVAL '1 hour'/$1,DATE '2024-01-01')", "22012", "division by zero", []any{0}},
				{"INSERT INTO dt_atomic VALUES(3,INTERVAL '1 hour',DATE '2024-01-01'+1),(4,INTERVAL '1 hour',$1::DATE+1)", "22008", "date out of range", []any{"5874897-12-31"}},
				{"UPDATE dt_atomic SET v=v/(id-$1)", "22012", "division by zero", []any{2}},
			} {
				t.Run(tc.q+fmt.Sprint(prepared), func(t *testing.T) {
					s := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), tc.args...)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, sentinel)
				})
			}
		}
		// Bound operands ensure this is a row-demand failure, not a constant-folding probe.
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT v/$1::DOUBLE PRECISION FROM dt_atomic ORDER BY id", fixture.ControlsReferenceDiagnostic("22012", "division by zero"), 0)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			run(sentinel)
		}
	})
}
