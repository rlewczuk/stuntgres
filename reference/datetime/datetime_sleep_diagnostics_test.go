package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Exact records observed by root in pinned PG18.0, tests-1/artifact11.
func datetimeSleepError(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func datetimeSleepFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, prepared, analysis bool, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
	if prepared && analysis {
		s.ErrorStage = "prepare"
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func TestReferenceDatetimeSleepDiagnostics(t *testing.T) {
	datetimeSleepScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ expr, signature string }{
			{"pg_sleep()", "pg_sleep()"}, {"pg_sleep_for()", "pg_sleep_for()"}, {"pg_sleep_until()", "pg_sleep_until()"},
			{"pg_sleep(0,0)", "pg_sleep(integer, integer)"}, {"pg_sleep_for(NULL,NULL)", "pg_sleep_for(unknown, unknown)"}, {"pg_sleep_until(NULL,NULL)", "pg_sleep_until(unknown, unknown)"},
			{"pg_sleep(true)", "pg_sleep(boolean)"}, {"pg_sleep('0'::text)", "pg_sleep(text)"}, {"pg_sleep_for(1)", "pg_sleep_for(integer)"}, {"pg_sleep_for('0'::text)", "pg_sleep_for(text)"}, {"pg_sleep_until(1)", "pg_sleep_until(integer)"}, {"pg_sleep_until('2000-01-01'::text)", "pg_sleep_until(text)"},
			{"pg_sleep(seconds=>0)", "pg_sleep(seconds => integer)"}, {"pg_sleep_for(delay=>NULL)", "pg_sleep_for(delay => unknown)"}, {"pg_sleep_until(deadline=>NULL)", "pg_sleep_until(deadline => unknown)"},
			{"pg_sleep(VARIADIC ARRAY[0])", "pg_sleep(integer[])"}, {`"PG_SLEEP"(0)`, "PG_SLEEP(integer)"},
			{"upper(pg_sleep(0))", "upper(void)"},
		} {
			d := datetimeSleepError("42883", "function "+tc.signature+" does not exist")
			d.Hint, d.Position = hint, "15"
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t", tc.expr, prepared), func(t *testing.T) { datetimeSleepFailure(t, p, b, c, "/*é🙂*/ SELECT "+tc.expr, d, prepared, true) })
			}
		}
		for _, tc := range []struct {
			expr, code, message, position, hint string
			runtime                             bool
		}{
			{"pg_sleep(DISTINCT 0)", "42809", "DISTINCT specified, but pg_sleep is not an aggregate function", "15", "", false},
			{"pg_sleep(0 ORDER BY 0)", "42809", "ORDER BY specified, but pg_sleep is not an aggregate function", "15", "", false},
			{"pg_sleep(0) FILTER (WHERE true)", "42809", "FILTER specified, but pg_sleep is not an aggregate function", "15", "", false},
			{"missing_schema.pg_sleep(0)", "3F000", `schema "missing_schema" does not exist`, "15", "", false},
			{"pg_sleep('bad')", "22P02", `invalid input syntax for type double precision: "bad"`, "24", "", false},
			{"pg_sleep_for('bad')", "22007", `invalid input syntax for type interval: "bad"`, "28", "", false},
			{"pg_sleep_until('bad')", "22007", `invalid input syntax for type timestamp with time zone: "bad"`, "30", "", false},
			{"pg_sleep('1e400')", "22003", `"1e400" is out of range for type double precision`, "24", "", false},
			{"pg_sleep_for(interval '2147483648 months')", "22015", `interval field value out of range: "2147483648 months"`, "37", "", false},
			{"pg_sleep_for(interval '2147483647 months')", "22008", "timestamp out of range", "", "", true},
			{"pg_sleep_until(timestamptz '294277-01-01')", "22008", `timestamp out of range: "294277-01-01"`, "42", "", false},
			{"pg_sleep_until(timestamptz '2000-13-01')", "22008", `date/time field value out of range: "2000-13-01"`, "42", `Perhaps you need a different "DateStyle" setting.`, false},
		} {
			d := datetimeSleepError(tc.code, tc.message)
			d.Position, d.Hint = tc.position, tc.hint
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t", tc.expr, prepared), func(t *testing.T) {
					datetimeSleepFailure(t, p, b, c, "/*é🙂*/ SELECT "+tc.expr, d, prepared, !tc.runtime)
				})
			}
		}
		for _, tc := range []struct{ name, code, typ string }{{"pg_sleep", "22P02", "double precision"}, {"pg_sleep_for", "22007", "interval"}, {"pg_sleep_until", "22007", "timestamp with time zone"}} {
			d := datetimeSleepError(tc.code, "invalid input syntax for type "+tc.typ+": \"bad\"")
			d.Context = "unnamed portal parameter $1 = '...'"
			t.Run(tc.name+"-bad-bind", func(t *testing.T) {
				datetimeSleepFailure(t, p, b, c, "/*é🙂*/ SELECT "+tc.name+"($1)", d, true, false, "bad")
			})
		}
		for _, tc := range []struct {
			expr string
			null bool
		}{{"pg_sleep(0)", false}, {"pg_sleep_for(interval '0')", false}, {"pg_sleep_until(timestamptz '2000-01-01')", false}, {"pg_sleep(NULL)", true}, {"pg_sleep_for(NULL)", true}, {"pg_sleep_until(NULL)", true}} {
			t.Run(tc.expr+"-metadata", func(t *testing.T) {
				for _, empty := range []bool{false, true} {
					q := "SELECT " + tc.expr + " AS result"
					var value any = ""
					if tc.null {
						value = nil
					}
					want := [][]any{{value}}
					if empty {
						q += " WHERE false"
						want = nil
					}
					s := fixture.Select(q, []string{"result"}, []string{"VOID"}, want)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						cts, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						if len(cts) != 1 {
							t.Fatalf("columns: %d", len(cts))
						}
						ct := cts[0]
						wantScan := reflect.TypeFor[string]()
						if b.PG {
							wantScan = reflect.TypeFor[any]()
						}
						if ct.ScanType() != wantScan {
							t.Fatalf("VOID scan type: %v want %v", ct.ScanType(), wantScan)
						}
						if n, ok := ct.Length(); n != 0 || ok {
							t.Fatalf("VOID length: %d/%t", n, ok)
						}
						if n, s, ok := ct.DecimalSize(); n != 0 || s != 0 || ok {
							t.Fatalf("VOID decimal: %d,%d/%t", n, s, ok)
						}
						if n, ok := ct.Nullable(); n || ok {
							t.Fatalf("computed VOID nullable: %t/%t", n, ok)
						}
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SELECT "+tc.expr, 1))
			})
		}
	})
}

