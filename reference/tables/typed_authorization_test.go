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
		// REVOKE type ACL setup uses the existing PG-only surface; native provider
		// authority is supplemented by Stage B package-function tests, not fake GRANT parity.
		if b.PG {
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO "+role, 0))
			run(fixture.Exec("REVOKE USAGE ON TYPE public.pair FROM PUBLIC", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(fixture.Select("SELECT has_type_privilege('public.pair','USAGE') AS source_usage", []string{"source_usage"}, []string{"BOOL"}, [][]any{{false}}))
			fixture.CompositeReferenceError(t, p, c, "CREATE TABLE public.denied OF public.pair", "execute", typedTableError("42501", "permission denied for type pair"))
			run(fixture.Exec("CREATE TABLE public.mine(id integer)", 0))
			// Pinned PG accepts attachment under the same revoked source USAGE
			// that rejects CREATE OF. Do not invent a source ACL gate for attach.
			run(fixture.Exec("ALTER TABLE public.mine OF public.pair", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT USAGE ON TYPE public.pair TO "+role, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(fixture.Exec("ALTER TABLE public.mine OF public.pair", 0))
			run(fixture.Exec("INSERT INTO public.mine VALUES(11)", 1))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE USAGE ON TYPE public.pair FROM "+role, 0))
			// Source owner may cascade to a table owned by a different role.
			run(fixture.Exec("ALTER TYPE public.pair ADD ATTRIBUTE flag boolean CASCADE", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(fixture.Exec("ALTER TABLE public.mine NOT OF", 0))
			run(fixture.Select("SELECT * FROM public.mine", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(11), nil}}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("DROP TABLE public.mine", 0))
			run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM "+role, 0))
		}
		run(fixture.Exec("DROP ROLE "+role, 0))
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
