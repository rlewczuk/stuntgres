package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceXML2XPathTableAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"xt_reader", "xt_provider_owner"})
	fixture.SchemaReferenceCleanup(t, "xt_private")
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		xtSeed(t, p, b, c)
		xtExec(t, p, b, c,
			"CREATE ROLE xt_reader", "CREATE ROLE xt_provider_owner",
			"GRANT USAGE ON SCHEMA public TO xt_reader",
			"CREATE TABLE xt_acl(id integer,doc text,secret integer)",
			"CREATE SCHEMA xt_private", "CREATE TABLE xt_private.xt_hidden(id integer,doc text)",
			"GRANT SELECT ON xt_private.xt_hidden TO xt_reader")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xt_acl VALUES(1,'<doc><a>10</a><a>20</a></doc>',1)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xt_private.xt_hidden VALUES(1,'<doc><a>10</a></doc>')", 1))
		xtExec(t, p, b, c, "GRANT SELECT(id,doc) ON xt_acl TO xt_reader")
		q := xtQuery("id", "doc", "xt_acl", "/doc/a", "true", "id integer,a text")
		s := fixture.Select(q, []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "10"}, {int64(1), "20"}})
		s.Bag = true
		xtExec(t, p, b, c, "SET ROLE xt_reader")
		xtBoth(t, p, b, c, "column_select_positive", s)
		xtFailure(t, p, b, c, "criteria_column_denied", xtQuery("id", "doc", "xt_acl", "/doc/a", "secret=1", "id integer,a text"), xtStatement(xml2Error("42501", "permission denied for table xt_acl"), "SELECT id, doc FROM xt_acl WHERE secret=1"), false)
		xtFailure(t, p, b, c, "source_table_denied", xtQuery("id", "doc", "xt_src", "/doc/a", "id=1", "id integer,a text"), xtStatement(xml2Error("42501", "permission denied for table xt_src"), "SELECT id, doc FROM xt_src WHERE id=1"), false)
		xtFailure(t, p, b, c, "source_namespace_denied", xtQuery("id", "doc", "xt_private.xt_hidden", "/doc/a", "true", "id integer,a text"), xtInternal(xml2Error("42501", "permission denied for schema xt_private"), "SELECT id, doc FROM xt_private.xt_hidden WHERE true", "21"), false)
		xtExec(t, p, b, c, "RESET ROLE")

		t.Run("held_current_permissions", func(t *testing.T) {
			// A fatal held-call assertion must not leave the parent's ALTER
			// running as xt_reader instead of the original installation owner.
			defer xtExec(t, p, b, c, "RESET ROLE")
			stmt, err := c.PrepareContext(p.Ctx, xtParameters)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: xtParameters}
			want := fixture.Select(xtParameters, []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "10"}, {int64(1), "20"}}, "id", "doc", "xt_acl", "/doc/a", "true")
			xtExec(t, p, b, c, "SET ROLE xt_reader")
			fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
			xtExec(t, p, b, c, "RESET ROLE")
			for _, tc := range []struct {
				name, revoke, grant string
				want                fixture.Diagnostic
			}{
				{"column_select", "REVOKE SELECT(doc) ON xt_acl FROM xt_reader", "GRANT SELECT(doc) ON xt_acl TO xt_reader", xtStatement(xml2Error("42501", "permission denied for table xt_acl"), "SELECT id, doc FROM xt_acl WHERE true")},
				{"execute", "REVOKE EXECUTE ON FUNCTION xpath_table(text,text,text,text,text) FROM PUBLIC", "GRANT EXECUTE ON FUNCTION xpath_table(text,text,text,text,text) TO PUBLIC", xml2Error("42501", "permission denied for function xpath_table")},
			} {
				t.Run(tc.name, func(t *testing.T) {
					defer xtExec(t, p, b, c, "RESET ROLE")
					xtExec(t, p, b, c, tc.revoke, "SET ROLE xt_reader")
					bad := fixture.ErrorDiagnostic(xtParameters, tc.want, want.Args...)
					bad.SelectRows, bad.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, h, b.Recorder, bad)
					xtFailure(t, p, b, c, "fresh_revoked", q, tc.want, false)
					if tc.name == "execute" {
						xtFailure(t, p, b, c, "strict_null_after_execute_revoke", xtParameters, tc.want, false, nil, "doc", "xt_acl", "[", "true")
					}
					xtExec(t, p, b, c, "RESET ROLE", tc.grant, "SET ROLE xt_reader")
					fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
					xtExec(t, p, b, c, "RESET ROLE")
				})
			}
			xtExec(t, p, b, c, "REVOKE USAGE ON SCHEMA public FROM PUBLIC,xt_reader", "SET ROLE xt_reader")
			unqualified := xml2Error("42883", "function xpath_table(text, text, text, text, text) does not exist")
			unqualified.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			unqualified.Position = "15"
			xtFailure(t, p, b, h, "held_namespace_revoke", xtParameters, unqualified, true, want.Args...)
			xtExec(t, p, b, c, "RESET ROLE", "GRANT USAGE ON SCHEMA public TO xt_reader")
		})

		xtExec(t, p, b, c,
			"ALTER FUNCTION xpath_table(text,text,text,text,text) OWNER TO xt_provider_owner",
			"SET ROLE xt_reader")
		xtBoth(t, p, b, c, "owner_change_keeps_invoker_column_rights", s)
		xtFailure(t, p, b, c, "owner_does_not_bypass_source_acl", xtQuery("id", "doc", "xt_src", "/doc/a", "id=1", "id integer,a text"), xtStatement(xml2Error("42501", "permission denied for table xt_src"), "SELECT id, doc FROM xt_src WHERE id=1"), false)
		xtExec(t, p, b, c, "RESET ROLE", "REVOKE USAGE ON SCHEMA public FROM PUBLIC,xt_reader", "SET ROLE xt_reader")
		qualified := xml2Error("42501", "permission denied for schema public")
		qualified.Position = "15"
		xtFailure(t, p, b, c, "function_namespace_denied", "SELECT * FROM public.xpath_table('id','doc','xt_acl','/doc/a','true') AS x(id integer,a text)", qualified, true)
		xtExec(t, p, b, c, "RESET ROLE", "GRANT USAGE ON SCHEMA public TO xt_reader")

		xtExec(t, p, b, c,
			"CREATE TABLE xt_rls(id integer,doc text)",
			"GRANT SELECT ON xt_rls TO xt_reader",
			"ALTER TABLE xt_rls ENABLE ROW LEVEL SECURITY",
			"CREATE POLICY xt_visible ON xt_rls FOR SELECT TO xt_reader USING(id=1)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xt_rls VALUES(1,'<doc><a>10</a></doc>'),(2,'<doc><a>20</a></doc>')", 2))
		xtExec(t, p, b, c, "SET ROLE xt_reader")
		xtBoth(t, p, b, c, "rls_invoker", fixture.Select(xtQuery("id", "doc", "xt_rls", "/doc/a", "true", "id integer,a integer"), []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
		xtExec(t, p, b, c, "RESET ROLE")
	})
}
