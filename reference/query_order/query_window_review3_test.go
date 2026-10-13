package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations follow the released callable overloads, not native capability
// errors. Each admitted invalid signature must precede window shape and clause
// restrictions; raw call-local ORDER keys must remain untransformed.
func TestReferenceQueryWindowReview3Signatures(t *testing.T) {
	for _, tc := range []struct {
		name, args, types string
	}{
		{"bool_and", "1", "integer"},
		{"bool_or", "1", "integer"},
		{"every", "1", "integer"},
		{"avg", "true", "boolean"},
		{"min", "true", "boolean"},
		{"max", "true", "boolean"},
		{"string_agg", "1,2", "integer, integer"},
		{"any_value", "1,2", "integer, integer"},
		{"count", "1,2", "integer, integer"},
		{"row_number", "1", "integer"},
		{"sum", "true", "boolean"},
		{"sum", "1,2", "integer, integer"},
		{"sum", "", ""},
		{"bool_and", "true,false", "boolean, boolean"},
		{"bool_or", "true,false", "boolean, boolean"},
		{"every", "true,false", "boolean, boolean"},
		{"avg", "1,2", "integer, integer"},
		{"min", "1,2", "integer, integer"},
		{"max", "1,2", "integer, integer"},
		{"string_agg", "'x'", "unknown"},
		{"any_value", "", ""},
		{"bool_and", "", ""},
		{"bool_or", "", ""},
		{"every", "", ""},
		{"avg", "", ""},
		{"min", "", ""},
		{"max", "", ""},
		{"string_agg", "", ""},
	} {
		for _, qualification := range []string{"", "pg_catalog."} {
			name := qualification + tc.name
			call := name + "(" + tc.args + ") OVER ()"
			queries := []string{"SELECT " + call, "SELECT 1 WHERE " + call + " IS NULL", "SELECT 1 HAVING " + call + " IS NULL"}
			if tc.args != "" {
				queries = append(queries, "SELECT "+name+"(DISTINCT "+tc.args+") OVER ()", "SELECT "+name+"("+tc.args+" ORDER BY missing_key) OVER ()")
			}
			for _, sql := range queries {
				for _, prefix := range []string{"", "/*é*/ "} {
					t.Run(prefix+sql, func(t *testing.T) {
						queryWindowReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
							q := prefix + sql
							d := fixture.Diagnostic{Code: "42883", Message: "function " + name + "(" + tc.types + ") does not exist", Hint: queryWindowReviewHint, Position: fixture.Position(len([]rune(q[:strings.Index(q, name)])) + 1)}
							s := fixture.ImplicitError(q, d)
							s.SelectRows = true
							queryWindowRoutes(t, p, b, s)
						})
					})
				}
			}
		}
	}
}

func TestReferenceQueryWindowReview3UnknownResolution(t *testing.T) {
	for _, tc := range []struct{ name, code, message, hint string }{
		{"avg", "42725", "function %s(unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{"sum", "42725", "function %s(unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts."},
		{"any_value", "42804", "could not determine polymorphic type because input has type unknown", ""},
	} {
		for _, qualification := range []string{"", "pg_catalog."} {
			name := qualification + tc.name
			for _, sql := range []string{"SELECT " + name + "(NULL) OVER ()", "SELECT " + name + "(DISTINCT NULL) OVER ()", "SELECT " + name + "(NULL ORDER BY missing_key) OVER ()", "SELECT 1 WHERE " + name + "(NULL) OVER () IS NULL"} {
				for _, prefix := range []string{"", "/*é*/ "} {
					t.Run(prefix+sql, func(t *testing.T) {
						queryWindowReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
							q := prefix + sql
							d := fixture.Diagnostic{Code: tc.code, Message: strings.ReplaceAll(tc.message, "%s", name), Hint: tc.hint}
							if tc.code == "42725" {
								d.Position = fixture.Position(len([]rune(q[:strings.Index(q, name)])) + 1)
							}
							s := fixture.ImplicitError(q, d)
							s.SelectRows = true
							queryWindowRoutes(t, p, b, s)
						})
					})
				}
			}
		}
	}
}

func TestReferenceQueryWindowReview3FilterPrecedence(t *testing.T) {
	for _, call := range []string{"bool_and(1)", "bool_or(1)", "every(1)", "avg(true)", "min(true)", "max(true)", "string_agg(1,2)", "any_value(1,2)", "count(1,2)", "row_number(1)", "sum(true)"} {
		for _, prefix := range []string{"", "/*é*/ "} {
			t.Run(prefix+call, func(t *testing.T) {
				queryWindowReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					for _, filter := range []struct{ expr, code, message, marker string }{
						{"missing_filter", "42703", `column "missing_filter" does not exist`, "missing_filter"},
						{"1", "42804", "argument of FILTER must be type boolean, not type integer", "1) OVER"},
					} {
						q := prefix + "SELECT " + call + " FILTER (WHERE " + filter.expr + ") OVER ()"
						s := queryWindowReview2Diagnostic(q, filter.code, filter.message, filter.marker)
						s.SelectRows = true
						queryWindowRoutes(t, p, b, s)
					}
				})
			})
		}
	}
}

func TestReferenceQueryWindowReview3ArgumentLimit(t *testing.T) {
	args := strings.TrimSuffix(strings.Repeat("1,", 101), ",")
	for _, name := range []string{"bool_and", "bool_or", "every", "avg", "min", "max", "string_agg", "any_value", "count", "row_number", "sum"} {
		t.Run(name, func(t *testing.T) {
			queryWindowReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				q := "SELECT " + name + "(" + args + ") OVER ()"
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"})
				s.SelectRows = true
				queryWindowRoutes(t, p, b, s)
			})
		})
	}
}

func TestReferenceQueryWindowReview3ValidExclusions(t *testing.T) {
	for _, tc := range []struct {
		call, typ string
		value     any
	}{
		{"bool_and(true)", "BOOL", true},
		{"bool_or(false)", "BOOL", false},
		{"every(true)", "BOOL", true},
		{"avg(CAST(1 AS FLOAT8))", "FLOAT8", float64(1)},
		{"min(1)", "INT4", int64(1)},
		{"max(1)", "INT4", int64(1)},
		{"string_agg('x','|')", "TEXT", "x"},
		{"any_value(1)", "INT4", int64(1)},
		{"sum(1)", "INT8", int64(1)},
	} {
		t.Run(tc.call, func(t *testing.T) {
			queryWindowReviewScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				q := "SELECT " + tc.call + " OVER () AS n"
				s := fixture.Select(q, []string{"n"}, []string{tc.typ}, [][]any{{tc.value}})
				if !b.PG && tc.call != "sum(1)" {
					s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "window function provider is unsupported", Position: "8"})
					s.SelectRows = true
				}
				queryWindowRoutes(t, p, b, s)
			})
		})
	}
}