func TestReferenceDatetimeSleepCancellation(t *testing.T) {
	datetimeSleepScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET statement_timeout='100ms'", 0))
		d := datetimeSleepError("57014", "canceling statement due to statement timeout")
		for _, expr := range []string{"pg_sleep(10)", "pg_sleep('Infinity'::float8)", "pg_sleep('1e300'::float8)", "pg_sleep_for(interval '10 seconds')", "pg_sleep_for(interval 'infinity')", "pg_sleep_for(interval '1 day')", "pg_sleep_until(clock_timestamp()+interval '10 seconds')", "pg_sleep_until(timestamptz 'infinity')"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t", expr, prepared), func(t *testing.T) { datetimeSleepFailure(t, p, b, c, "SELECT "+expr, d, prepared, false) })
			}
		}
		// Keep fixture setup outside the short timeout used by sleep witnesses.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET statement_timeout=0", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT 1 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(1)}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE sleep_cancel_probe(id integer)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO sleep_cancel_probe VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET statement_timeout='100ms'", 0))
		datetimeSleepFailure(t, p, b, c, "SELECT pg_sleep(10)", d, false, false)
		datetimeSleepFailure(t, p, b, c, "SELECT count(*) FROM sleep_cancel_probe", datetimeSleepError("25P02", "current transaction is aborted, commands ignored until end of transaction block"), false, false)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET statement_timeout=0", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM sleep_cancel_probe", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		datetimeSleepOwners(t, p, b, c)
		datetimeSleepActiveCancellation(t, p, b, c)
		datetimeSleepAtomicWriteCancellation(t, p, b, c)
	})
}

