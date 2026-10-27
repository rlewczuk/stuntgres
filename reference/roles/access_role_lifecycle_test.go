package reference_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAccessRoleLifecycle(t *testing.T) {
	fixture.AccessRoleCases(t, []string{"ar_actor", "ar_middle", "ar_target", "ar_recreated"}, func(s *fixture.RoleMembershipScenario) {
		fixture.AccessRoleGraph(s)
		fixture.AccessRoleTable(s)
		held := predefinedHeld(s, "SELECT pg_has_role('ar_actor','ar_target',$1) AS v")
		fixture.AccessRoleHeldBool(s, held, false, "USAGE")
		s.Exec("GRANT ar_middle TO ar_actor WITH INHERIT TRUE")
		fixture.AccessRoleHeldBool(s, held, true, "USAGE")
		fixture.AccessRoleHeldBool(s, held, false, "SET")
		s.Exec("GRANT ar_target TO ar_middle WITH SET TRUE")
		fixture.AccessRoleHeldBool(s, held, true, "SET")
		fixture.AccessRoleHeldBool(s, held, true, "MEMBER")
		// Held inferred NAME parameters resolve the live name on each execution.
		names := predefinedHeld(s, "SELECT pg_has_role($1,'MEMBER') AS v")
		old := fixture.AccessRoleOID(s, "ar_recreated")
		fixture.AccessRoleHeldBool(s, names, true, "ar_recreated")
		s.Exec("DROP ROLE ar_recreated")
		d := fixture.ControlsReferenceDiagnostic("42704", `role "ar_recreated" does not exist`)
		failure := fixture.ErrorDiagnostic(names.SQL, d, "ar_recreated")
		failure.SelectRows = true
		fixture.RunStep(s.T, s.P.Ctx, names, s.Record, failure)
		s.Exec("CREATE ROLE ar_recreated LOGIN")
		if fresh := fixture.AccessRoleOID(s, "ar_recreated"); fresh == old {
			s.T.Fatal("recreated role reused old identity")
		}
		fixture.AccessRoleHeldBool(s, names, true, "ar_recreated")
		fixture.AccessRoleBool(s, "SELECT pg_has_role('ar_actor'::name,$1::oid,'MEMBER') AS v", false, old)
		fixture.AccessRoleBool(s, "SELECT pg_has_role($1::oid,$1::oid,'MEMBER') AS v", true, old)
		rls := predefinedHeld(s, "SELECT row_security_active($1::text) AS v")
		s.Actor("ar_actor")
		fixture.AccessRoleHeldBool(s, rls, false, "ar_t")
		s.Actor("")
		s.Exec("ALTER TABLE ar_t ENABLE ROW LEVEL SECURITY")
		s.Actor("ar_actor")
		// ar_actor now inherits ar_target, and so is an owner.
		fixture.AccessRoleHeldBool(s, rls, false, "ar_t")
		s.Actor("")
		s.Exec("GRANT ar_middle TO ar_actor WITH INHERIT FALSE")
		s.Actor("ar_actor")
		fixture.AccessRoleHeldBool(s, rls, true, "ar_t")
		s.Exec("SET ROLE ar_target")
		fixture.AccessRoleHeldBool(s, rls, false, "ar_t")
		s.Exec("RESET ROLE")
		fixture.AccessRoleHeldBool(s, rls, true, "ar_t")
		s.Actor("")
		s.Exec("ALTER ROLE ar_actor BYPASSRLS")
		s.Actor("ar_actor")
		fixture.AccessRoleHeldBool(s, rls, false, "ar_t")
		s.Actor("")
		s.Exec("ALTER ROLE ar_actor NOBYPASSRLS")
		s.Exec("ALTER TABLE ar_t OWNER TO ar_actor")
		s.Actor("ar_actor")
		fixture.AccessRoleHeldBool(s, rls, false, "ar_t")
		s.Exec("ALTER TABLE ar_t FORCE ROW LEVEL SECURITY")
		fixture.AccessRoleHeldBool(s, rls, true, "ar_t")
		s.Exec("ALTER TABLE ar_t DISABLE ROW LEVEL SECURITY")
		fixture.AccessRoleHeldBool(s, rls, false, "ar_t")
		s.Actor("")
		s.Exec("CREATE VIEW ar_identity AS SELECT current_user AS who,pg_has_role('ar_target','USAGE') AS member")
		s.Exec("CREATE VIEW ar_invoker WITH (security_invoker=true) AS SELECT current_user AS who,pg_has_role('ar_target','USAGE') AS member")
		s.Exec("GRANT SELECT ON ar_identity,ar_invoker TO ar_actor")
		s.Actor("ar_actor")
		for _, view := range []string{"ar_identity", "ar_invoker"} {
			predefinedSelect(s, "SELECT who,member FROM "+view, []string{"who", "member"}, []string{"NAME", "BOOL"}, [][]any{{"ar_actor", false}})
		}
	})
}

