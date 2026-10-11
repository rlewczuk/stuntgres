package reference_test

import (
	"context"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAnyValueDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE t(v INTEGER)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1),(0)", 2))
			const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			for _, tc := range []struct{ q, code, message, position, hint string }{
				{"SELECT any_value(NULL) FROM t", "42804", "could not determine polymorphic type because input has type unknown", "", ""},
				{"SELECT any_value('x') FROM t", "42804", "could not determine polymorphic type because input has type unknown", "", ""},
				{"SELECT any_value() FROM t", "42883", "function any_value() does not exist", "8", hint},
				{"SELECT any_value(*) FROM t", "42883", "function any_value() does not exist", "8", hint},
				{"SELECT any_value(1,2) FROM t", "42883", "function any_value(integer, integer) does not exist", "8", hint},
				{"SELECT /*é*/ any_value(1,2) FROM t WHERE FALSE", "42883", "function any_value(integer, integer) does not exist", "14", hint},
				{"SELECT pg_catalog.any_value(1,2) FROM t", "42883", "function pg_catalog.any_value(integer, integer) does not exist", "8", hint},
				{"SELECT public.any_value(1) FROM t", "42883", "function public.any_value(integer) does not exist", "8", hint},
				{"SELECT av_missing.any_value(1) FROM t", "3F000", `schema "av_missing" does not exist`, "8", ""},
				{"SELECT any_value(" + strings.Repeat("1,", 99) + "1) FROM t", "42883", "function any_value(" + strings.Repeat("integer, ", 99) + "integer) does not exist", "8", hint},
				{"SELECT any_value(" + strings.Repeat("1,", 100) + "1) FROM t", "54023", "cannot pass more than 100 arguments to a function", "8", ""},
				{"SELECT av_missing.any_value(" + strings.Repeat("1,", 100) + "1) FROM t", "54023", "cannot pass more than 100 arguments to a function", "8", ""},
				{"SELECT av_missing.any_value(" + strings.Repeat("1,", 100) + "missing) FROM t", "42703", `column "missing" does not exist`, "229", ""},
				{"SELECT av_missing.any_value(missing) FROM t", "42703", `column "missing" does not exist`, "29", ""},
				{"SELECT any_value(any_value(v)) FROM t", "42803", "aggregate function calls cannot be nested", "18", ""},
				{"SELECT any_value(count(*)) FROM t", "42803", "aggregate function calls cannot be nested", "18", ""},
				{"SELECT any_value(count(*),1) FROM t", "42883", "function any_value(bigint, integer) does not exist", "8", hint},
				{"SELECT any_value(count(*),missing) FROM t", "42703", `column "missing" does not exist`, "27", ""},
				{"SELECT any_value(v) FROM t WHERE any_value(v)>0", "42803", "aggregate functions are not allowed in WHERE", "34", ""},
				{"SELECT v FROM t GROUP BY any_value(v)", "42803", "aggregate functions are not allowed in GROUP BY", "26", ""},
				{"SELECT v FROM t JOIN t AS u ON any_value(t.v)=u.v", "42803", "aggregate functions are not allowed in JOIN conditions", "32", ""},
				{"SELECT v FROM t LIMIT any_value(v)", "42803", "aggregate functions are not allowed in LIMIT", "23", ""},
				{"SELECT v FROM t OFFSET any_value(v)", "42803", "aggregate functions are not allowed in OFFSET", "24", ""},
				{"INSERT INTO t VALUES(2) RETURNING any_value(v)", "42803", "aggregate functions are not allowed in RETURNING", "35", ""},
				{"INSERT INTO t VALUES(2) RETURNING any_value(1,2)", "42883", "function any_value(integer, integer) does not exist", "35", hint},
				{"CREATE TABLE av_check(v INT CHECK(any_value(v)>0))", "42803", "aggregate functions are not allowed in check constraints", "35", ""},
			} {
				t.Run(tc.q, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						d.Position, d.Hint = tc.position, tc.hint
						s := fixture.ErrorDiagnostic(tc.q, d)
						if !backend.PG && strings.HasPrefix(tc.q, "INSERT") {
							// INSERT RETURNING is excluded by the parser before
							// aggregate consumer/signature analysis.
							s = fixture.Error(tc.q, "0A000")
						}
						s.Prepared, s.SelectRows, s.ErrorStage = prepared, !strings.HasPrefix(tc.q, "CREATE"), "execute"
						if prepared && s.SelectRows {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
				})
			}
			for _, arg := range []any{nil, "9", int64(9)} {
				var want any = int64(9)
				if arg == nil {
					want = nil
				}
				fixture.AnyValueRoutes(t, p, backend, fixture.Select("SELECT $1::integer,any_value($1) FROM t GROUP BY 1", []string{"int4", "any_value"}, []string{"INT4", "INT4"}, [][]any{{want, want}}, arg))
				for _, q := range []string{"SELECT any_value($1) FROM t", "SELECT any_value($1)::integer FROM t", "SELECT any_value($1),$1::integer FROM t GROUP BY 2", "SELECT any_value($1) FROM t WHERE v=$1::integer", "SELECT any_value($1) FROM t HAVING $1::integer>0", "SELECT any_value($1) FROM t ORDER BY $1::integer"} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42804", Message: "could not determine polymorphic type because input has type unknown"}, arg)
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
				}
			}
		})
	}
}

func TestReferenceAnyValueBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE t(v INT)", 0))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(11),(23),(NULL)", 3))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE SEQUENCE av_boundary_sequence", 0))
			for _, tc := range []struct {
				q    string
				rows [][]any
			}{
				{"SELECT any_value(1)", [][]any{{int64(1)}}},
				{"SELECT COALESCE(any_value(1),0)", [][]any{{int64(1)}}},
				{"SELECT any_value(v) FILTER (WHERE FALSE) FROM t", [][]any{{nil}}},
				{"SELECT any_value(v) FILTER (WHERE NULL) FROM t", [][]any{{nil}}},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.Select(tc.q, []string{"any_value"}, []string{"INT4"}, tc.rows)
					if strings.Contains(tc.q, "COALESCE") {
						s.Columns = []string{"coalesce"}
					}
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
			for _, q := range []string{"SELECT any_value(DISTINCT v) FROM t", "SELECT any_value(ALL v) FROM t", "SELECT any_value(v ORDER BY v) FROM t"} {
				for _, prepared := range []bool{false, true} {
					fixture.AnyValueMembership(t, p, backend, q, prepared, []string{"any_value"}, []string{"INT4"}, map[any][]any{nil: {int64(11), int64(23)}})
				}
			}
			const windowQuery = "SELECT any_value(v) OVER () FROM t"
			for _, prepared := range []bool{false, true} {
				if !backend.PG {
					s := fixture.Error(windowQuery, "0A000")
					s.SelectRows, s.Prepared = true, prepared
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					continue
				}
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
				var executor fixture.Executor = backend.DB
				if prepared {
					stmt, err := backend.DB.PrepareContext(ctx, windowQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = fixture.HeldStatement{backend.DB, stmt, windowQuery}
				}
				rows, err := executor.QueryContext(ctx, windowQuery)
				if err != nil {
					t.Fatal(err)
				}
				values := fixture.AnyValueConsume(t, rows, backend.Recorder, windowQuery, []string{"any_value"}, []string{"INT4"})
				if len(values) != 3 {
					t.Fatalf("window cardinality %#v", values)
				}
				for _, row := range values {
					if row[0] != int64(11) && row[0] != int64(23) {
						t.Fatalf("window eligibility %#v", row)
					}
				}
			}
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					q              string
					columns, types []string
					row            []any
				}{
					{"SELECT any_value(1),current_schema()", []string{"any_value", "current_schema"}, []string{"INT4", "NAME"}, []any{int64(1), "public"}},
					{"SELECT any_value(1),nextval('av_boundary_sequence')", []string{"any_value", "nextval"}, []string{"INT4", "INT8"}, []any{int64(1), int64(1)}},
				} {
					s := fixture.Select(tc.q, tc.columns, tc.types, [][]any{tc.row})
					if prepared && tc.types[1] == "INT8" {
						s.Rows = [][]any{{int64(1), int64(2)}}
					}
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
		})
	}
}