func TestReferenceDatetimeSleepStored(t *testing.T) {
	datetimeSleepScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ q, code, message string }{
			{"CREATE TABLE sleep_void_column(v void)", "42P16", `column "v" has pseudo-type void`},
			{"CREATE TABLE sleep_default_text(v text DEFAULT pg_sleep(0)::text)", "", ""},
			{"CREATE TABLE sleep_default_bool(v boolean DEFAULT (pg_sleep(0) IS NULL))", "", ""},
			{"CREATE TABLE sleep_check(v integer CHECK (pg_sleep(0) IS NOT NULL))", "", ""},
			{"CREATE TABLE sleep_generated(v integer, s boolean GENERATED ALWAYS AS (pg_sleep(0) IS NULL) STORED)", "42P17", "generation expression is not immutable"},
			{"CREATE TABLE sleep_index(v integer)", "", ""},
			{"CREATE INDEX sleep_volatile_index ON sleep_index ((pg_sleep(0) IS NULL))", "42P17", "functions in index expression must be marked IMMUTABLE"},
			{"CREATE INDEX sleep_volatile_predicate ON sleep_index(v) WHERE pg_sleep(0) IS NULL", "42P17", "functions in index predicate must be marked IMMUTABLE"},
			{"CREATE VIEW sleep_view AS SELECT pg_sleep(0) AS v", "42P16", `column "v" has pseudo-type void`},
		} {
			t.Run(tc.q, func(t *testing.T) {
				if tc.code == "" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(tc.q, 0))
					return
				}
				d := datetimeSleepError(tc.code, tc.message)
				// VOID table-column support is an adjacent type-DDL boundary,
				// not expanded by implementing the sleep scalar family.
				if !b.PG && tc.q == "CREATE TABLE sleep_void_column(v void)" {
					d = datetimeSleepError("0A000", "unsupported column type")
					d.Position = "34"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(tc.q, d))
			})
		}
		datetimeSleepVoidViewBoundary(t, p, b, c)
		datetimeSleepValue(t, p, b, c, "SELECT CASE WHEN false THEN pg_sleep(10) ELSE pg_sleep(0) END AS result", false)
		datetimeSleepValue(t, p, b, c, "SELECT pg_sleep(NULL::float8) AS result", true)
		for _, tc := range []struct {
			expr  string
			value bool
		}{{"pg_sleep(0) IS NULL", false}, {"pg_sleep(NULL) IS NULL", true}} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT "+tc.expr+" AS result", []string{"result"}, []string{"BOOL"}, [][]any{{tc.value}}))
		}
		datetimeSleepStoredExecution(t, p, b, c)
	})
}

func TestReferenceDatetimeSleepFeatureSmoke(t *testing.T) {
	datetimeSleepScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		start := time.Now()
		datetimeSleepValue(t, p, b, c, "SELECT pg_sleep(0.075) AS result", false)
		if time.Since(start) < 75*time.Millisecond {
			t.Fatal("fractional smoke returned early")
		}
		datetimeSleepValue(t, p, b, c, "SELECT pg_sleep_for(interval '-1 second') AS result", false)
		datetimeSleepValue(t, p, b, c, "SELECT pg_sleep_until(timestamptz '2000-01-01') AS result", false)
		datetimeSleepValue(t, p, b, c, "SELECT pg_sleep(NULL) AS result", true)
		intervalSQL := "SELECT pg_sleep_for($1) AS result"
		interval, err := c.PrepareContext(p.Ctx, intervalSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer interval.Close()
		held := fixture.HeldStatement{Executor: c, Statement: interval, SQL: intervalSQL}
		start = time.Now()
		datetimeSleepValue(t, p, b, held, intervalSQL, false, "75 milliseconds")
		if time.Since(start) < 75*time.Millisecond {
			t.Fatal("prepared interval smoke returned early")
		}
		datetimeSleepValue(t, p, b, held, intervalSQL, true, nil)
		untilSQL := "SELECT pg_sleep_until($1) AS result"
		until, err := c.PrepareContext(p.Ctx, untilSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer until.Close()
		target := time.Now().Add(100 * time.Millisecond).Truncate(time.Microsecond)
		datetimeSleepValue(t, p, b, fixture.HeldStatement{Executor: c, Statement: until, SQL: untilSQL}, untilSQL, false, target)
		if time.Now().Before(target) {
			t.Fatal("until smoke returned early")
		}
		datetimeSleepValue(t, p, b, c, "SELECT pg_sleep(-0.1) AS result", false)
		datetimeSleepValue(t, p, b, c, "SELECT pg_sleep('NaN'::float8) AS result", false)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE sleep_cancel_probe(id integer)", 0))
		datetimeSleepOwners(t, p, b, c)
		datetimeSleepActiveCancellation(t, p, b, c)
	})
}
