package reference_test

import (
	"strings"
	"testing"
)

func queryWindowReviewScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		body(t, p, b)
	}
}

const queryWindowReviewHint = "No function matches the given name and argument types. You might need to add explicit type casts."

func TestReferenceQueryWindowReviewPrecedence(t *testing.T) {
	for _, tc := range []struct{ name, q, code, message, marker, hint string }{
		{"count_arity", "SELECT count(1,2) OVER ()", "42883", "function count(integer, integer) does not exist", "count", queryWindowReviewHint},
		{"where_signature", "SELECT 1 WHERE row_number(1) OVER () > 0", "42883", "function row_number(integer) does not exist", "row_number", queryWindowReviewHint},
		{"distinct_filter_child", "SELECT count(DISTINCT 1) FILTER (WHERE missing) OVER ()", "42703", `column "missing" does not exist`, "missing", ""},
		{"filter_child", "SELECT count(*) FILTER (WHERE missing) OVER ()", "42703", `column "missing" does not exist`, "missing", ""},
		{"filter_window", "SELECT count(*) FILTER (WHERE row_number() OVER ()>0) OVER ()", "42P20", "window functions are not allowed in FILTER", "row_number", ""},
		{"argument_before_filter", "SELECT count(missing_arg) FILTER (WHERE missing_filter) OVER ()", "42703", `column "missing_arg" does not exist`, "missing_arg", ""},
	} {
		for _, prefix := range []string{"", "/*é*/ "} {
			t.Run(tc.name+prefix, func(t *testing.T) {
				queryWindowReviewScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					q := prefix + tc.q
					s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: referencePosition(strings.Index(tc.q, tc.marker) + 1 + len([]rune(prefix))), Hint: tc.hint})
					s.selectRows = true
					queryWindowRoutes(t, p, b, s)
				})
			})
		}
	}
}

func TestReferenceQueryWindowReviewQualifiedDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, q, message string }{
		{"missing_over", "SELECT pg_catalog.row_number()", "window function pg_catalog.row_number requires an OVER clause"},
		{"parameterless_count", "SELECT pg_catalog.count() OVER ()", "pg_catalog.count(*) must be used to call a parameterless aggregate function"},
		{"scalar_over", "SELECT pg_catalog.num_nulls(1) OVER ()", "OVER specified, but pg_catalog.num_nulls is not a window function nor an aggregate function"},
	} {
		for _, prefix := range []string{"", "/*é*/ "} {
			t.Run(tc.name+prefix, func(t *testing.T) {
				queryWindowReviewScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					s := referenceImplicitError(prefix+tc.q, referenceDiagnostic{Code: "42809", Message: tc.message, Position: referencePosition(8 + len([]rune(prefix)))})
					s.selectRows = true
					queryWindowRoutes(t, p, b, s)
				})
			})
		}
	}
}

func TestReferenceQueryWindowReviewKeywordTypes(t *testing.T) {
	for _, word := range []string{"over", "partition", "rows", "range", "groups", "preceding", "following", "unbounded", "current"} {
		for _, kind := range []string{"enum", "domain", "schema"} {
			t.Run(kind+"/"+word, func(t *testing.T) {
				queryWindowReviewScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					switch kind {
					case "enum":
						queryWindowRun(t, p, b, referenceExec("CREATE TYPE "+word+" AS ENUM('x')", 0))
						queryWindowRun(t, p, b, referenceExec("CREATE TABLE keyword_t(v "+word+")", 0))
						queryWindowRun(t, p, b, referenceExec("INSERT INTO keyword_t VALUES('x')", 1))
						queryWindowRoutes(t, p, b, referenceSelect("SELECT v::TEXT AS v FROM keyword_t", []string{"v"}, []string{"TEXT"}, [][]any{{"x"}}))
					case "domain":
						queryWindowRun(t, p, b, referenceExec("CREATE DOMAIN "+word+" AS INT", 0))
						queryWindowRun(t, p, b, referenceExec("CREATE TABLE keyword_t(v "+word+")", 0))
						queryWindowRun(t, p, b, referenceExec("INSERT INTO keyword_t VALUES(7)", 1))
						queryWindowRoutes(t, p, b, referenceSelect("SELECT v::INT AS v FROM keyword_t", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					case "schema":
						queryWindowRun(t, p, b, referenceExec("CREATE SCHEMA "+word, 0))
						if b.pg {
							t.Cleanup(func() {
								if _, err := b.db.ExecContext(p.ctx, "DROP SCHEMA "+word+" CASCADE"); err != nil {
									t.Error(err)
								}
							})
						}
						queryWindowRun(t, p, b, referenceExec("CREATE TYPE "+word+".partition AS ENUM('x')", 0))
						queryWindowRun(t, p, b, referenceExec("CREATE DOMAIN "+word+".wrapped AS "+word+".partition", 0))
						queryWindowRun(t, p, b, referenceExec("CREATE TABLE keyword_t(v "+word+".wrapped)", 0))
						queryWindowRun(t, p, b, referenceExec("INSERT INTO keyword_t VALUES('x')", 1))
						queryWindowRoutes(t, p, b, referenceSelect("SELECT v::TEXT AS v FROM keyword_t", []string{"v"}, []string{"TEXT"}, [][]any{{"x"}}))
					}
				})
			})
		}
	}
}
