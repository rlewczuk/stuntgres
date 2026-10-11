package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// These independent expectations follow REL_18_0 parse_func.c/parse_agg.c
// and the existing visibility-dependent domain diagnostic contract. The root
// validates them against the pinned release before authorizing production fixes.
func numericSumReviewScenario(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p, conn, backend.Recorder, backend.PG)
		})
	}
}

// Analysis errors must preserve complete fields, nil Exec/Query outputs, and a
// nil prepared handle. The unchanged-state witness runs after each failed route.
func numericSumReviewDiagnostic(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, query string, want fixture.Diagnostic, unchanged *fixture.Step) {
	t.Helper()
	for _, prefix := range []string{"", "/*é*/ "} {
		d := want
		if prefix != "" {
			position := 0
			for _, digit := range want.Position {
				position = position*10 + int(digit-'0')
			}
			d.Position = fixture.Position(position + utf8.RuneCountInString(prefix))
		}
		for _, queryMode := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(prefix+query+"/"+map[bool]string{false: "exec", true: "query"}[queryMode]+"/"+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					s := fixture.ImplicitError(prefix+query, d)
					s.SelectRows, s.Prepared, s.ErrorStage = queryMode, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, conn, recorder, s)
					if unchanged != nil {
						fixture.RunStep(t, p.Ctx, conn, recorder, *unchanged)
					}
				})
			}
		}
	}
}

func TestReferenceNumericSumReviewInsertValues(t *testing.T) {
	numericSumReviewScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE sum_review_insert(i INTEGER)", 0))
		run(fixture.Exec("INSERT INTO sum_review_insert VALUES(17)", 1))
		unchanged := fixture.Select("SELECT i FROM sum_review_insert", []string{"i"}, []string{"INT4"}, [][]any{{int64(17)}})
		missing := "No function matches the given name and argument types. You might need to add explicit type casts."
		ambiguous := "Could not choose a best candidate function. You might need to add explicit type casts."
		for _, c := range []struct{ expression, code, message, hint, token string }{
			{"sum(TRUE)", "42883", "function sum(boolean) does not exist", missing, "sum(TRUE)"},
			{"sum()", "42883", "function sum() does not exist", missing, "sum()"},
			{"sum(NULL)", "42725", "function sum(unknown) is not unique", ambiguous, "sum(NULL)"},
			{"public.sum(1)", "42883", "function public.sum(integer) does not exist", missing, "public.sum"},
			{"sum('oops'::numeric)", "22P02", "invalid input syntax for type numeric: \"oops\"", "", "'oops'"},
			{"public.sum('oops'::numeric)", "22P02", "invalid input syntax for type numeric: \"oops\"", "", "'oops'"},
			{"sum(absent,TRUE)", "42703", "column \"absent\" does not exist", "", "absent"},
			{"sum(TRUE) FILTER(WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "", "1)"},
			{"sum(1)", "42803", "aggregate functions are not allowed in VALUES", "", "sum(1)"},
		} {
			// A valid first row must not be installed when the later row fails analysis.
			query := "INSERT INTO sum_review_insert(i) VALUES(99),(" + c.expression + ")"
			want := fixture.Diagnostic{Code: c.code, Message: c.message, Hint: c.hint, Position: fixture.Position(strings.Index(query, c.token) + 1)}
			numericSumReviewDiagnostic(t, p, conn, recorder, query, want, &unchanged)
		}
		for _, c := range []struct {
			expression, code, message, hint, detail, token string
			tokenOffset                                    int
		}{
			{"CAST(sum(TRUE) FILTER(WHERE 1) AS INTEGER)", "42804", "argument of FILTER must be type boolean, not type integer", "", "", "1)", 0},
			{"CAST(CAST(sum(TRUE) FILTER(WHERE 1) AS INTEGER) AS INTEGER)", "42804", "argument of FILTER must be type boolean, not type integer", "", "", "1)", 0},
			{"sum(i)", "42703", "column \"i\" does not exist", "", "There is a column named \"i\" in table \"sum_review_insert\", but it cannot be referenced from this part of the query.", "sum(i)", 4},
			{"TRUE", "42804", "column \"i\" is of type integer but expression is of type boolean", "You will need to rewrite or cast the expression.", "", "TRUE", 0},
		} {
			query := "INSERT INTO sum_review_insert(i) VALUES(99),(" + c.expression + ")"
			want := fixture.Diagnostic{Code: c.code, Message: c.message, Hint: c.hint, Detail: c.detail, Position: fixture.Position(strings.Index(query, c.token) + c.tokenOffset + 1)}
			numericSumReviewDiagnostic(t, p, conn, recorder, query, want, &unchanged)
		}
	})
}

