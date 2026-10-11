package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

const datetimeJustifyHint = "No function matches the given name and argument types. You might need to add explicit type casts."

func TestReferenceDatetimeJustifyDiagnostics(t *testing.T) {
	datetimeJustifyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"justify_days", "justify_hours", "justify_interval"} {
			for _, tc := range []struct{ input, signature string }{
				{"", ""}, {"1", "integer"}, {"1.5", "numeric"}, {"'1 day'::text", "text"},
				{"INTERVAL '1 day', INTERVAL '2 days'", "interval, interval"}, {"TIMETZ '12:00+00'", "time with time zone"},
				{"span=>INTERVAL '1 day'", "span => interval"}, {"VARIADIC '{1 day}'::interval[]", "interval[]"},
			} {
				q := "SELECT " + f + "(" + tc.input + ")"
				t.Run(q, func(t *testing.T) {
					datetimeJustifyFailure(t, p, b, c, q, "42883", "function "+f+"("+tc.signature+") does not exist", datetimeJustifyHint, 8)
				})
			}
			datetimeJustifyFailure(t, p, b, c, "SELECT /*é*/ "+f+"(1)", "42883", "function "+f+"(integer) does not exist", datetimeJustifyHint, 14)
			datetimeJustifyFailure(t, p, b, c, "SELECT pg_catalog."+f+"(1)", "42883", "function pg_catalog."+f+"(integer) does not exist", datetimeJustifyHint, 8)
			upper := strings.ToUpper(f)
			datetimeJustifyFailure(t, p, b, c, "SELECT \""+upper+"\"(INTERVAL '0')", "42883", "function "+upper+"(interval) does not exist", datetimeJustifyHint, 8)
			datetimeJustifyFailure(t, p, b, c, "SELECT hidden_justify."+f+"(INTERVAL '0')", "3F000", `schema "hidden_justify" does not exist`, "", 8)
			for _, prefix := range []string{"pg_catalog.", "\""} {
				name := prefix + f
				if prefix == "\"" {
					name += "\""
				}
				datetimeJustifyValue(t, p, b, c, name+"('0')", "00:00:00")
			}
			q := "SELECT " + f + "(" + strings.TrimSuffix(strings.Repeat("1,", 101), ",") + ")"
			datetimeJustifyFailure(t, p, b, c, q, "54023", "cannot pass more than 100 arguments to a function", "", 8)
			q = "SELECT " + f + "(" + strings.Repeat("1,", 100) + "missing_column)"
			datetimeJustifyFailure(t, p, b, c, q, "42703", `column "missing_column" does not exist`, "", len("SELECT "+f+"(")+201)
			q = "SELECT " + f + "(INTERVAL 'invalid',1)"
			datetimeJustifyFailure(t, p, b, c, q, "22007", `invalid input syntax for type interval: "invalid"`, "", len("SELECT "+f+"(INTERVAL ")+1)
			for _, tc := range []struct{ input, code, message string }{
				{"INTERVAL 'invalid'", "22007", `invalid input syntax for type interval: "invalid"`},
				{"'invalid'", "22007", `invalid input syntax for type interval: "invalid"`},
				{"INTERVAL '2147483648 days'", "22015", `interval field value out of range: "2147483648 days"`},
			} {
				q = "SELECT " + f + "(" + tc.input + ")"
				datetimeJustifyFailure(t, p, b, c, q, tc.code, tc.message, "", strings.Index(q, "'")+1)
			}
			if b.PG {
				want := "1 day"
				if f == "justify_days" {
					want = "24:00:00"
				}
				datetimeJustifyValue(t, p, b, c, f+"(TIME '24:00')", want)
			} else {
				datetimeJustifyFailure(t, p, b, c, "SELECT "+f+"(TIME '24:00')", "0A000", "time to interval conversion is unsupported", "", 8)
			}
		}
	})
}