func TestReferenceAccessRoleExpressions(t *testing.T) {
	fixture.AccessRoleCases(t, []string{"ar_actor", "ar_target"}, func(s *fixture.RoleMembershipScenario) {
		s.Exec("GRANT ar_target TO ar_actor")
		s.Exec("CREATE TABLE ar_expr(n INT,who name)")
		s.Run(fixture.Exec("INSERT INTO ar_expr VALUES(1,'ar_actor'),(2,'ar_target')", 2))
		// Composite expressions and view outputs retain their ordinary descriptor
		// semantics; only focused direct inquiry tests assert strict nullability.
		checkExpression := func(q string, want bool, args ...any) {
			step := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...)
			step.CheckMetadata = fixture.AccessRoleMetadata(s.Backend, false)
			s.Run(step)
		}
		for _, q := range []string{
			"SELECT CASE WHEN pg_has_role($1,'MEMBER') THEN COALESCE(row_security_active(NULL::oid),true) ELSE false END AS v",
			"SELECT (pg_has_role($1,'MEMBER') AND NOT row_security_active(0::oid)) AS v",
			"WITH x AS (SELECT pg_has_role($1,'MEMBER') AS v) SELECT v FROM x",
			"SELECT v FROM (SELECT pg_has_role($1,'MEMBER') AS v) AS x WHERE v ORDER BY v",
		} {
			checkExpression(q, true, "ar_target")
		}
		fixture.AccessRoleSelect(s, "SELECT pg_has_role(who,'ar_target','MEMBER') AS v FROM ar_expr GROUP BY who HAVING pg_has_role(who,'ar_target','MEMBER') ORDER BY v", []string{"v"}, [][]any{{true}, {true}})
		fixture.AccessRoleSelect(s, "SELECT pg_has_role(a.who,b.who,'MEMBER') AS v FROM ar_expr a JOIN ar_expr b ON a.n=b.n WHERE pg_has_role(a.who,b.who,'USAGE') ORDER BY a.n", []string{"v"}, [][]any{{true}, {true}})
		s.Exec("CREATE VIEW ar_expression_view AS SELECT CASE WHEN pg_has_role('ar_target','MEMBER') THEN row_security_active(0::oid) ELSE true END AS v")
		checkExpression("SELECT v FROM ar_expression_view", false)
		s.Exec("ALTER VIEW ar_expression_view RENAME TO ar_expression_remapped")
		checkExpression("SELECT v FROM ar_expression_remapped", false)
		predefinedFailure(s, "CREATE INDEX ar_stable_idx ON ar_expr(pg_has_role(who,'MEMBER'))", fixture.Diagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"}, false, "execute")
	})
}

func TestReferenceAccessRoleControls(t *testing.T) {
	fixture.AccessRoleCases(t, []string{"ar_actor", "ar_target"}, func(s *fixture.RoleMembershipScenario) {
		fixture.AccessRoleTable(s)
		s.Exec("ALTER TABLE ar_t ENABLE ROW LEVEL SECURITY")
		s.Exec("ALTER TABLE ar_t FORCE ROW LEVEL SECURITY")
		s.Actor("ar_target")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "ar_target", func() {
			s.Exec("CREATE VIEW ar_saved AS SELECT pg_has_role('ar_target','MEMBER') AS m,row_security_active('ar_t') AS r")
		})
		s.Actor("ar_actor")
		fixture.AccessRoleSelect(s, "SELECT pg_has_role('ar_target','MEMBER') AS m,row_security_active('ar_t') AS r", []string{"m", "r"}, [][]any{{false, true}})
		s.Actor("")
		fixture.AccessRoleSelect(s, "SELECT m,r FROM ar_saved", []string{"m", "r"}, [][]any{{true, false}})
		if s.Backend.PG {
			return
		}
		var stream bytes.Buffer
		if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &stream); err != nil {
			s.T.Fatal(err)
		}
		if strings.Contains(stream.String(), "CREATE ROLE") || strings.Contains(stream.String(), "GRANT ar_target TO") {
			s.T.Fatal("dump serialized shared role state")
		}
		if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(stream.Bytes())); err != nil {
			s.T.Fatal(err)
		}
		// Reacquire after replacement; genuine view recipe binds destination types.
		c, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer c.Close()
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		fixture.RunStep(s.T, s.P.Ctx, c, s.Record, fixture.Exec("SET SESSION AUTHORIZATION ar_target", 0))
		step := fixture.Select("SELECT m,r FROM ar_saved", []string{"m", "r"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}})
		step.Prepared = s.Prepared
		fixture.RunStep(s.T, s.P.Ctx, c, s.Record, step)
		// Retained native rows are eager values, not deferred authority decisions.
		rows, err := c.QueryContext(s.P.Ctx, "SELECT row_security_active('ar_t') AS v")
		if err != nil {
			s.T.Fatal(err)
		}
		defer rows.Close()
		if _, err := c.ExecContext(s.P.Ctx, "ALTER TABLE ar_t DISABLE ROW LEVEL SECURITY"); err != nil {
			s.T.Fatal(err)
		}
		fixture.ArrayReferenceConsumeHeld(s.T, rows, [][]any{{true}}, []string{"BOOL"})
		fixture.RunStep(s.T, s.P.Ctx, c, s.Record, fixture.Select("SELECT row_security_active('ar_t') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
	})
}
