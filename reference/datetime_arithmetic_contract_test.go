package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// These are pinned PostgreSQL18.0 records from the root's tests-1/artifact14.
// The fixture independently checks public raw codecs, rows and metadata, rather
// than obtaining any expectation from native execution.
func datetimeArithmeticContractBoth(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, s referenceStep) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.prepared = prepared
		var meta []*sql.ColumnType
		s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
			var err error
			meta, err = rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			datetimeArithmeticMetadata(t, b.pg, meta)
		}
		referenceRunStep(t, p.ctx, c, b.recorder, s)
		if s.state == "" {
			datetimeArithmeticMetadata(t, b.pg, meta)
			if len(meta) != len(s.types) {
				t.Fatal("retained descriptor width", meta)
			}
			for i, m := range meta {
				if m.Name() != s.columns[i] || m.DatabaseTypeName() != s.types[i] {
					t.Fatal("retained result descriptor", m)
				}
			}
		}
	}
}

func TestReferenceDatetimeArithmeticInference(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			query          string
			columns, types []string
			row, args      []any
		}{
			{"SELECT DATE '2000-01-01'-$1 AS result", []string{"result"}, []string{"INT4"}, []any{int64(1)}, []any{"1999-12-31"}},
			{"SELECT INTERVAL '1 day'+$1 AS result", []string{"result"}, []string{"INTERVAL"}, []any{"1 day 01:00:00"}, []any{"1 hour"}},
			{"SELECT INTERVAL '1 hour'*$1 AS result", []string{"result"}, []string{"INTERVAL"}, []any{"03:30:00"}, []any{3.5}},
			{"SELECT DATE '2024-02-28'+1::SMALLINT AS result", []string{"result"}, []string{"DATE"}, []any{datetimeArithmeticRaw(t, b.pg, "DATE", "2024-02-29")}, nil},
			{"SELECT DATE '2024-02-28'+1::INTEGER AS result", []string{"result"}, []string{"DATE"}, []any{datetimeArithmeticRaw(t, b.pg, "DATE", "2024-02-29")}, nil},
			{"SELECT INTERVAL '1 hour'*3.5::REAL AS result", []string{"result"}, []string{"INTERVAL"}, []any{"03:30:00"}, nil},
			{"SELECT 3.5::NUMERIC*INTERVAL '1 hour' AS result", []string{"result"}, []string{"INTERVAL"}, []any{"03:30:00"}, nil},
			{"SELECT TIME '12:00'=TIMETZ '12:00+00' AS result", []string{"result"}, []string{"BOOL"}, []any{true}, nil},
			{"SELECT TIMESTAMP '2024-01-02'-TIMESTAMPTZ '2024-01-01+00' AS result", []string{"result"}, []string{"INTERVAL"}, []any{"1 day"}, nil},
			// The first occurrence infers DATE. The later interval+DATE chooses
			// the released commutation; it is not a conflicting parameter type.
			{"SELECT DATE '2024-01-01'-$1,INTERVAL '1 day'+$1", []string{"?column?", "?column?"}, []string{"INT4", "TIMESTAMP"}, []any{int64(0), time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)}, []any{"2024-01-01"}},
		} {
			t.Run(tc.query, func(t *testing.T) {
				datetimeArithmeticContractBoth(t, p, b, c, referenceSelect(tc.query, tc.columns, tc.types, [][]any{tc.row}, tc.args...))
			})
		}
		for _, tc := range []struct {
			q, code, message, hint, position string
			args                             []any
		}{
			{"SELECT DATE '2000-01-01'+$1 AS result", "42725", "operator is not unique: date + unknown", "Could not choose a best candidate operator. You might need to add explicit type casts.", "25", []any{"1"}},
			{"SELECT DATE '2000-01-01'+'1' AS result", "42725", "operator is not unique: date + unknown", "Could not choose a best candidate operator. You might need to add explicit type casts.", "25", nil},
			{"SELECT $1*$2 AS result", "42725", "operator is not unique: unknown * unknown", "Could not choose a best candidate operator. You might need to add explicit type casts.", "10", []any{"2", "3"}},
			{"SELECT $1+$2 AS result", "42725", "operator is not unique: unknown + unknown", "Could not choose a best candidate operator. You might need to add explicit type casts.", "10", []any{"2", "3"}},
			{"SELECT $1-$2 AS result", "42725", "operator is not unique: unknown - unknown", "Could not choose a best candidate operator. You might need to add explicit type casts.", "10", []any{"2", "3"}},
			{"SELECT -$1 AS result", "42725", "operator is not unique: - unknown", "Could not choose a best candidate operator. You might need to add explicit type casts.", "8", []any{"2"}},
			{"SELECT DATE '2024-02-28'+1::BIGINT AS result", "42883", "operator does not exist: date + bigint", "No operator matches the given name and argument types. You might need to add explicit type casts.", "25", nil},
			{"/*é*/ SELECT DATE '2000-01-01' OPERATOR(pg_catalog.*) 2", "42883", "operator does not exist: date pg_catalog.* integer", "No operator matches the given name and argument types. You might need to add explicit type casts.", "32", nil},
			{"/*é*/ SELECT DATE '2000-01-01' OPERATOR(pg_catalog.+) '1'", "42725", "operator is not unique: date pg_catalog.+ unknown", "Could not choose a best candidate operator. You might need to add explicit type casts.", "32", nil},
			{"SELECT +INTERVAL '1 hour'", "42883", "operator does not exist: + interval", "No operator matches the given name and argument type. You might need to add an explicit type cast.", "8", nil},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := controlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint, d.Position = tc.hint, tc.position
				s := referenceErrorDiagnostic(tc.q, d, tc.args...)
				s.selectRows = true
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, c, b.recorder, s)
				s.errorStage = "prepare"
				referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
			})
		}
		// PostgreSQL accepts DATE−TIME through implicit TIME→INTERVAL, not a
		// direct date/time signature. That prerequisite cast is not implemented
		// natively and is not added by this operator-only part (root decision).
		q := "SELECT DATE '2000-01-01' - TIME '01:00' AS result"
		if b.pg {
			datetimeArithmeticContractBoth(t, p, b, c, referenceSelect(q, []string{"result"}, []string{"TIMESTAMP"}, [][]any{{time.Date(1999, 12, 31, 23, 0, 0, 0, time.UTC)}}))
		} else {
			d := controlsReferenceDiagnostic("42883", "operator does not exist: date - time without time zone")
			d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			d.Position = "26"
			s := referenceErrorDiagnostic(q, d)
			s.selectRows = true
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, c, b.recorder, s)
			s.errorStage = "prepare"
			referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
		}
	})
}

