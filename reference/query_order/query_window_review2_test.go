package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func queryWindowReview2Diagnostic(q, code, message, marker string) fixture.Step {
	position := len([]rune(q[:strings.LastIndex(q, marker)])) + 1
	return fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Position: fixture.Position(position)})
}

func TestReferenceQueryWindowReview2OrderShape(t *testing.T) {
	for _, tc := range []struct{ name, call, message string }{
		{"missing_order_child", "count(1 ORDER BY missing)", "aggregate ORDER BY is not implemented for window functions"},
		{"invalid_order_call", "count(1 ORDER BY row_number(1))", "aggregate ORDER BY is not implemented for window functions"},
		{"nested_order_window", "count(1 ORDER BY row_number() OVER ())", "aggregate ORDER BY is not implemented for window functions"},
		{"distinct_before_order_child", "count(DISTINCT 1 ORDER BY missing)", "DISTINCT is not implemented for window functions"},
	} {
		for _, prefix := range []string{"", "/*é*/ "} {
			t.Run(tc.name+prefix, func(t *testing.T) {
				queryWindowReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE audit(v INT)", 0))
					q := prefix + "SELECT " + tc.call + " OVER ()"
					failure := queryWindowReview2Diagnostic(q, "0A000", tc.message, "count")
					failure.SelectRows = true
					queryWindowRoutes(t, p, b, failure)
					q = prefix + "INSERT INTO audit SELECT " + tc.call + " OVER ()"
					queryWindowRoutes(t, p, b, queryWindowReview2Diagnostic(q, "0A000", tc.message, "count"))
					queryWindowRoutes(t, p, b, fixture.Select("SELECT v FROM audit", []string{"v"}, []string{"INT4"}, nil))
				})
			})
		}
	}
}

func TestReferenceQueryWindowReview2MissingOverFilter(t *testing.T) {
	for _, tc := range []struct{ name, call, code, message, marker string }{
		{"missing_column", "row_number() FILTER (WHERE missing)", "42703", `column "missing" does not exist`, "missing"},
		{"boolean_type", "row_number() FILTER (WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "1"},
		{"nested_window", "row_number() FILTER (WHERE row_number() OVER ()>0)", "42P20", "window functions are not allowed in FILTER", "row_number"},
	} {
		for _, prefix := range []string{"", "/*é*/ "} {
			t.Run(tc.name+prefix, func(t *testing.T) {
				queryWindowReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE audit(v INT)", 0))
					q := prefix + "SELECT " + tc.call
					failure := queryWindowReview2Diagnostic(q, tc.code, tc.message, tc.marker)
					failure.SelectRows = true
					queryWindowRoutes(t, p, b, failure)
					q = prefix + "INSERT INTO audit SELECT " + tc.call
					queryWindowRoutes(t, p, b, queryWindowReview2Diagnostic(q, tc.code, tc.message, tc.marker))
					queryWindowRoutes(t, p, b, fixture.Select("SELECT v FROM audit", []string{"v"}, []string{"INT4"}, nil))
				})
			})
		}
	}
}

func TestReferenceQueryWindowReview2RepeatedOver(t *testing.T) {
	for _, callee := range []string{"row_number()", "num_nulls(1)"} {
		for _, shape := range []struct{ name, first, second string }{{"parenthesized", "()", "()"}, {"named", "first", "second"}} {
			for _, token := range []string{"OVER", "over", "OvEr"} {
				for _, prefix := range []string{"", "/*é*/ "} {
					t.Run(callee+"/"+shape.name+"/"+token+prefix, func(t *testing.T) {
						queryWindowReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
							queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE audit(v INT)", 0))
							malformed := "SELECT " + callee + " OVER " + shape.first + " " + token + " " + shape.second
							q := prefix + malformed
							failure := queryWindowReview2Diagnostic(q, "42601", `syntax error at or near "`+token+`"`, token)
							failure.SelectRows = true
							queryWindowRoutes(t, p, b, failure)
							q = prefix + "INSERT INTO audit VALUES(99);" + malformed
							queryWindowRoutes(t, p, b, queryWindowReview2Diagnostic(q, "42601", `syntax error at or near "`+token+`"`, token))
							queryWindowRoutes(t, p, b, fixture.Select("SELECT v FROM audit", []string{"v"}, []string{"INT4"}, nil))
						})
					})
				}
			}
		}
	}
}
