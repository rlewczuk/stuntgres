package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAlterIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_identity_owner"})
	fixture.SchemaReferenceCleanup(t, "ra_identity")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE SCHEMA ra_identity", "CREATE ROLE ra_identity_owner",
			"GRANT USAGE,CREATE ON SCHEMA ra_identity TO ra_identity_owner",
			"CREATE FUNCTION ra_pick(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$",
			"CREATE FUNCTION ra_pick(text) RETURNS bigint LANGUAGE SQL AS $$SELECT 104::bigint$$",
			"CREATE PROCEDURE ra_proc(IN x integer) LANGUAGE SQL AS $$SELECT x$$")
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'ra_pick(integer)'::regprocedure::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		procOID := routineAlterOID(t, p, c, "ra_proc")
		routineExec(t, p, b, c,
			"ALTER FUNCTION ra_pick(IN ignored int) RENAME TO ra_new",
			"ALTER ROUTINE ra_new(integer) SET SCHEMA ra_identity",
			"ALTER FUNCTION ra_identity.ra_new(integer) OWNER TO ra_identity_owner",
			"ALTER PROCEDURE ra_proc(integer) RENAME TO ra_proc_new",
			"ALTER PROCEDURE ra_proc_new(integer) SET SCHEMA ra_identity",
			"ALTER ROUTINE ra_identity.ra_proc_new(integer) OWNER TO ra_identity_owner")
		routineAlterSameOID(t, p, c, "ra_new", oid)
		routineAlterSameOID(t, p, c, "ra_proc_new", procOID)
		routineValue(t, p, b, c, "SELECT ra_identity.ra_new(4) AS answer", "answer", "INT4", int64(5))
		routineValue(t, p, b, c, "SELECT ra_pick('x'::text) AS answer", "answer", "INT8", int64(104))
		routineAlterRows(t, p, b, c, "SELECT proname,prokind,proowner='ra_identity_owner'::regrole AS owned FROM pg_proc WHERE pronamespace='ra_identity'::regnamespace ORDER BY proname", []string{"proname", "prokind", "owned"}, []string{"NAME", "CHAR", "BOOL"}, [][]any{{"ra_new", "f", true}, {"ra_proc_new", "p", true}})
		routineAlterError(t, p, b, c, "ALTER FUNCTION ra_pick(integer) RENAME TO absent", "42883", "function ra_pick(integer) does not exist")
		d := fixture.ControlsReferenceDiagnostic("42809", "ra_identity.ra_proc_new(integer) is a procedure")
		d.Position, d.Hint = "8", "To call a procedure, use CALL."
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT ra_identity.ra_proc_new(4)", d)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	})
}

func TestReferenceRoutineAlterSignatures(t *testing.T) {
	for _, target := range []string{"ra_form(IN integer)", "ra_form(IN ignored int)", "ra_form(INOUT integer)", "ra_form(VARIADIC integer)", "ra_form(integer, OUT ignored ra_absent.missing)"} {
		t.Run(target, func(t *testing.T) {
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				routineExec(t, p, b, c, "CREATE FUNCTION ra_form(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
				routineAlterError(t, p, b, c, "ALTER FUNCTION ra_form() RENAME TO ra_selected", "42883", "function ra_form() does not exist")
				routineAlterError(t, p, b, c, "ALTER FUNCTION ra_form(bigint) RENAME TO ra_selected", "42883", "function ra_form(bigint) does not exist")
				routineExec(t, p, b, c, "ALTER FUNCTION "+target+" RENAME TO ra_selected")
				routineValue(t, p, b, c, "SELECT ra_selected(7) AS v", "v", "INT4", int64(7))
			})
		})
	}
	t.Run("masking domains quoted and missing", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "ra_early", "ra_late", "Ra Quote")
		routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			routineExec(t, p, b, c, "CREATE SCHEMA ra_early", "CREATE SCHEMA ra_late", `CREATE SCHEMA "Ra Quote"`,
				"CREATE DOMAIN ra_domain AS integer",
				"CREATE FUNCTION ra_early.ra_path(integer) RETURNS integer RETURN 1",
				"CREATE FUNCTION ra_late.ra_path(integer) RETURNS integer RETURN 2",
				"CREATE FUNCTION ra_late.ra_path(text) RETURNS integer RETURN 3",
				`CREATE FUNCTION "Ra Quote"."Mixed Name"(ra_domain) RETURNS integer RETURN 9`,
				"CREATE FUNCTION ra_default(integer,integer DEFAULT 2) RETURNS integer RETURN $1+$2",
				"CREATE FUNCTION ra_zero() RETURNS integer RETURN 8",
				"SET search_path TO ra_early,ra_late,public")
			d := fixture.ControlsReferenceDiagnostic("42725", `function name "ra_path" is not unique`)
			d.Hint = "Specify the argument list to select the function unambiguously."
			routineDropError(t, p, b, c, "ALTER FUNCTION ra_path RENAME TO ra_selected", d)
			routineExec(t, p, b, c, "ALTER FUNCTION ra_path(integer) RENAME TO ra_selected", `ALTER ROUTINE "Ra Quote"."Mixed Name"(ra_domain) RENAME TO "New Name"`, "ALTER FUNCTION ra_zero RENAME TO ra_zero_new")
			routineValue(t, p, b, c, "SELECT ra_selected(4) AS v", "v", "INT4", int64(1))
			routineValue(t, p, b, c, "SELECT ra_path(4) AS v", "v", "INT4", int64(2))
			routineValue(t, p, b, c, `SELECT "Ra Quote"."New Name"(4::ra_domain) AS v`, "v", "INT4", int64(9))
			routineValue(t, p, b, c, "SELECT ra_zero_new() AS v", "v", "INT4", int64(8))
			for _, tc := range []struct{ q, code, message string }{
				{"ALTER FUNCTION ra_default(integer) RENAME TO bad", "42883", "function ra_default(integer) does not exist"},
				{"ALTER FUNCTION ra_zero_new(ra_missing_type) RENAME TO bad", "42704", `type "ra_missing_type" does not exist`},
				{"ALTER FUNCTION ra_absent.f(integer) RENAME TO bad", "3F000", `schema "ra_absent" does not exist`},
				{"ALTER PROCEDURE ra_zero_new() RENAME TO bad", "42809", "ra_zero_new() is not a procedure"},
				{"ALTER ROUTINE ra_missing RENAME TO bad", "42883", `could not find a function named "ra_missing"`},
			} {
				routineAlterError(t, p, b, c, tc.q, tc.code, tc.message)
			}
		})
	})
}
