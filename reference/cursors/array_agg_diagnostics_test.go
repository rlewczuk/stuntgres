package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArrayAggDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			arrayAggSetup(run)
			run(fixture.Exec("CREATE TABLE aa_out(v INT[])", 0))
			const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			type diagnosticCase struct {
				name, q string
				want    fixture.Diagnostic
				args    []any
			}
			var cases []diagnosticCase
			for _, tc := range []struct{ name, args, sig string }{{"zero", "", ""}, {"star", "*", ""}, {"two", "v,id", "integer, integer"}} {
				cases = append(cases, diagnosticCase{tc.name, "SELECT array_agg(" + tc.args + ") FROM aa_scalar", fixture.Diagnostic{Code: "42883", Message: "function array_agg(" + tc.sig + ") does not exist", Hint: hint, Position: "8"}, nil})
			}
			for _, tc := range []struct {
				name, q, code, message, token string
				last                          bool
			}{
				{"missing-child", "SELECT absent.array_agg(missing,v) FROM aa_scalar", "42703", `column "missing" does not exist`, "missing", false},
				{"schema", "SELECT absent.array_agg(v) FROM aa_scalar", "3F000", `schema "absent" does not exist`, "absent", false},
				{"public", "SELECT public.array_agg(v) FROM aa_scalar", "42883", "function public.array_agg(integer) does not exist", "public", false},
				{"nested", "SELECT array_agg(array_agg(v)) FROM aa_scalar", "42803", "aggregate function calls cannot be nested", "array_agg", true},
				{"where", "SELECT v FROM aa_scalar WHERE array_agg(v) IS NULL", "42803", "aggregate functions are not allowed in WHERE", "array_agg", false},
				{"group", "SELECT array_agg(v) FROM aa_scalar GROUP BY array_agg(v)", "42803", "aggregate functions are not allowed in GROUP BY", "array_agg", true},
				{"join", "SELECT aa_scalar.v FROM aa_scalar JOIN aa_array ON array_agg(aa_scalar.v) IS NULL", "42803", "aggregate functions are not allowed in JOIN conditions", "array_agg", false},
				{"filter", "SELECT array_agg(v) FILTER(WHERE count(*)>0) FROM aa_scalar", "42803", "aggregate functions are not allowed in FILTER", "count", false},
				{"check", "CREATE TABLE aa_bad(v INT CHECK(array_agg(v) IS NULL))", "42803", "aggregate functions are not allowed in check constraints", "array_agg", false},
				{"index", "CREATE INDEX aa_bad_index ON aa_scalar ((array_agg(v)))", "42803", "aggregate functions are not allowed in index expressions", "array_agg", false},
				{"values", "INSERT INTO aa_out VALUES(array_agg(1))", "42803", "aggregate functions are not allowed in VALUES", "array_agg", false},
				{"ungrouped", "SELECT id,array_agg(v) FROM aa_scalar", "42803", `column "aa_scalar.id" must appear in the GROUP BY clause or be used in an aggregate function`, "id", false},
				{"missing-key", "SELECT array_agg(v ORDER BY missing) FROM aa_scalar WHERE FALSE", "42703", `column "missing" does not exist`, "missing", false},
				{"distinct-key", "SELECT array_agg(DISTINCT v ORDER BY id) FROM aa_scalar", "42P10", "in an aggregate with DISTINCT, ORDER BY expressions must appear in argument list", "id", false},
				{"unicode", "SELECT 'é',array_agg(v ORDER BY missing) FROM aa_scalar", "42703", `column "missing" does not exist`, "missing", false},
				{"qualified-zero", "SELECT pg_catalog.array_agg()", "42883", "function pg_catalog.array_agg() does not exist", "pg_catalog", false},
			} {
				w := fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.StringAggPosition(tc.q, tc.token, tc.last)}
				if tc.code == "42883" {
					w.Hint = hint
				}
				cases = append(cases, diagnosticCase{tc.name, tc.q, w, nil})
			}
			for _, n := range []int{100, 101} {
				args := strings.TrimSuffix(strings.Repeat("v,", n), ",")
				w := fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}
				if n == 100 {
					w.Code = "42883"
					w.Message = "function array_agg(" + strings.TrimSuffix(strings.Repeat("integer, ", n), ", ") + ") does not exist"
					w.Hint = hint
				}
				cases = append(cases, diagnosticCase{args, "SELECT array_agg(" + args + ") FROM aa_scalar", w, nil})
				if n == 101 {
					cases = append(cases, diagnosticCase{"limit-before-schema", "SELECT absent.array_agg(" + args + ") FROM aa_scalar", w, nil})
					q := "SELECT absent.array_agg(missing," + args + ") FROM aa_scalar"
					cases = append(cases, diagnosticCase{"missing-before-limit", q, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: fixture.StringAggPosition(q, "missing", false)}, nil})
				}
			}
			// Pinned PostgreSQL 18.0: the two polymorphic overloads make unknown
			// inputs ambiguous, including unresolved parameter occurrences.
			for _, arg := range []string{"NULL", "'x'", "$1"} {
				var args []any
				if arg == "$1" {
					args = []any{int64(1)}
				}
				cases = append(cases, diagnosticCase{"unknown-" + arg, "SELECT array_agg(" + arg + ")", fixture.Diagnostic{Code: "42725", Message: "function array_agg(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: "8"}, args})
			}
			cases = append(cases, diagnosticCase{"parameter-late", "SELECT array_agg($1),$1::integer FROM aa_scalar", fixture.Diagnostic{Code: "42725", Message: "function array_agg(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: "8"}, []any{int64(2)}})
			q := "SELECT array_agg($1::integer),array_agg($1::integer[]) FROM aa_scalar"
			// Pinned PostgreSQL 18.0 reports the second cast operator, not its
			// parameter token; all optional diagnostic fields are absent.
			cases = append(cases, diagnosticCase{"parameter-incompatible", q, fixture.Diagnostic{Code: "42846", Message: "cannot cast type integer to integer[]", Position: "43"}, []any{int64(2)}})
			q = "SELECT 'é',pg_catalog.array_agg(NULL)"
			cases = append(cases, diagnosticCase{"qualified-unicode-unknown", q, fixture.Diagnostic{Code: "42725", Message: "function pg_catalog.array_agg(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: fixture.StringAggPosition(q, "pg_catalog", false)}, nil})
			// Pinned PostgreSQL 18.0: cast-first fixes integer; later casts convert it.
			for i, s := range []fixture.Step{
				fixture.Select("SELECT $1::integer,array_agg($1 ORDER BY id) FROM aa_scalar", []string{"int4", "array_agg"}, []string{"INT4", "_INT4"}, [][]any{{int64(2), "{2,2,2,2,2}"}}, int64(2)),
				arrayAggSelect("SELECT array_agg($1::integer ORDER BY id) FROM aa_scalar", "_INT4", [][]any{{"{2,2,2,2,2}"}}, b.PG, int64(2)),
				arrayAggSelect("SELECT array_agg($1::integer[] ORDER BY id) FROM aa_array", "_INT4", [][]any{{"{{7,NULL},{7,NULL}}"}}, b.PG, "{7,NULL}"),
				fixture.Select("SELECT array_agg($1::integer),$1::text FROM aa_scalar", []string{"array_agg", "text"}, []string{"_INT4", "TEXT"}, [][]any{{"{2,2,2,2,2}", "2"}}, int64(2)),
			} {
				t.Run(fixture.Position(i), func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						})
					}
				})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
							s := fixture.ImplicitError(tc.q, tc.want, tc.args...)
							s.SelectRows = strings.HasPrefix(tc.q, "SELECT")
							s.Prepared = prepared
							s.ErrorStage = "execute"
							if prepared && !strings.HasPrefix(tc.q, "CREATE") {
								s.ErrorStage = "prepare"
							}
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						})
					}
				})
			}
		})
	}
}