func TestReferenceDatetimeArithmeticExtremeClocks(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		// Exact observed signed64-wrap results. Reducing interval micros modulo
		// a day before adding would produce different values for several rows.
		for _, tc := range []struct{ clock, op, micros, want string }{
			{"00:00:00", "+", "9223372036854775807", "04:00:54.775807"},
			{"00:00:00", "+", "-9223372036854775808", "19:59:05.224192"},
			{"00:00:00", "-", "9223372036854775807", "19:59:05.224193"},
			{"00:00:00", "-", "-9223372036854775808", "19:59:05.224192"},
			{"00:00:00.000001", "+", "9223372036854775807", "19:59:05.224192"},
			{"00:00:00.000001", "+", "-9223372036854775808", "19:59:05.224193"},
			{"00:00:00.000001", "-", "9223372036854775807", "19:59:05.224194"},
			{"00:00:00.000001", "-", "-9223372036854775808", "19:59:05.224193"},
			{"23:59:59.999999", "+", "9223372036854775807", "19:59:05.22419"},
			{"23:59:59.999999", "+", "-9223372036854775808", "19:59:05.224191"},
			{"23:59:59.999999", "-", "9223372036854775807", "19:59:05.224192"},
			{"23:59:59.999999", "-", "-9223372036854775808", "19:59:05.224191"},
			{"24:00:00", "+", "9223372036854775807", "19:59:05.224191"},
			{"24:00:00", "+", "-9223372036854775808", "19:59:05.224192"},
			{"24:00:00", "-", "9223372036854775807", "19:59:05.224193"},
			{"24:00:00", "-", "-9223372036854775808", "19:59:05.224192"},
		} {
			for _, typ := range []string{"TIME", "TIMETZ"} {
				t.Run(typ+tc.clock+tc.op+tc.micros, func(t *testing.T) {
					input, want := tc.clock, tc.want
					if typ == "TIMETZ" {
						input += "+02:03:04"
						want += "+02:03:04"
					}
					q := "SELECT ($1::" + typ + tc.op + "$2::INTERVAL) AS result,($1::" + typ + tc.op + "$2::INTERVAL)::TEXT AS rendered"
					s := referenceSelect(q, []string{"result", "rendered"}, []string{typ, "TEXT"}, [][]any{{datetimeArithmeticRaw(t, b.pg, typ, want), want}}, input, tc.micros+" microseconds")
					s.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
						var rendered string
						if b.pg {
							var value time.Time
							if err := rows.Scan(&value, &rendered); err != nil || !value.Equal(datetimeArithmeticRaw(t, true, typ, want).(time.Time)) {
								t.Fatal(value, err)
							}
						} else {
							var value string
							if err := rows.Scan(&value, &rendered); err != nil || value != want {
								t.Fatal(value, err)
							}
						}
						if rendered != want {
							t.Fatal(rendered, want)
						}
					}
					datetimeArithmeticContractBoth(t, p, b, c, s)
				})
			}
		}
	})
}

