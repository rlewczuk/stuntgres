package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgClassACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pc18_acl_reader", "pc18_acl_owner", "pc18_acl_observer"})
	fixture.SchemaReferenceCleanup(t, "pc18_acl_schema")
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.PGClassExec(t, p, b, b.DB, "CREATE ROLE pc18_acl_reader", "CREATE ROLE pc18_acl_owner", "CREATE ROLE pc18_acl_observer", "CREATE SCHEMA pc18_acl_schema AUTHORIZATION pc18_acl_reader", "ALTER SCHEMA pc18_acl_schema OWNER TO pc18_acl_reader", "CREATE TABLE pc18_acl_schema.pc18_acl_heap(id integer)", "ALTER TABLE pc18_acl_schema.pc18_acl_heap OWNER TO pc18_acl_owner", "CREATE TABLE public.pc18_acl_hidden(id integer)", "ALTER TABLE public.pc18_acl_hidden OWNER TO pc18_acl_owner")
		q := "SELECT relacl FROM pg_class WHERE oid=$1"
		var oid int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'pc18_acl_schema.pc18_acl_heap'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		held := fixture.ViewHold(t, p, b, q)
		check := func(db fixture.Executor, acl any) {
			pgClassCheck(t, p, b, db, fixture.Select(q, []string{"relacl"}, []string{"_ACLITEM"}, [][]any{{acl}}, oid))
		}
		check(b.DB, nil)
		check(held, nil)
		a, observer := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.PGClassExec(t, p, b, a, "GRANT SELECT,UPDATE ON pc18_acl_schema.pc18_acl_heap TO pc18_acl_reader WITH GRANT OPTION", "GRANT SELECT ON pc18_acl_schema.pc18_acl_heap TO PUBLIC")
		initial := "{pc18_acl_owner=arwdDxtm/pc18_acl_owner,pc18_acl_reader=r*w*/pc18_acl_owner,=r/pc18_acl_owner}"
		check(b.DB, initial)
		check(held, initial)
		pgClassCheck(t, p, b, b.DB, fixture.Prepared(fixture.Select(q, []string{"relacl"}, []string{"_ACLITEM"}, [][]any{{initial}}, oid)))
		var old *sql.Rows
		var types []*sql.ColumnType
		if !b.PG {
			var err error
			old, err = b.DB.QueryContext(p.Ctx, q, oid)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			types, err = old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
		}
		fixture.PGClassExec(t, p, b, a, "REVOKE UPDATE ON pc18_acl_schema.pc18_acl_heap FROM pc18_acl_reader", "GRANT SELECT(id) ON pc18_acl_schema.pc18_acl_heap TO pc18_acl_observer")
		after := "{pc18_acl_owner=arwdDxtm/pc18_acl_owner,pc18_acl_reader=r*/pc18_acl_owner,=r/pc18_acl_owner}"
		check(held, after)
		// The pinned fixture's ordinary recreated public schema is not visible
		// to this role by default. Native public visibility stays unchanged;
		// real USAGE isolates table permission from schema-name visibility.
		if b.PG {
			fixture.PGClassExec(t, p, b, a, "GRANT USAGE ON SCHEMA public TO pc18_acl_observer")
		}
		// Column ACL is not projected into stored relation ACL, nor are rows
		// hidden from a principal without any relation grants.
		fixture.PGClassExec(t, p, b, observer, "SET ROLE pc18_acl_observer")
		defer observer.ExecContext(p.Ctx, "RESET ROLE")
		check(observer, after)
		pgClassCheck(t, p, b, observer, fixture.Select("SELECT relname,relacl FROM pg_class WHERE relname='pc18_acl_hidden'", []string{"relname", "relacl"}, []string{"NAME", "_ACLITEM"}, [][]any{{"pc18_acl_hidden", nil}}))
		denied := fixture.ImplicitError("SELECT id FROM public.pc18_acl_hidden WHERE FALSE", fixture.Diagnostic{Code: "42501", Message: "permission denied for table pc18_acl_hidden"})
		denied.SelectRows, denied.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, denied)
		fixture.PGClassExec(t, p, b, observer, "RESET ROLE")
		fixture.PGClassExec(t, p, b, a, "REVOKE SELECT ON pc18_acl_schema.pc18_acl_heap FROM PUBLIC")
		check(observer, "{pc18_acl_owner=arwdDxtm/pc18_acl_owner,pc18_acl_reader=r*/pc18_acl_owner}")
		if old != nil {
			pgClassRetained(t, old, types, []string{"relacl"}, []string{"_ACLITEM"}, []any{initial})
		}
		// An explicitly empty ACL is distinct from an implicit default NULL.
		fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE pc18_acl_schema.pc18_acl_empty(id integer)", "ALTER TABLE pc18_acl_schema.pc18_acl_empty OWNER TO pc18_acl_owner")
		fixture.PGClassExec(t, p, b, a, "REVOKE ALL ON pc18_acl_schema.pc18_acl_empty FROM pc18_acl_owner")
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relacl,relacl IS NULL AS absent FROM pg_class WHERE relname='pc18_acl_empty'", []string{"relacl", "absent"}, []string{"_ACLITEM", "BOOL"}, [][]any{{"{}", false}}))
		// Exercise every stored table privilege, rather than only SELECT; the
		// private authorization bit layout must not be cast into ACLITEM bits.
		fixture.PGClassExec(t, p, b, a, "GRANT INSERT,SELECT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER,MAINTAIN ON pc18_acl_schema.pc18_acl_heap TO pc18_acl_reader WITH GRANT OPTION")
		check(held, "{pc18_acl_owner=arwdDxtm/pc18_acl_owner,pc18_acl_reader=a*r*w*d*D*x*t*m*/pc18_acl_owner}")
		fixture.PGClassExec(t, p, b, a, "SET ROLE pc18_acl_reader", "GRANT SELECT ON pc18_acl_schema.pc18_acl_heap TO pc18_acl_observer", "RESET ROLE")
		check(held, "{pc18_acl_owner=arwdDxtm/pc18_acl_owner,pc18_acl_reader=a*r*w*d*D*x*t*m*/pc18_acl_owner,pc18_acl_observer=r/pc18_acl_reader}")
	})
}

