package reference_test

import (
	"strings"
	"testing"
)

func queryWindowReview4Failure(q, code, message, marker string) referenceStep {
	position := len([]rune(q[:strings.LastIndex(q, marker)])) + 1
	return referenceImplicitError(q, referenceDiagnostic{Code: code, Message: message, Position: referencePosition(position)})
}

// Every ordinary parent below has a real callable for its completed child.
// Its own aggregate-argument prohibition must precede the containing window's
// later nesting/signature/capability analysis, including the excluded SUM path.
func TestReferenceQueryWindowReview4NearestAggregate(t *testing.T) {
	for _, tc := range []struct{ name, args string }{
		{"count", "$window"}, {"sum", "$window"}, {"avg", "$window"},
		{"min", "$window"}, {"max", "$window"}, {"any_value", "$window"},
		{"bool_and", "$window>0"}, {"bool_or", "$window>0"}, {"every", "$window>0"},
		{"string_agg", "CAST($window AS TEXT),','"},
	} {
		for _, outer := range []string{"count", "sum", "row_number"} {
			for _, inner := range []string{"row_number()", "count(*)"} {
				for _, qualification := range []string{"", "pg_catalog."} {
					window := qualification + inner + " OVER ()"
					ordinary := qualification + tc.name + "(" + strings.ReplaceAll(tc.args, "$window", window) + ")"
					for _, prefix := range []string{"", "/*é*/ "} {
						t.Run(prefix+outer+"/"+ordinary, func(t *testing.T) {
							queryWindowReviewScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
								queryWindowRun(t, p, b, referenceExec("CREATE TABLE audit(v INT)", 0))
								expr := qualification + outer + "(" + ordinary + ") OVER ()"
								marker := qualification + inner
								q := prefix + "SELECT " + expr
								s := queryWindowReview4Failure(q, "42803", "aggregate function calls cannot contain window function calls", marker)
								s.selectRows = true
								queryWindowRoutes(t, p, b, s)
								insert := prefix + "INSERT INTO audit SELECT " + expr
								queryWindowRoutes(t, p, b, queryWindowReview4Failure(insert, "42803", "aggregate function calls cannot contain window function calls", marker))
								queryWindowRoutes(t, p, b, referenceSelect("SELECT v FROM audit", []string{"v"}, []string{"INT4"}, nil))
							})
						})
					}
				}
			}
		}
	}
}

func TestReferenceQueryWindowReview4AuthorityContrasts(t *testing.T) {
	for _, tc := range []struct{ q, code, message, marker string }{
		{"SELECT count(row_number() OVER ()) OVER ()", "42P20", "window function calls cannot be nested", "row_number"},
		{"SELECT count(row_number() OVER ())", "42803", "aggregate function calls cannot contain window function calls", "row_number"},
		// Nearest window wins here, despite the surrounding ordinary aggregate.
		{"SELECT count(count(row_number() OVER ()) OVER ())", "42P20", "window function calls cannot be nested", "row_number"},
		{"SELECT count(count(sum(row_number() OVER ())) OVER ())", "42803", "aggregate function calls cannot contain window function calls", "row_number"},
		{"SELECT count(count(*) FILTER (WHERE row_number() OVER ()>0)) OVER ()", "42P20", "window functions are not allowed in FILTER", "row_number"},
		{"SELECT count((SELECT count(*) OVER (ORDER BY row_number() OVER ()))) OVER ()", "42P20", "window functions are not allowed in window definitions", "row_number"},
	} {
		for _, prefix := range []string{"", "/*é*/ "} {
			t.Run(prefix+tc.q, func(t *testing.T) {
				queryWindowReviewScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					s := queryWindowReview4Failure(prefix+tc.q, tc.code, tc.message, tc.marker)
					s.selectRows = true
					queryWindowRoutes(t, p, b, s)
				})
			})
		}
	}
}

func TestReferenceQueryWindowReview4LexicalBoundaries(t *testing.T) {
	for _, prefix := range []string{"", "/*é*/ "} {
		t.Run(prefix, func(t *testing.T) {
			queryWindowReviewScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				for _, q := range []string{
					"SELECT count((SELECT row_number() OVER ())) AS n, row_number() OVER () AS rn",
					"SELECT row_number() OVER () AS rn,(SELECT count((SELECT row_number() OVER ()))) AS n",
				} {
					names := []string{"n", "rn"}
					if strings.HasPrefix(q, "SELECT row_number") {
						names = []string{"rn", "n"}
					}
					queryWindowRoutes(t, p, b, referenceSelect(prefix+q, names, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
				}
				// A child query may contain windows even inside an outer window argument;
				// native still excludes count(expr), rather than inventing a nesting error.
				q := prefix + "SELECT count((SELECT row_number() OVER ())) OVER () AS n"
				s := referenceSelect(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
				if !b.pg {
					s = queryWindowReview4Failure(q, "0A000", "window function provider is unsupported", "count")
					s.selectRows = true
				}
				queryWindowRoutes(t, p, b, s)
				// Pinned PG resolves both invalid outer callables before same-query
				// nesting analysis: both arguments are bigint.
				for _, suffix := range []string{"", " OVER ()"} {
					q = prefix + "SELECT count((SELECT row_number() OVER ()),row_number() OVER ())" + suffix
					s = referenceImplicitError(q, referenceDiagnostic{Code: "42883", Message: "function count(bigint, bigint) does not exist", Hint: queryWindowReviewHint, Position: referencePosition(len([]rune(prefix)) + 8)})
					s.selectRows = true
					queryWindowRoutes(t, p, b, s)
				}
				for _, tc := range []struct{ q, code, message string }{
					{"SELECT count((SELECT count(row_number() OVER ()))) OVER ()", "42803", "aggregate function calls cannot contain window function calls"},
					{"SELECT count((SELECT count(row_number() OVER ()) OVER ()))", "42P20", "window function calls cannot be nested"},
				} {
					s := queryWindowReview4Failure(prefix+tc.q, tc.code, tc.message, "row_number")
					s.selectRows = true
					queryWindowRoutes(t, p, b, s)
				}
			})
		})
	}
}
