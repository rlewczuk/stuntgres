package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
	"unicode/utf8"
)

func fmtDatetimeArgs(args []any) string { return fmt.Sprint(args) }

func datetimeArithmeticRuntimeError(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q, code, message string, args ...any) {
	t.Helper()
	s := referenceErrorDiagnostic(q, controlsReferenceDiagnostic(code, message), args...)
	s.selectRows = true
	s.errorStage = "execute"
	datetimeArithmeticBoth(t, p, b, x, s)
}

func TestReferenceDatetimeArithmeticDiagnostics(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
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
				want := controlsReferenceDiagnostic("42883", "operator does not exist: "+tc.signature)
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				want.Position = referencePosition(utf8.RuneCountInString(prefix+tc.left+" ") + 1)
				t.Run(q, func(t *testing.T) {
					s := referenceErrorDiagnostic(q, want)
					s.selectRows = true
					referenceRunStep(t, p.ctx, c, b.recorder, s)
					s.errorStage = "prepare"
					referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
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
	datetimeArithmeticScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE dt_atomic(id INTEGER,v INTERVAL,d DATE)", 0))
		run(referenceExec("INSERT INTO dt_atomic VALUES(1,'1 hour','2024-01-01'),(2,'2 hours','2024-01-02')", 2))
		sentinel := referenceSelect("SELECT id,v,d::TEXT AS d FROM dt_atomic ORDER BY id", []string{"id", "v", "d"}, []string{"INT4", "INTERVAL", "TEXT"}, [][]any{{int64(1), "01:00:00", "2024-01-01"}, {int64(2), "02:00:00", "2024-01-02"}})
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
					s := referenceErrorDiagnostic(tc.q, controlsReferenceDiagnostic(tc.code, tc.message), tc.args...)
					s.prepared = prepared
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, c, b.recorder, s)
					referenceRunStep(t, p.ctx, c, b.recorder, sentinel)
				})
			}
		}
		// Bound operands ensure this is a row-demand failure, not a constant-folding probe.
		for _, prepared := range []bool{false, true} {
			s := referenceErrorDiagnostic("SELECT v/$1::DOUBLE PRECISION FROM dt_atomic ORDER BY id", controlsReferenceDiagnostic("22012", "division by zero"), 0)
			s.selectRows = true
			s.prepared = prepared
			s.errorStage = "execute"
			run(s)
			run(sentinel)
		}
	})
}
