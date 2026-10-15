package reference_test

import (
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferenceAccessRoleDecorations(t *testing.T) {
	accessRoleCases(t, nil, func(s *membershipScenario) {
		for _, fn := range []struct {
			name, args, badArgs, badTypes string
			want                          bool
		}{
			{"pg_has_role", pq.QuoteLiteral(s.bootstrap) + ",'MEMBER'", "true,'MEMBER'", "boolean, unknown", true},
			{"row_security_active", "0::oid", "true", "boolean", false},
		} {
			ordinary := "SELECT " + fn.name + "(" + fn.args + ")"
			missingSignature := "function " + fn.name + "(" + fn.badTypes + ") does not exist"
			hint := "No function matches the given name and argument types. You might need to add explicit type casts."
			cases := []struct {
				q string
				d fixture.Diagnostic
			}{
				{"SELECT " + fn.name + "(DISTINCT " + fn.args + ")", fixture.Diagnostic{Code: "42809", Message: "DISTINCT specified, but " + fn.name + " is not an aggregate function", Position: "8"}},
				{"SELECT " + fn.name + "(" + fn.args + " ORDER BY 1)", fixture.Diagnostic{Code: "42809", Message: "ORDER BY specified, but " + fn.name + " is not an aggregate function", Position: "8"}},
				{ordinary + " FILTER (WHERE false)", fixture.Diagnostic{Code: "42809", Message: "FILTER specified, but " + fn.name + " is not an aggregate function", Position: "8"}},
				{"SELECT pg_catalog." + fn.name + "(DISTINCT " + fn.args + ")", fixture.Diagnostic{Code: "42809", Message: "DISTINCT specified, but pg_catalog." + fn.name + " is not an aggregate function", Position: "8"}},
				{"SELECT pg_catalog." + fn.name + "(" + fn.args + " ORDER BY 1)", fixture.Diagnostic{Code: "42809", Message: "ORDER BY specified, but pg_catalog." + fn.name + " is not an aggregate function", Position: "8"}},
				{"SELECT pg_catalog." + fn.name + "(" + fn.args + ") FILTER (WHERE false)", fixture.Diagnostic{Code: "42809", Message: "FILTER specified, but pg_catalog." + fn.name + " is not an aggregate function", Position: "8"}},
				// Scalar resolution rejects ORDER BY before transforming its child.
				{"SELECT " + fn.name + "(" + fn.args + " ORDER BY missing_order)", fixture.Diagnostic{Code: "42809", Message: "ORDER BY specified, but " + fn.name + " is not an aggregate function", Position: "8"}},
				{ordinary + " FILTER (WHERE missing_filter)", fixture.Diagnostic{Code: "42703", Message: `column "missing_filter" does not exist`}},
				{ordinary + " FILTER (WHERE 1)", fixture.Diagnostic{Code: "42804", Message: "argument of FILTER must be type boolean, not type integer"}},
				{"SELECT " + fn.name + "(DISTINCT missing_arg)", fixture.Diagnostic{Code: "42703", Message: `column "missing_arg" does not exist`}},
				{"SELECT " + fn.name + "(DISTINCT " + fn.badArgs + ")", fixture.Diagnostic{Code: "42883", Message: missingSignature, Hint: hint, Position: "8"}},
				{"SELECT " + fn.name + "(" + fn.badArgs + " ORDER BY missing_order)", fixture.Diagnostic{Code: "42883", Message: missingSignature, Hint: hint, Position: "8"}},
				{"SELECT " + fn.name + "(" + fn.badArgs + ") FILTER (WHERE missing_filter)", fixture.Diagnostic{Code: "42703", Message: `column "missing_filter" does not exist`}},
				{"SELECT " + fn.name + "(DISTINCT " + fn.args + " ORDER BY 1) FILTER (WHERE false)", fixture.Diagnostic{Code: "42809", Message: "DISTINCT specified, but " + fn.name + " is not an aggregate function", Position: "8"}},
			}
			for _, tc := range cases {
				// Positions refer to the explicitly named lexical occurrence, not output
				// observed from either backend. All other optional diagnostic fields empty.
				if tc.d.Code == "42703" {
					token := "missing_arg"
					if strings.Contains(tc.q, "missing_filter") {
						token = "missing_filter"
					}
					tc.d.Position = fixture.Position(strings.Index(tc.q, token) + 1)
				} else if tc.d.Code == "42804" {
					tc.d.Position = fixture.Position(strings.Index(tc.q, "WHERE 1") + len("WHERE ") + 1)
				}
				s.t.Run(tc.q, func(t *testing.T) {
					child := *s
					child.t = t
					for _, query := range []bool{false, true} {
						predefinedFailure(&child, tc.q, tc.d, query, "prepare")
					}
				})
			}
			// PostgreSQL forgets VARIADIC decoration on a matching fixed scalar
			// signature. Array VARIADIC remains covered by the missing-signature tests.
			q := "SELECT row_security_active(VARIADIC 0::oid) AS v"
			if fn.name == "pg_has_role" {
				q = "SELECT pg_has_role(" + pq.QuoteLiteral(s.bootstrap) + ",VARIADIC 'MEMBER'::text) AS v"
			}
			accessRoleBool(s, q, fn.want)
		}
	})
}
