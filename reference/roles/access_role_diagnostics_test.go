package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAccessRoleDiagnostics(t *testing.T) {
	accessRoleCases(t, nil, func(s *membershipScenario) {
		hint := "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct{ q, signature, pos string }{
			{"SELECT pg_has_role()", "pg_has_role()", "8"},
			{"SELECT pg_has_role('x')", "pg_has_role(unknown)", "8"},
			{"SELECT pg_has_role('a','b','c','d')", "pg_has_role(unknown, unknown, unknown, unknown)", "8"},
			{"SELECT pg_has_role(true,'MEMBER')", "pg_has_role(boolean, unknown)", "8"},
			{"SELECT row_security_active()", "row_security_active()", "8"},
			{"SELECT row_security_active('x','y')", "row_security_active(unknown, unknown)", "8"},
			{"SELECT row_security_active(true)", "row_security_active(boolean)", "8"},
			{`SELECT "PG_HAS_ROLE"('x','MEMBER')`, `PG_HAS_ROLE(unknown, unknown)`, "8"},
			{"/* é */ SELECT pg_has_role(true,'MEMBER')", "pg_has_role(boolean, unknown)", "16"},
			{"SELECT pg_has_role(who => 'x', privilege => 'MEMBER')", "pg_has_role(who => unknown, privilege => unknown)", "8"},
			{"SELECT row_security_active(VARIADIC ARRAY[0::oid])", "row_security_active(oid[])", "8"},
		} {
			s.t.Run(tc.q, func(t *testing.T) {
				child := *s
				child.t = t
				for _, query := range []bool{false, true} {
					predefinedFailure(&child, tc.q, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: hint, Position: tc.pos}, query, "prepare")
				}
			})
		}
		for _, tc := range []struct {
			q string
			d fixture.Diagnostic
		}{
			{"SELECT missing_schema.pg_has_role('x','MEMBER')", fixture.Diagnostic{Code: "3F000", Message: `schema "missing_schema" does not exist`, Position: "8"}},
			{"SELECT pg_has_role(missing_column,'MEMBER')", fixture.Diagnostic{Code: "42703", Message: `column "missing_column" does not exist`, Position: "20"}},
			{"SELECT row_security_active('bad'::oid)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`, Position: "28"}},
			{"SELECT row_security_active('4294967296'::oid)", fixture.Diagnostic{Code: "22003", Message: `value "4294967296" is out of range for type oid`, Position: "28"}},
			{"SELECT pg_has_role(x => 'a', x => 'b')", fixture.Diagnostic{Code: "42601", Message: `argument name "x" used more than once`, Position: "30"}},
			{"SELECT pg_has_role(x => 'a', 'b')", fixture.Diagnostic{Code: "42601", Message: "positional argument cannot follow named argument", Position: "30"}},
		} {
			s.t.Run(tc.q, func(t *testing.T) {
				child := *s
				child.t = t
				for _, query := range []bool{false, true} {
					predefinedFailure(&child, tc.q, tc.d, query, "prepare")
				}
			})
		}
		accessRoleRuntimeError(s, "SELECT pg_has_role('missing','MEMBER')", "42704", `role "missing" does not exist`)
		// STABLE name lookup happens at execution, not during actual Prepare.
		held := predefinedHeld(s, "SELECT pg_has_role($1,'MEMBER') AS v")
		d := fixture.ControlsReferenceDiagnostic("42704", `role "missing" does not exist`)
		step := fixture.ErrorDiagnostic(held.SQL, d, "missing")
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, step)
		accessRoleHeldBool(s, held, true, s.bootstrap)
		if len(*s.notices) != 0 {
			s.t.Fatalf("unexpected inquiry notices: %#v", *s.notices)
		}
	})
}