// Runtime arrays are table values, so Prepare must succeed. PostgreSQL may
// surface an error through Query or Rows.Err; native must never publish a cursor.
func arrayAggRuntimeError(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, prepared bool, w fixture.Diagnostic, partial ...string) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := b.DB.PrepareContext(p.Ctx, q)
		if e != nil {
			t.Fatalf("runtime Prepare: %v", e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx)
	} else {
		rows, err = b.DB.QueryContext(p.Ctx, q)
	}
	w.Severity, w.SeverityUnlocalized = "ERROR", "ERROR"
	if err != nil {
		if rows != nil {
			rows.Close()
			t.Fatal("error exposed cursor")
		}
		fixture.AssertDiagnostic(t, err, &w)
		return
	}
	if rows == nil {
		t.Fatal("missing error and cursor")
	}
	defer rows.Close()
	if !b.PG {
		t.Fatal("native aggregate error exposed cursor")
	}
	count := 0
	for rows.Next() {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, true, "_INT4", raw)
		if count >= len(partial) || fixture.Normalize(raw) != partial[count] {
			t.Fatalf("unexpected partial aggregate row %d: %#v", count, raw)
		}
		count++
	}
	fixture.AssertDiagnostic(t, rows.Err(), &w)
}

func TestReferenceArrayAggRuntimeDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE aa_fail(id INT,v INT[])", 0))
			var previous int64
			for _, tc := range []struct{ name, values, code, message string }{
				{"empty", "(1,'{}')", "2202E", "cannot accumulate empty arrays"},
				{"null", "(1,NULL)", "22004", "cannot accumulate null arrays"},
				{"late-null", "(1,'{1,NULL}'),(2,NULL)", "22004", "cannot accumulate null arrays"},
				{"late-empty", "(1,'{1,NULL}'),(2,'{}')", "2202E", "cannot accumulate arrays of different dimensionality"},
				{"extent", "(1,'{1,2}'),(2,'{3}')", "2202E", "cannot accumulate arrays of different dimensionality"},
				{"bounds", "(1,'[0:1]={1,2}'),(2,'[1:2]={3,4}')", "2202E", "cannot accumulate arrays of different dimensionality"},
				{"rank", "(1,'{1,2}'),(2,'{{3,4}}')", "2202E", "cannot accumulate arrays of different dimensionality"},
				{"max-rank", "(1,'{{{{{{7}}}}}}')", "54000", "number of array dimensions (7) exceeds the maximum allowed (6)"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					run(fixture.Exec("DELETE FROM aa_fail", previous))
					count := int64(1)
					if strings.Contains(tc.values, "(2,") {
						count = 2
					}
					run(fixture.Exec("INSERT INTO aa_fail VALUES"+tc.values, count))
					previous = count
					w := fixture.Diagnostic{Code: tc.code, Message: tc.message}
					for _, prepared := range []bool{false, true} {
						arrayAggRuntimeError(t, p, b, "SELECT array_agg(v ORDER BY id) FROM aa_fail", prepared, w)
						if count == 1 || tc.code == "22004" {
							arrayAggRuntimeError(t, p, b, "SELECT array_agg(v) FROM aa_fail", prepared, w)
						}
					}
					fixture.ArrayAggBoth(run, arrayAggSelect("SELECT array_agg(v) FROM aa_fail WHERE FALSE", "_INT4", [][]any{{nil}}, b.PG))
					fixture.ArrayAggBoth(run, arrayAggSelect("SELECT array_agg(v) FILTER(WHERE FALSE) FROM aa_fail", "_INT4", [][]any{{nil}}, b.PG))
				})
			}
			run(fixture.Exec("DELETE FROM aa_fail", 1))
			run(fixture.Exec("INSERT INTO aa_fail VALUES(1,'{1,NULL}'),(2,NULL)", 2))
			fixture.ArrayAggBoth(run, arrayAggSelect("SELECT array_agg(v ORDER BY id) FILTER(WHERE v IS NOT NULL) FROM aa_fail", "_INT4", [][]any{{"{{1,NULL}}"}}, b.PG))
		})
	}
}