func TestReferenceDatetimeArithmeticConstantPhases(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE dt_phase(id INTEGER,flag BOOLEAN)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO dt_phase VALUES(1,TRUE),(2,FALSE)", 2))
		for _, tc := range []struct{ q, code, message string }{
			{"SELECT INTERVAL '1 hour'/0 AS result", "22012", "division by zero"},
			{"SELECT DATE '5874897-12-31'+1 AS result", "22008", "date out of range"},
			{"SELECT NULL::INTERVAL+(INTERVAL '1 hour'/0) AS result", "22012", "division by zero"},
			{"SELECT CASE WHEN flag THEN INTERVAL '1 hour' ELSE INTERVAL '1 hour'/0 END AS result FROM dt_phase WHERE id=1", "22012", "division by zero"},
			{"SELECT CASE WHEN FALSE THEN INTERVAL '1 hour' ELSE INTERVAL '1 hour'/0 END AS result", "22012", "division by zero"},
			{"SELECT COALESCE(NULL::INTERVAL,INTERVAL '1 hour'/0) AS result", "22012", "division by zero"},
		} {
			t.Run(tc.q, func(t *testing.T) { datetimeArithmeticRuntimeError(t, p, b, c, tc.q, tc.code, tc.message) })
		}
		for _, tc := range []struct {
			q    string
			want any
		}{
			{"SELECT NULL::INTERVAL/0 AS result", nil},
			{"SELECT CASE WHEN TRUE THEN INTERVAL '1 hour' ELSE INTERVAL '1 hour'/0 END AS result", "01:00:00"},
			{"SELECT COALESCE(INTERVAL '1 hour',INTERVAL '1 hour'/0) AS result", "01:00:00"},
		} {
			datetimeArithmeticContractBoth(t, p, b, c, referenceSelect(tc.q, []string{"result"}, []string{"INTERVAL"}, [][]any{{tc.want}}))
		}
		for _, tc := range []struct {
			q    string
			args []any
		}{
			{"SELECT INTERVAL '1 hour'/0 AS result FROM dt_phase WHERE FALSE", nil},
			{"SELECT INTERVAL '1 hour'/$1::DOUBLE PRECISION AS result FROM dt_phase WHERE FALSE", []any{0}},
		} {
			// Ordinary native arithmetic is row-owned. PostgreSQL custom planning
			// evaluates these before its empty scan; neither contract is hidden.
			if b.pg {
				datetimeArithmeticRuntimeError(t, p, b, c, tc.q, "22012", "division by zero", tc.args...)
			} else {
				datetimeArithmeticContractBoth(t, p, b, c, referenceSelect(tc.q, []string{"result"}, []string{"INTERVAL"}, nil, tc.args...))
			}
		}
		s := referenceErrorDiagnostic("SELECT $1::INTERVAL/2 AS result", controlsReferenceDiagnostic("22007", "invalid input syntax for type interval: \"invalid\""), "invalid")
		s.diagnostic.Context = "unnamed portal parameter $1 = '...'"
		s.selectRows = true
		s.errorStage = "execute"
		datetimeArithmeticBoth(t, p, b, c, s)
		for _, tc := range []struct{ ddl, code, message string }{
			{"CREATE TABLE dt_phase_default(v INTERVAL DEFAULT INTERVAL '1 hour'/0)", "22012", "division by zero"},
			{"CREATE TABLE dt_phase_default(v DATE DEFAULT DATE '5874897-12-31'+1)", "22008", "date out of range"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.ddl+fmt.Sprint(prepared), func(t *testing.T) {
					referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("DROP TABLE IF EXISTS dt_phase_default", 0))
					create := referenceExec(tc.ddl, 0)
					create.prepared = prepared
					referenceRunStep(t, p.ctx, c, b.recorder, create)
					insert := referenceErrorDiagnostic("INSERT INTO dt_phase_default DEFAULT VALUES", controlsReferenceDiagnostic(tc.code, tc.message))
					insert.prepared = prepared
					insert.errorStage = "execute"
					referenceRunStep(t, p.ctx, c, b.recorder, insert)
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT v FROM dt_phase_default", []string{"v"}, []string{map[string]string{"22012": "INTERVAL", "22008": "DATE"}[tc.code]}, nil))
				})
			}
		}
	})
}

