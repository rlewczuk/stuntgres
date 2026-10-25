package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableAuthorization(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		role := "typed_table_actor"
		fixture.RoleReferenceCleanup(t, []string{role})
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("CREATE USER "+role, 0))
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("CREATE TABLE owned OF pair", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		fixture.CompositeReferenceError(t, p, c, "ALTER TABLE public.owned NOT OF", "execute", typedTableError("42501", "must be owner of table owned"))
		fixture.CompositeReferenceError(t, p, c, "ALTER TYPE public.pair ADD ATTRIBUTE flag boolean CASCADE", "execute", typedTableError("42501", "must be owner of table pair"))
		fixture.CompositeReferenceError(t, p, c, "DROP TYPE public.pair CASCADE", "execute", typedTableError("42501", "must be owner of type pair"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO "+role, 0))
		run(fixture.Exec("CREATE TYPE mood AS ENUM ('calm')", 0))
		run(fixture.Exec("CREATE TABLE rowsrc(id integer)", 0))
		run(fixture.Exec("REVOKE USAGE ON TYPE public.pair,public.mood,public.rowsrc FROM PUBLIC", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		if b.PG { // Native has no has_type_privilege; the denials below witness the ACL.
			run(fixture.Select("SELECT has_type_privilege('public.pair','USAGE') AS source_usage", []string{"source_usage"}, []string{"BOOL"}, [][]any{{false}}))
		}
		fixture.CompositeReferenceError(t, p, c, "CREATE TABLE public.denied OF public.pair", "execute", typedTableError("42501", "permission denied for type pair"))
		run(fixture.Exec("CREATE TABLE public.mine(id integer)", 0))
		// PostgreSQL 18.6 (CVE-2026-6470) gates attachment on source USAGE like
		// CREATE OF: after the composite-source checks, before inheritance/shape.
		fixture.CompositeReferenceError(t, p, c, "ALTER TABLE public.mine OF public.pair", "execute", typedTableError("42501", "permission denied for type pair"))
		run(fixture.TypedTableAttachment("public.mine", "public.pair", false))
		fixture.CompositeReferenceError(t, p, c, "ALTER TABLE public.mine OF public.mood", "execute", typedTableError("42809", "type mood is not a composite type"))
		rowType := typedTableError("42809", "type rowsrc is the row type of another table")
		rowType.Detail = "A typed table must use a stand-alone composite type created with CREATE TYPE."
		fixture.CompositeReferenceError(t, p, c, "ALTER TABLE public.mine OF public.rowsrc", "execute", rowType)
		run(fixture.Exec("CREATE TABLE public.parent(id integer)", 0))
		run(fixture.Exec("CREATE TABLE public.child() INHERITS (public.parent)", 0))
		run(fixture.Exec("CREATE TABLE public.other(z text)", 0))
		fixture.CompositeReferenceError(t, p, c, "ALTER TABLE public.child OF public.pair", "execute", typedTableError("42501", "permission denied for type pair"))
		fixture.CompositeReferenceError(t, p, c, "ALTER TABLE public.other OF public.pair", "execute", typedTableError("42501", "permission denied for type pair"))
		run(fixture.Exec("DROP TABLE public.child,public.parent,public.other", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT USAGE ON TYPE public.pair TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		run(fixture.Exec("ALTER TABLE public.mine OF public.pair", 0))
		run(fixture.TypedTableAttachment("public.mine", "public.pair", true))
		run(fixture.Exec("INSERT INTO public.mine VALUES(11)", 1))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE USAGE ON TYPE public.pair FROM "+role, 0))
		// A superuser source owner may cascade to a table owned by a different role.
		run(fixture.Exec("ALTER TYPE public.pair ADD ATTRIBUTE flag boolean CASCADE", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		run(fixture.Exec("ALTER TABLE public.mine NOT OF", 0))
		run(fixture.Select("SELECT * FROM public.mine", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(11), nil}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("DROP TABLE public.mine", 0))
		run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM "+role, 0))
		run(fixture.Exec("DROP ROLE "+role, 0))
	})
}

// Source/member type ACLs and typed-table ownership, with a non-superuser
// source owner and a reader that owns an attached table.
func TestReferenceTypedTableProviderACL(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		owner, reader := "typed_acl_owner", "typed_acl_reader"
		fixture.RoleReferenceCleanup(t, []string{reader, owner})
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		as := func(role string) { run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0)) }
		admin := func() { run(fixture.Exec("RESET SESSION AUTHORIZATION", 0)) }
		denied := func(q, message string) {
			fixture.CompositeReferenceError(t, p, c, q, "execute", typedTableError("42501", message))
		}
		rows := func(table string, columns, types []string, row []any) {
			run(fixture.Select("SELECT * FROM "+table+" ORDER BY id", columns, types, [][]any{row}))
		}
		for _, q := range []string{"CREATE USER " + owner, "CREATE USER " + reader, "CREATE DOMAIN member AS integer", "CREATE TYPE foreign_pair AS (z integer)", "GRANT USAGE,CREATE ON SCHEMA public TO " + owner + "," + reader} {
			run(fixture.Exec(q, 0))
		}
		as(owner)
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("CREATE TABLE typed OF pair", 0))
		run(fixture.Exec("INSERT INTO typed VALUES(5)", 1))
		run(fixture.Exec("REVOKE USAGE ON TYPE pair FROM PUBLIC", 0))
		as(reader)
		run(fixture.Exec("CREATE TABLE mine(id integer)", 0))
		run(fixture.Exec("INSERT INTO mine VALUES(11)", 1))
		denied("CREATE TABLE denied OF pair", "permission denied for type pair")
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='denied'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		denied("ALTER TABLE mine OF pair", "permission denied for type pair")
		run(fixture.TypedTableAttachment("mine", "pair", false))
		as(owner)
		run(fixture.Exec("GRANT USAGE ON TYPE pair TO "+reader, 0))
		as(reader)
		run(fixture.Exec("ALTER TABLE mine OF pair", 0))
		run(fixture.TypedTableAttachment("mine", "pair", true))
		run(fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='mine'::regclass::oid", []string{"rolname"}, []string{"NAME"}, [][]any{{reader}}))
		// Source ownership does not extend to typed tables owned by other roles.
		as(owner)
		for _, q := range []string{"ALTER TYPE pair ADD ATTRIBUTE flag boolean CASCADE", "ALTER TYPE pair DROP ATTRIBUTE id CASCADE", "ALTER TYPE pair RENAME ATTRIBUTE id TO k CASCADE", "ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint CASCADE"} {
			denied(q, "must be owner of table mine")
		}
		admin()
		run(fixture.Exec("REVOKE USAGE ON TYPE member FROM PUBLIC", 0))
		as(owner)
		// ADD ATTRIBUTE checks typed-table ownership first; ALTER ATTRIBUTE TYPE
		// checks the new type's USAGE first.
		denied("ALTER TYPE pair ADD ATTRIBUTE value member CASCADE", "must be owner of table mine")
		denied("ALTER TYPE pair ALTER ATTRIBUTE id TYPE member CASCADE", "permission denied for type member")
		denied("DROP TYPE pair,foreign_pair CASCADE", "must be owner of type foreign_pair")
		admin()
		rows("mine", []string{"id"}, []string{"INT4"}, []any{int64(11)})
		rows("typed", []string{"id"}, []string{"INT4"}, []any{int64(5)})
		run(fixture.TypedTableAttachment("typed", "pair", true))
		run(fixture.TypedTableAttachment("mine", "pair", true))
		as(reader)
		run(fixture.Exec("ALTER TABLE mine NOT OF", 0))
		as(owner)
		denied("ALTER TYPE pair ADD ATTRIBUTE value member CASCADE", "permission denied for type member")
		admin()
		run(fixture.Exec("GRANT USAGE ON TYPE member TO "+owner, 0))
		// Inheritance descendants of typed tables are ownership-checked too.
		run(fixture.Exec("CREATE TABLE kid() INHERITS (typed)", 0))
		run(fixture.Exec("ALTER TABLE kid OWNER TO "+reader, 0))
		as(owner)
		denied("ALTER TYPE pair ADD ATTRIBUTE value member CASCADE", "must be owner of table kid")
		admin()
		run(fixture.Exec("DROP TABLE kid", 0))
		as(owner)
		run(fixture.Exec("ALTER TYPE pair ADD ATTRIBUTE value member CASCADE", 0))
		rows("typed", []string{"id", "value"}, []string{"INT4", "INT4"}, []any{int64(5), nil})
		admin()
		rows("mine", []string{"id"}, []string{"INT4"}, []any{int64(11)})
		for _, q := range []string{"DROP TABLE mine,typed", "DROP TYPE pair,foreign_pair", "DROP DOMAIN member", "REVOKE USAGE,CREATE ON SCHEMA public FROM " + owner + "," + reader, "DROP ROLE " + reader + "," + owner} {
			run(fixture.Exec(q, 0))
		}
	})
}

func TestReferenceTypedTableReadOnlyAdmission(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("BEGIN READ ONLY", 0))
		for _, tc := range []struct{ q, tag string }{
			{"CREATE TABLE denied OF missing", "CREATE TABLE"},
			{"ALTER TABLE missing OF absent", "ALTER TABLE"},
			{"ALTER TABLE missing NOT OF", "ALTER TABLE"},
			{"ALTER TYPE missing ADD ATTRIBUTE flag boolean CASCADE", "ALTER TYPE"},
			{"DROP TYPE missing CASCADE", "DROP TYPE"},
		} {
			// A failed PG transaction must be recovered before the next ordering witness.
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("BEGIN READ ONLY", 0))
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + tc.tag + " in a read-only transaction"})
			s.ErrorStage = "execute"
			run(s)
		}
		run(fixture.Exec("ROLLBACK", 0))
	})
}