func TestReferenceNumericSumReviewDomains(t *testing.T) {
	numericSumReviewScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE DOMAIN sum_review_bool AS BOOLEAN", 0))
		run(fixture.Exec("CREATE DOMAIN sum_review_numeric AS NUMERIC", 0))
		run(fixture.Exec("CREATE TABLE sum_review_domains(n sum_review_numeric)", 0))
		run(fixture.Exec("INSERT INTO sum_review_domains VALUES(1.20),(NULL),(3.456)", 3))
		// A numeric domain uses the existing base-type overload, preserving scale
		// and promoted NUMERIC metadata rather than exposing the domain descriptor.
		s := fixture.Select("SELECT sum(n) FROM public.sum_review_domains", []string{"sum"}, []string{"NUMERIC"}, [][]any{{"4.656"}})
		s.CheckMetadata = numericSumMetadata(pg, s.Types)
		numericSumBoth(t, p, conn, recorder, s)
		missing := "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, path := range []string{"public", "pg_catalog"} {
			run(fixture.Exec("SET search_path TO "+path, 0))
			for _, qualifier := range []string{"", "public."} {
				if path == "pg_catalog" && qualifier == "" {
					continue // A hidden input domain must be explicitly qualified.
				}
				diagnosticQualifier := ""
				if path == "pg_catalog" {
					diagnosticQualifier = "public."
				}
				for _, c := range []struct{ expression, signature string }{
					{"NULL::" + qualifier + "sum_review_bool", diagnosticQualifier + "sum_review_bool"},
					{"NULL::" + qualifier + "sum_review_numeric,1", diagnosticQualifier + "sum_review_numeric, integer"},
				} {
					query := "SELECT sum(" + c.expression + ")"
					numericSumReviewDiagnostic(t, p, conn, recorder, query, fixture.Diagnostic{Code: "42883", Message: "function sum(" + c.signature + ") does not exist", Hint: missing, Position: "8"}, nil)
				}
			}
			// Success is unchanged when the domain itself is hidden but the table
			// is qualified; overload selection is based on its numeric base type.
			numericSumBoth(t, p, conn, recorder, s)
		}
		run(fixture.Exec("SET search_path TO public", 0))
	})
}

func TestReferenceNumericSumReviewOrderDiagnostics(t *testing.T) {
	numericSumReviewScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		misplaced := "No aggregate function matches the given name and argument types. Perhaps you misplaced ORDER BY; ORDER BY must appear after all regular arguments of the aggregate."
		missing := "No function matches the given name and argument types. You might need to add explicit type casts."
		ambiguous := "Could not choose a best candidate function. You might need to add explicit type casts."
		for _, c := range []struct{ query, code, message, hint, token string }{
			{"SELECT sum(1,2 ORDER BY 1,2)", "42883", "function sum(integer, integer) does not exist", misplaced, "sum"},
			{"SELECT public.sum(1,2 ORDER BY 1,2)", "42883", "function public.sum(integer, integer) does not exist", misplaced, "public"},
			{"SELECT sum(TRUE ORDER BY 1,2)", "42883", "function sum(boolean) does not exist", misplaced, "sum"},
			{"SELECT sum(1,2 ORDER BY 1)", "42883", "function sum(integer, integer) does not exist", missing, "sum"},
			{"SELECT sum(NULL ORDER BY 1,2)", "42725", "function sum(unknown) is not unique", ambiguous, "sum"},
			{"SELECT sum('oops'::numeric,2 ORDER BY 1,2)", "22P02", "invalid input syntax for type numeric: \"oops\"", "", "'oops'"},
			{"SELECT public.sum(absent,2 ORDER BY 1,2)", "42703", "column \"absent\" does not exist", "", "absent"},
			{"SELECT sum(1,2 ORDER BY 1,2) FILTER(WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "", "1)"},
			{"SELECT public.sum(1,2 ORDER BY 1,2) FILTER(WHERE absent)", "42703", "column \"absent\" does not exist", "", "absent"},
		} {
			want := fixture.Diagnostic{Code: c.code, Message: c.message, Hint: c.hint, Position: fixture.Position(strings.Index(c.query, c.token) + 1)}
			numericSumReviewDiagnostic(t, p, conn, recorder, c.query, want, nil)
		}
		// Multiple ordering keys remain valid for a resolved unary signature;
		// they do not force a missing-signature error or change its result type.
		numericSumBoth(t, p, conn, recorder, fixture.Select("SELECT sum(1 ORDER BY 1,2)", []string{"sum"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}
