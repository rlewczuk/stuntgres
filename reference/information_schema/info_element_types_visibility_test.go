package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoElementTypesVisibility(t *testing.T) {
	t.Run("object_rights", func(t *testing.T) {
		etScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
			etExec(t, p, b, c,
				"CREATE ROLE et_owner", "CREATE ROLE et_reader", "CREATE ROLE et_inherit", "CREATE ROLE et_noinherit NOINHERIT", "CREATE ROLE et_set",
				"CREATE SCHEMA et_private", "REVOKE ALL ON SCHEMA et_private FROM PUBLIC",
				"CREATE DOMAIN et_private.et_element AS integer",
				"CREATE DOMAIN et_private.et_domain AS integer[]",
				"CREATE TYPE et_private.et_composite AS (a integer[],s integer)",
				"CREATE TABLE et_private.et_acl(a integer[],b et_private.et_element[],s integer)",
				"CREATE FUNCTION et_private.et_acl_fun(a integer[]) RETURNS integer[] LANGUAGE SQL AS 'SELECT a'",
				"ALTER TABLE et_private.et_acl OWNER TO et_owner",
				"ALTER DOMAIN et_private.et_domain OWNER TO et_owner",
				"ALTER TYPE et_private.et_composite OWNER TO et_owner",
				"ALTER FUNCTION et_private.et_acl_fun(integer[]) OWNER TO et_owner",
				"REVOKE ALL ON TABLE et_private.et_acl FROM PUBLIC,et_owner",
				"REVOKE ALL ON DOMAIN et_private.et_domain FROM PUBLIC,et_owner",
				"REVOKE ALL ON DOMAIN et_private.et_element FROM PUBLIC",
				"REVOKE ALL ON TYPE et_private.et_composite FROM PUBLIC,et_owner",
				"REVOKE ALL ON FUNCTION et_private.et_acl_fun(integer[]) FROM PUBLIC,et_owner",
				"GRANT et_owner TO et_inherit WITH INHERIT TRUE, SET TRUE",
				"GRANT et_owner TO et_noinherit",
				"GRANT et_owner TO et_set WITH INHERIT FALSE, SET TRUE",
			)
			id := rsuIdentity(t, p, c, "et_private", "et_acl_fun", "integer[]")
			a := etRow(database, "et_private", "et_acl", "TABLE", 1, "integer", "pg_catalog", "int4")
			bb := etRow(database, "et_private", "et_acl", "TABLE", 2, "USER-DEFINED", "et_private", "et_element")
			domain := etRow(database, "et_private", "et_domain", "DOMAIN", 1, "integer", "pg_catalog", "int4")
			composite := etRow(database, "et_private", "et_composite", "USER-DEFINED TYPE", 1, "integer", "pg_catalog", "int4")
			routine := [][]any{etRow(database, "et_private", id, "ROUTINE", 0, "integer", "pg_catalog", "int4"), etRow(database, "et_private", id, "ROUTINE", 1, "integer", "pg_catalog", "int4")}
			all := append([][]any{a, bb, domain, composite}, routine...)
			step := func(rows [][]any) fixture.Step { return etProjection(b.PG, "object_schema='et_private'", rows) }
			held := etHeld(t, p, b, c, step(nil).Query)
			actor := func(role string, rows [][]any) {
				t.Helper()
				etExec(t, p, b, c, "SET SESSION AUTHORIZATION "+role)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{role, role}}))
				etCheck(t, p, b, c, step(rows))
				held(step(rows))
				etExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
			}
			actor("et_owner", all)
			actor("et_inherit", all)
			for _, role := range []string{"et_reader", "et_noinherit", "et_set"} {
				actor(role, nil)
			}
			etExec(t, p, b, c, "SET SESSION AUTHORIZATION et_noinherit")
			held(step(nil))
			etExec(t, p, b, c, "SET ROLE et_owner")
			held(step(all))
			etExec(t, p, b, c, "RESET ROLE")
			held(step(nil))
			etExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
			// Each qualifying column mask is independent; a scalar or DELETE grant
			// must not accidentally disclose either array declaration.
			for _, x := range []struct {
				privilege string
				rows      [][]any
			}{
				{"SELECT(a)", [][]any{a}}, {"INSERT(b)", [][]any{bb}},
				{"UPDATE(b)", [][]any{bb}}, {"REFERENCES(b)", [][]any{bb}},
				{"DELETE", nil}, {"SELECT(s)", nil},
				{"SELECT", [][]any{a, bb}}, {"INSERT", [][]any{a, bb}},
				{"UPDATE", [][]any{a, bb}}, {"REFERENCES", [][]any{a, bb}},
			} {
				etExec(t, p, b, c, "GRANT "+x.privilege+" ON TABLE et_private.et_acl TO et_reader")
				actor("et_reader", x.rows)
				etExec(t, p, b, c, "REVOKE "+x.privilege+" ON TABLE et_private.et_acl FROM et_reader")
				actor("et_reader", nil)
			}
			for _, x := range []struct {
				grant string
				rows  [][]any
			}{
				{"USAGE ON DOMAIN et_private.et_domain", [][]any{domain}},
				{"USAGE ON TYPE et_private.et_composite", [][]any{composite}},
				{"EXECUTE ON FUNCTION et_private.et_acl_fun(integer[])", routine},
			} {
				etExec(t, p, b, c, "GRANT "+x.grant+" TO et_reader")
				actor("et_reader", x.rows)
				etExec(t, p, b, c, "REVOKE "+x.grant+" FROM et_reader")
				actor("et_reader", nil)
				etExec(t, p, b, c, "GRANT "+x.grant+" TO PUBLIC")
				actor("et_reader", x.rows)
				etExec(t, p, b, c, "REVOKE "+x.grant+" FROM PUBLIC")
				actor("et_reader", nil)
			}
			etExec(t, p, b, c, "GRANT SELECT(a) ON TABLE et_private.et_acl TO PUBLIC")
			actor("et_reader", [][]any{a})
			etExec(t, p, b, c, "REVOKE SELECT(a) ON TABLE et_private.et_acl FROM PUBLIC")
			// A grant to an inherited role works without schema or element USAGE.
			etExec(t, p, b, c, "GRANT SELECT(b) ON TABLE et_private.et_acl TO et_noinherit", "GRANT et_noinherit TO et_reader WITH INHERIT TRUE")
			actor("et_reader", [][]any{bb})
			etExec(t, p, b, c, "REVOKE et_noinherit FROM et_reader")
			actor("et_reader", nil)
			etExec(t, p, b, c, "SET SESSION AUTHORIZATION et_set")
			held(step(nil))
			etExec(t, p, b, c, "SET ROLE et_owner")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_user,session_user", []string{"current_user", "session_user"}, []string{"NAME", "NAME"}, [][]any{{"et_owner", "et_set"}}))
			held(step(all))
			etExec(t, p, b, c, "RESET ROLE")
			held(step(nil))
			etExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("view_rights", func(t *testing.T) {
		etScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
			etExec(t, p, b, c, "CREATE ROLE et_reader", "CREATE TABLE et_view_acl(a integer[])", "GRANT SELECT ON TABLE et_view_acl TO et_reader")
			want := [][]any{etRow(database, "public", "et_view_acl", "TABLE", 1, "integer", "pg_catalog", "int4")}
			step := etProjection(b.PG, "object_schema='public' AND object_name='et_view_acl'", want)
			held := etHeld(t, p, b, c, step.Query)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.ScenarioBudget)
				defer cancel()
				for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT SELECT ON information_schema.element_types TO PUBLIC"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Errorf("restoring view ACL: %v", err)
					}
				}
			})
			etExec(t, p, b, c, "REVOKE SELECT ON information_schema.element_types FROM PUBLIC", "SET SESSION AUTHORIZATION et_reader")
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(step.Query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for view element_types"))
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			denied := fixture.ErrorDiagnostic(step.Query, fixture.ControlsReferenceDiagnostic("42501", "permission denied for view element_types"))
			denied.SelectRows, denied.ErrorStage = true, "execute"
			held(denied)
			etExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT(object_name) ON information_schema.element_types TO et_reader", "SET SESSION AUTHORIZATION et_reader")
			etCheck(t, p, b, c, fixture.Select("SELECT object_name FROM information_schema.element_types WHERE object_name='et_view_acl'", []string{"object_name"}, []string{"NAME"}, [][]any{{"et_view_acl"}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
			etExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON information_schema.element_types TO PUBLIC", "REVOKE SELECT(object_name) ON information_schema.element_types FROM et_reader", "SET SESSION AUTHORIZATION et_reader")
			held(step)
			etExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		})
	})
}
