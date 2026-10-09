package reference_test

import (
	"context"
	"strings"
	"testing"
)

func TestReferenceAnyValueDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE t(v INTEGER)", 0))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO t VALUES(1),(0)", 2))
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
						d := controlsReferenceDiagnostic(tc.code, tc.message)
						d.Position, d.Hint = tc.position, tc.hint
						s := referenceErrorDiagnostic(tc.q, d)
						if !backend.pg && strings.HasPrefix(tc.q, "INSERT") {
							// INSERT RETURNING is excluded by the parser before
							// aggregate consumer/signature analysis.
							s = referenceError(tc.q, "0A000")
						}
						s.prepared, s.selectRows, s.errorStage = prepared, !strings.HasPrefix(tc.q, "CREATE"), "execute"
						if prepared && s.selectRows {
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					}
				})
			}
			for _, arg := range []any{nil, "9", int64(9)} {
				var want any = int64(9)
				if arg == nil {
					want = nil
				}
				anyValueRoutes(t, p, backend, referenceSelect("SELECT $1::integer,any_value($1) FROM t GROUP BY 1", []string{"int4", "any_value"}, []string{"INT4", "INT4"}, [][]any{{want, want}}, arg))
				for _, q := range []string{"SELECT any_value($1) FROM t", "SELECT any_value($1)::integer FROM t", "SELECT any_value($1),$1::integer FROM t GROUP BY 2", "SELECT any_value($1) FROM t WHERE v=$1::integer", "SELECT any_value($1) FROM t HAVING $1::integer>0", "SELECT any_value($1) FROM t ORDER BY $1::integer"} {
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError(q, referenceDiagnostic{Code: "42804", Message: "could not determine polymorphic type because input has type unknown"}, arg)
						s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					}
				}
			}
		})
	}
}

func TestReferenceAnyValueBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE t(v INT)", 0))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO t VALUES(11),(23),(NULL)", 3))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE SEQUENCE av_boundary_sequence", 0))
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
					s := referenceSelect(tc.q, []string{"any_value"}, []string{"INT4"}, tc.rows)
					if strings.Contains(tc.q, "COALESCE") {
						s.columns = []string{"coalesce"}
					}
					if !backend.pg {
						s = referenceError(tc.q, "0A000")
						s.selectRows = true
					}
					s.prepared = prepared
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
			for _, q := range []string{"SELECT any_value(DISTINCT v) FROM t", "SELECT any_value(ALL v) FROM t", "SELECT any_value(v ORDER BY v) FROM t"} {
				for _, prepared := range []bool{false, true} {
					if backend.pg {
						anyValueMembership(t, p, backend, q, prepared, []string{"any_value"}, []string{"INT4"}, map[any][]any{nil: {int64(11), int64(23)}})
					} else {
						s := referenceError(q, "0A000")
						s.selectRows, s.prepared = true, prepared
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					}
				}
			}
			const windowQuery = "SELECT any_value(v) OVER () FROM t"
			for _, prepared := range []bool{false, true} {
				if !backend.pg {
					s := referenceError(windowQuery, "0A000")
					s.selectRows, s.prepared = true, prepared
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					continue
				}
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
				var executor referenceExecutor = backend.db
				if prepared {
					stmt, err := backend.db.PrepareContext(ctx, windowQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					executor = referenceHeldStatement{backend.db, stmt, windowQuery}
				}
				rows, err := executor.QueryContext(ctx, windowQuery)
				if err != nil {
					t.Fatal(err)
				}
				values := anyValueConsume(t, rows, backend.recorder, windowQuery, []string{"any_value"}, []string{"INT4"})
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
					s := referenceSelect(tc.q, tc.columns, tc.types, [][]any{tc.row})
					if !backend.pg {
						s = referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: "SELECT without FROM is unsupported", Position: "1"})
						s.selectRows = true
						s.errorStage = "execute"
						if prepared {
							s.errorStage = "prepare"
						}
					} else if prepared && tc.types[1] == "INT8" {
						s.rows = [][]any{{int64(1), int64(2)}}
					}
					s.prepared = prepared
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
		})
	}
}
