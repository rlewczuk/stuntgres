package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringAggDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			stringAggSetup(run)
			run(fixture.Exec("CREATE TABLE diagnostic_out(v TEXT)", 0))
			const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			type diagnosticCase struct {
				name, q string
				want    fixture.Diagnostic
			}
			var cases []diagnosticCase
			for _, tc := range []struct{ name, args, signature string }{
				{"zero", "", ""}, {"star", "*", ""}, {"one", "v", "text"}, {"three", "v,d,id", "text, text, integer"},
				{"integer", "id,d", "integer, text"}, {"boolean", "TRUE,d", "boolean, text"}, {"text-binary", "v,b", "text, bytea"}, {"binary-text", "b,v", "bytea, text"},
				{"outer-before-nesting", "count(*),','", "bigint, unknown"},
			} {
				q := "SELECT string_agg(" + tc.args + ") FROM sa"
				cases = append(cases, diagnosticCase{tc.name, q, fixture.Diagnostic{Code: "42883", Message: "function string_agg(" + tc.signature + ") does not exist", Hint: hint, Position: "8"}})
			}
			for _, tc := range []struct {
				name, q, code, message, token string
				last                          bool
			}{
				{"missing-before-arity", "SELECT string_agg(missing,v,d) FROM sa", "42703", `column "missing" does not exist`, "missing", false},
				{"nested-value", "SELECT string_agg(string_agg(v,','),',') FROM sa", "42803", "aggregate function calls cannot be nested", "string_agg", true},
				{"nested-key", "SELECT string_agg(v,',' ORDER BY count(*)) FROM sa", "42803", "aggregate function calls cannot be nested", "count", false},
				{"where", "SELECT v FROM sa WHERE string_agg(v,',')='a'", "42803", "aggregate functions are not allowed in WHERE", "string_agg", false},
				{"group", "SELECT string_agg(v,',') FROM sa GROUP BY string_agg(v,',')", "42803", "aggregate functions are not allowed in GROUP BY", "string_agg", true},
				{"join", "SELECT sa.v FROM sa JOIN empty_sa ON string_agg(sa.v,',')='a'", "42803", "aggregate functions are not allowed in JOIN conditions", "string_agg", false},
				{"limit", "SELECT v FROM sa LIMIT length(string_agg(v,','))", "42803", "aggregate functions are not allowed in LIMIT", "string_agg", false},
				{"offset", "SELECT v FROM sa OFFSET length(string_agg(v,','))", "42803", "aggregate functions are not allowed in OFFSET", "string_agg", false},
				{"ungrouped", "SELECT id,string_agg(v,',') FROM sa", "42803", `column "sa.id" must appear in the GROUP BY clause or be used in an aggregate function`, "id", false},
				{"order-alias", "SELECT string_agg(v,',' ORDER BY answer) AS answer FROM sa", "42703", `column "answer" does not exist`, "answer", false},
				{"missing-key", "SELECT string_agg(v,',' ORDER BY missing) FROM sa WHERE FALSE", "42703", `column "missing" does not exist`, "missing", false},
				{"missing-schema", "SELECT absent.string_agg(v,d) FROM sa", "3F000", `schema "absent" does not exist`, "absent", false},
				{"qualified-signature", "SELECT pg_catalog.string_agg(id,d) FROM sa", "42883", "function pg_catalog.string_agg(integer, text) does not exist", "pg_catalog", false},
				{"missing-qualified-child", "SELECT absent.string_agg(missing) FROM sa", "42703", `column "missing" does not exist`, "missing", false},
				{"check", "CREATE TABLE bad_check(v TEXT CHECK(string_agg(v,',')='a'))", "42803", "aggregate functions are not allowed in check constraints", "string_agg", false},
				{"values", "INSERT INTO diagnostic_out VALUES(string_agg('a',','))", "42803", "aggregate functions are not allowed in VALUES", "string_agg", false},
				{"returning", "INSERT INTO diagnostic_out VALUES('a') RETURNING string_agg(v,',')", "42803", "aggregate functions are not allowed in RETURNING", "string_agg", false},
				{"comma-order", "SELECT string_agg(v,',' ORDER BY ,id) FROM sa", "42601", `syntax error at or near ","`, ",id", false},
				{"order-eof", "SELECT string_agg(v,',' ORDER BY", "42601", "syntax error at end of input", "", true},
				{"unicode-order", "SELECT 'é',string_agg(v,',' ORDER BY ,id) FROM sa", "42601", `syntax error at or near ","`, ",id", false},
			} {
				w := fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.StringAggPosition(tc.q, tc.token, tc.last)}
				if tc.code == "42883" {
					w.Hint = hint
				}
				cases = append(cases, diagnosticCase{tc.name, tc.q, w})
			}
			args := strings.TrimSuffix(strings.Repeat("v,", 101), ",")
			cases = append(cases, diagnosticCase{"argument-limit", "SELECT string_agg(" + args + ") FROM sa", fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}})
			cases = append(cases, diagnosticCase{"limit-before-schema", "SELECT absent.string_agg(" + args + ") FROM sa", fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}})
			hundred := strings.TrimSuffix(strings.Repeat("v,", 100), ",")
			signature := strings.TrimSuffix(strings.Repeat("text, ", 100), ", ")
			cases = append(cases, diagnosticCase{"hundred-signature", "SELECT string_agg(" + hundred + ") FROM sa", fixture.Diagnostic{Code: "42883", Message: "function string_agg(" + signature + ") does not exist", Hint: hint, Position: "8"}})
			q := "SELECT absent.string_agg(missing," + args + ") FROM sa"
			cases = append(cases, diagnosticCase{"missing-before-limit", q, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: fixture.StringAggPosition(q, "missing", false)}})
			// Complete records captured by the root's pinned PostgreSQL 18.0 run.
			cases = append(cases,
				diagnosticCase{"parameter-conflict", "SELECT string_agg($1,($1::integer)::text) FROM sa", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "19"}},
				diagnosticCase{"collation-conflict", `SELECT string_agg(v COLLATE "C",d COLLATE "POSIX") FROM sa`, fixture.Diagnostic{Code: "42P21", Message: `collation mismatch between explicit collations "C" and "POSIX"`, Position: "35"}},
				diagnosticCase{"unknown-namespace", "SELECT public.string_agg(v,d) FROM sa", fixture.Diagnostic{Code: "42883", Message: "function public.string_agg(text, text) does not exist", Hint: hint, Position: "8"}},
				diagnosticCase{"bad-order-type", "SELECT string_agg(v,',' ORDER BY 'x'::xml) FROM sa", fixture.Diagnostic{Code: "42883", Message: "could not identify an ordering operator for type xml", Hint: "Use an explicit ordering operator or modify the query.", Position: "34"}},
			)
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
							s := fixture.ImplicitError(tc.q, tc.want)
							s.SelectRows = strings.HasPrefix(tc.q, "SELECT")
							s.Prepared = prepared
							if tc.name == "parameter-conflict" {
								s.Args = []any{int64(1)}
							}
							runStep := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
							runStep(s)
						})
					}
				})
			}
		})
	}
}