func TestReferenceDatetimeArithmeticIntrinsicStableDDL(t *testing.T) {
	datetimeArithmeticScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ q, message string }{
			{"CREATE TABLE dt_stable_generated(z TIMESTAMPTZ,g TIMESTAMPTZ GENERATED ALWAYS AS (z+INTERVAL '0') STORED)", "generation expression is not immutable"},
			{"CREATE TABLE dt_stable_generated(z TIMESTAMPTZ,g TIMESTAMPTZ GENERATED ALWAYS AS (INTERVAL '1 day'+z) STORED)", "generation expression is not immutable"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.q+fmt.Sprint(prepared), func(t *testing.T) {
					s := referenceErrorDiagnostic(tc.q, controlsReferenceDiagnostic("42P17", tc.message))
					s.prepared = prepared
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				})
			}
		}
		for _, q := range []string{
			"CREATE TABLE dt_ddl_immutable(s TIMESTAMP,g TIMESTAMP GENERATED ALWAYS AS (s+INTERVAL '1 day') STORED)",
			"CREATE TABLE dt_ddl_immutable(z TIMESTAMPTZ,g INTERVAL GENERATED ALWAYS AS (z-TIMESTAMPTZ '2000-01-01+00') STORED)",
		} {
			for _, prepared := range []bool{false, true} {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("DROP TABLE IF EXISTS dt_ddl_immutable", 0))
				s := referenceExec(q, 0)
				s.prepared = prepared
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			}
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE dt_ddl_index(z TIMESTAMPTZ,s TIMESTAMP)", 0))
		for _, expr := range []string{"z+INTERVAL '0'", "INTERVAL '1 day'+z", "z-INTERVAL '1 hour'"} {
			for _, prepared := range []bool{false, true} {
				t.Run(expr+fmt.Sprint(prepared), func(t *testing.T) {
					s := referenceErrorDiagnostic("CREATE INDEX dt_stable_idx ON dt_ddl_index (("+expr+"))", controlsReferenceDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE"))
					s.prepared = prepared
					s.errorStage = "execute"
					referenceRunStep(t, p.ctx, c, b.recorder, s)
				})
			}
		}
		for _, expr := range []string{"s+INTERVAL '1 day'", "z-TIMESTAMPTZ '2000-01-01+00'"} {
			for _, prepared := range []bool{false, true} {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("DROP INDEX IF EXISTS dt_immutable_idx", 0))
				s := referenceExec("CREATE INDEX dt_immutable_idx ON dt_ddl_index (("+expr+"))", 0)
				s.prepared = prepared
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			}
		}
	})
}