func TestReferencePgClassOptions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pc18_opts_owner"})
	fixture.SchemaReferenceCleanup(t, "pc18_opts_moved")
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.PGClassExec(t, p, b, b.DB, "CREATE TABLE pc18_opts_heap(id integer)", "CREATE VIEW pc18_opts_view WITH(security_invoker=false,security_barrier=true) AS SELECT id FROM pc18_opts_heap")
		q := "SELECT reloptions FROM pg_class WHERE oid=$1"
		var oid, rule int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'pc18_opts_view'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1", oid).Scan(&rule); err != nil {
			t.Fatal(err)
		}
		held := fixture.ViewHold(t, p, b, q)
		check := func(options any) {
			s := fixture.Select(q, []string{"reloptions"}, []string{"_TEXT"}, [][]any{{options}}, oid)
			pgClassCheck(t, p, b, b.DB, s)
			pgClassCheck(t, p, b, held, s)
		}
		check("{security_invoker=false,security_barrier=true}")
		fixture.PGClassExec(t, p, b, b.DB, "ALTER VIEW pc18_opts_view SET(security_invoker=true)")
		check("{security_barrier=true,security_invoker=true}")
		fixture.PGClassExec(t, p, b, b.DB, "ALTER VIEW pc18_opts_view RESET(security_barrier)")
		check("{security_invoker=true}")
		fixture.PGClassExec(t, p, b, b.DB, "ALTER VIEW pc18_opts_view RESET(security_invoker)")
		check(nil)
		fixture.PGClassExec(t, p, b, b.DB, "ALTER VIEW pc18_opts_view SET(security_invoker=off,security_barrier=FALSE)")
		check("{security_invoker=off,security_barrier=false}")
		fixture.PGClassExec(t, p, b, b.DB, "CREATE ROLE pc18_opts_owner", "CREATE SCHEMA pc18_opts_moved", "ALTER VIEW pc18_opts_view OWNER TO pc18_opts_owner", "ALTER VIEW pc18_opts_view RENAME TO pc18_opts_renamed", "ALTER VIEW pc18_opts_renamed SET SCHEMA pc18_opts_moved")
		check("{security_invoker=off,security_barrier=false}")
		fixture.PGClassExec(t, p, b, b.DB, "GRANT SELECT ON pc18_opts_moved.pc18_opts_renamed TO PUBLIC")
		acl := "{pc18_opts_owner=arwdDxtm/pc18_opts_owner,=r/pc18_opts_owner}"
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT relacl FROM pg_class WHERE oid=$1", []string{"relacl"}, []string{"_ACLITEM"}, [][]any{{acl}}, oid))
		fixture.PGClassExec(t, p, b, b.DB, "CREATE OR REPLACE VIEW pc18_opts_moved.pc18_opts_renamed AS SELECT id FROM pc18_opts_heap")
		check(nil)
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT oid=$1 AS identity,relacl=$2::aclitem[] AS acl FROM pg_class WHERE oid='pc18_opts_moved.pc18_opts_renamed'::regclass::oid", []string{"identity", "acl"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, oid, acl))
		pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT oid=$1 AS identity FROM pg_rewrite WHERE ev_class=$2", []string{"identity"}, []string{"BOOL"}, [][]any{{true}}, rule, oid))
		for _, tc := range []struct{ suffix, sql, option string }{{"local", "WITH LOCAL CHECK OPTION", "{check_option=local}"}, {"cascaded", "WITH CASCADED CHECK OPTION", "{check_option=cascaded}"}, {"numeric", "", "{security_invoker=0,security_barrier=on}"}} {
			opts := ""
			if tc.suffix == "numeric" {
				opts = " WITH(security_invoker=0,security_barrier='on')"
			}
			fixture.PGClassExec(t, p, b, b.DB, "CREATE VIEW pc18_opts_"+tc.suffix+opts+" AS SELECT id FROM pc18_opts_heap WHERE id>0 "+tc.sql)
			pgClassCheck(t, p, b, b.DB, fixture.Select("SELECT reloptions FROM pg_class WHERE relname='pc18_opts_"+tc.suffix+"'", []string{"reloptions"}, []string{"_TEXT"}, [][]any{{tc.option}}))
		}
	})
}