func TestReferenceStringAggEvaluationFailures(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE fail_sa(k INT,id INT,v TEXT)", 0))
			run(fixture.Exec("INSERT INTO fail_sa VALUES(1,1,'a'),(2,0,NULL)", 2))
			run(fixture.Exec("CREATE TABLE fail_out(v TEXT)", 0))
			run(fixture.Exec("INSERT INTO fail_out VALUES('sentinel')", 1))
			for _, expr := range []string{"string_agg((10/id)::text,',' ORDER BY id)", "string_agg(v,(10/id)::text ORDER BY id)", "string_agg(v,',' ORDER BY 10/id)"} {
				for _, prefix := range []string{"SELECT ", "INSERT INTO fail_out SELECT "} {
					q := prefix + expr + " FROM fail_sa GROUP BY k"
					for _, prepared := range []bool{false, true} {
						t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
							if prefix == "SELECT " {
								wantRow := "a"
								if strings.HasPrefix(expr, "string_agg((10/id)") {
									wantRow = "10"
								}
								stringAggRuntimeQueryError(t, p.Ctx, backend.DB, backend.PG, q, prepared, wantRow)
							} else {
								s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
								s.Prepared = prepared
								s.ErrorStage = "execute"
								fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
							}
						})
					}
				}
			}
			run(fixture.Select("SELECT v FROM fail_out", []string{"v"}, []string{"TEXT"}, [][]any{{"sentinel"}}))
		})
	}
}

// Native aggregate finalization is eager and must not publish any cursor.
// PostgreSQL can stream a completed group before reporting a later group's
// error. Both routes must expose the same complete runtime diagnostic.
func stringAggRuntimeQueryError(t *testing.T, ctx context.Context, db *sql.DB, pg bool, q string, prepared bool, wantRow string) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, prepareErr := db.PrepareContext(ctx, q)
		if prepareErr != nil {
			t.Fatalf("runtime-error query failed during Prepare: %v", prepareErr)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(ctx)
	} else {
		rows, err = db.QueryContext(ctx, q)
	}
	want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
	if err != nil {
		if rows != nil {
			rows.Close()
			t.Fatal("query error published a cursor")
		}
		fixture.AssertDiagnostic(t, err, &want)
		return
	}
	if rows == nil {
		t.Fatal("query returned neither error nor cursor")
	}
	defer rows.Close()
	if !pg {
		t.Fatal("native late aggregate failure published a cursor")
	}
	stringAggMetadata(pg)(t, rows)
	count := 0
	for rows.Next() {
		var got string
		if scanErr := rows.Scan(&got); scanErr != nil {
			t.Fatal(scanErr)
		}
		count++
		if count > 1 || got != wantRow {
			t.Fatalf("unexpected partial PostgreSQL row %d: %q want %q", count, got, wantRow)
		}
	}
	fixture.AssertDiagnostic(t, rows.Err(), &want)
}
