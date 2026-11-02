package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckGINPrivileges(t *testing.T) {
	t.Run("execute_and_namespace", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"gin_am_reader"})
		fixture.SchemaReferenceCleanup(t, "gin_am_secure")
		heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE SCHEMA gin_am_secure",
				"CREATE EXTENSION amcheck SCHEMA gin_am_secure",
				"CREATE ROLE gin_am_reader",
				"GRANT USAGE ON SCHEMA public,gin_am_secure TO gin_am_reader",
				"CREATE TABLE gin_am_priv(a integer[],n integer)",
				"CREATE INDEX gin_am_priv_i ON gin_am_priv USING gin(a)",
				"CREATE INDEX gin_am_priv_bt ON gin_am_priv(n)",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO gin_am_priv VALUES('{1,1,2}',1),('{}',2),(NULL,3)", 3))
			var session string
			if err := c.QueryRowContext(p.Ctx, "SELECT session_user").Scan(&session); err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				for _, q := range []string{"RESET ROLE", "RESET search_path", "RESET application_name"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error("GIN privilege caller cleanup", q, err)
					}
				}
			}()
			state := func() {
				t.Helper()
				run(fixture.Select("SELECT current_user::text AS actor,session_user::text AS session", []string{"actor", "session"}, []string{"TEXT", "TEXT"}, [][]any{{"gin_am_reader", session}}))
				run(fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"gin_am_secure, public, pg_catalog"}}))
				run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"GIN checker caller"}}))
			}
			const healthy = "SELECT gin_am_secure.gin_index_check('public.gin_am_priv_i') AS ok"
			const nullInput = "SELECT gin_am_secure.gin_index_check(NULL::regclass) AS ok"
			denied := heapDiagnostic("42501", "permission denied for function gin_index_check")
			run(fixture.Exec("SET application_name='GIN checker caller'", 0))
			run(fixture.Exec("SET search_path=gin_am_secure,public,pg_catalog", 0))
			run(fixture.Exec("SET ROLE gin_am_reader", 0))
			heapBoth(t, p, b, c, heapError(healthy, denied))
			heapBoth(t, p, b, c, amcheckSelect(nullInput, true))
			state()
			run(fixture.Exec("RESET ROLE", 0))
			// A grant on an incumbent checker cannot authorize the new member.
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION gin_am_secure.bt_index_check(regclass) TO gin_am_reader", 0))
			run(fixture.Exec("SET ROLE gin_am_reader", 0))
			heapBoth(t, p, b, c, amcheckSelect("SELECT gin_am_secure.bt_index_check('public.gin_am_priv_bt') AS ok", false))
			heapBoth(t, p, b, c, heapError(healthy, denied))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION gin_am_secure.gin_index_check(regclass) TO gin_am_reader", 0))
			run(fixture.Exec("SET ROLE gin_am_reader", 0))
			heapBoth(t, p, b, c, amcheckSelect(healthy, false))
			heapBoth(t, p, b, c, heapError("SELECT a FROM public.gin_am_priv", heapDiagnostic("42501", "permission denied for table gin_am_priv")))
			wrongMethod := heapDiagnostic("0A000", `expected "gin" index as targets for verification`)
			wrongMethod.Detail = `Relation "gin_am_priv_bt" is a btree index.`
			heapBoth(t, p, b, c, heapError("SELECT gin_am_secure.gin_index_check('public.gin_am_priv_bt')", wrongMethod))
			state()
			q := "SELECT gin_am_secure.gin_index_check($1::regclass) AS ok"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, amcheckSelect(q, false, "public.gin_am_priv_i"))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION gin_am_secure.gin_index_check(regclass) FROM gin_am_reader", 0))
			run(fixture.Exec("SET ROLE gin_am_reader", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, heapError(q, denied, "public.gin_am_priv_i"))
			state()
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION gin_am_secure.gin_index_check(regclass) TO gin_am_reader", 0))
			run(fixture.Exec("REVOKE USAGE ON SCHEMA gin_am_secure FROM gin_am_reader", 0))
			run(fixture.Exec("SET ROLE gin_am_reader", 0))
			d := heapDiagnostic("42501", "permission denied for schema gin_am_secure")
			d.Position = "8"
			heapBoth(t, p, b, c, heapError(healthy, d))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA gin_am_secure TO gin_am_reader", 0))
			run(fixture.Exec("SET ROLE gin_am_reader", 0))
			heapBoth(t, p, b, c, amcheckSelect(healthy, false))
			state()
			run(fixture.Exec("RESET ROLE", 0))
			// Renaming preserves installed authority; a replacement spelling and
			// its PUBLIC grant do not transfer authority to the renamed member.
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION gin_am_secure.gin_index_check(regclass) FROM gin_am_reader", 0))
			run(fixture.Exec("ALTER FUNCTION gin_am_secure.gin_index_check(regclass) RENAME TO gin_installed", 0))
			run(fixture.Exec("CREATE FUNCTION gin_am_secure.gin_index_check(regclass) RETURNS integer LANGUAGE SQL AS $$ SELECT 42 $$", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION gin_am_secure.gin_index_check(regclass) TO gin_am_reader", 0))
			run(fixture.Exec("SET ROLE gin_am_reader", 0))
			heapBoth(t, p, b, c, fixture.Select("SELECT gin_am_secure.gin_index_check('public.gin_am_priv_i') AS ordinary", []string{"ordinary"}, []string{"INT4"}, [][]any{{int64(42)}}))
			heapBoth(t, p, b, c, heapError("SELECT gin_am_secure.gin_installed('public.gin_am_priv_i')", heapDiagnostic("42501", "permission denied for function gin_installed")))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION gin_am_secure.gin_installed(regclass) TO gin_am_reader", 0))
			run(fixture.Exec("SET ROLE gin_am_reader", 0))
			heapBoth(t, p, b, c, amcheckSelect("SELECT gin_am_secure.gin_installed('public.gin_am_priv_i') AS ok", false))
			state()
			run(fixture.Exec("RESET ROLE", 0))
		})
	})
	ginAmcheckACLProvenance(t)
	ginAmcheckDefaultPrivileges(t)
}

func ginAmcheckACLProvenance(t *testing.T) {
	t.Helper()
	t.Run("acl_provenance", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"gin_am_holder", "gin_am_third", "gin_am_public"})
		heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE EXTENSION amcheck", "CREATE ROLE gin_am_holder", "CREATE ROLE gin_am_third", "CREATE ROLE gin_am_public",
				"GRANT USAGE ON SCHEMA public TO gin_am_holder,gin_am_third,gin_am_public",
				"CREATE TABLE gin_am_acl(a integer[],n integer)",
				"CREATE INDEX gin_am_acl_i ON gin_am_acl USING gin(a)",
				"CREATE INDEX gin_am_acl_bt ON gin_am_acl(n)",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO gin_am_acl VALUES('{1,1}',1),('{}',2),(NULL,3)", 3))
			var owner string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			actor := func(role string, body func()) {
				t.Helper()
				run(fixture.Exec("SET ROLE "+role, 0))
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil {
						t.Error("reset GIN ACL actor", err)
					}
				}()
				body()
			}
			check := func(phase string) {
				t.Helper()
				grants := [][]any{{owner, "gin_am_holder", "NO"}}
				if phase == "chain" {
					grants = [][]any{{owner, "gin_am_holder", "YES"}, {"gin_am_holder", "gin_am_third", "NO"}}
				}
				if phase == "public" {
					grants = append(grants, []any{owner, "PUBLIC", "NO"})
				}
				if phase == "empty" {
					grants = nil
				}
				s := fixture.Select("SELECT grantor::text,grantee::text,is_grantable::text FROM information_schema.routine_privileges WHERE routine_schema='public' AND routine_name='gin_index_check' AND grantee IN ('gin_am_holder','gin_am_third','gin_am_public','PUBLIC')", []string{"grantor", "grantee", "is_grantable"}, []string{"TEXT", "TEXT", "TEXT"}, grants)
				s.Bag = true
				heapBoth(t, p, b, c, s)
				for _, role := range []string{"gin_am_holder", "gin_am_third", "gin_am_public"} {
					actor(role, func() {
						q := "SELECT public.gin_index_check('public.gin_am_acl_i') AS ok"
						allowed := phase != "empty" && (role == "gin_am_holder" || phase == "chain" && role == "gin_am_third" || phase == "public")
						s := amcheckSelect(q, false)
						if !allowed {
							s = heapError(q, heapDiagnostic("42501", "permission denied for function gin_index_check"))
						}
						heapBoth(t, p, b, c, s)
						heapBoth(t, p, b, c, amcheckSelect("SELECT public.gin_index_check(NULL::regclass) AS ok", true))
						// Replay must not apply slot8's grants to an incumbent slot.
						heapBoth(t, p, b, c, heapError("SELECT public.bt_index_check('public.gin_am_acl_bt')", heapDiagnostic("42501", "permission denied for function bt_index_check")))
					})
				}
			}
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION public.gin_index_check(regclass) TO gin_am_holder WITH GRANT OPTION", 0))
			actor("gin_am_holder", func() {
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION public.gin_index_check(regclass) TO gin_am_third", 0))
			})
			check("chain")
			if !b.PG {
				ginAmcheckReplayACL(t, p)
				check("chain")
			}
			d := heapDiagnostic("2BP01", "dependent privileges exist")
			d.Hint = "Use CASCADE to revoke them too."
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION public.gin_index_check(regclass) FROM gin_am_holder RESTRICT", d)
				s.Prepared = prepared
				run(s)
				check("chain")
			}
			for _, phase := range []struct{ name, query string }{
				{"cascade", "REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION public.gin_index_check(regclass) FROM gin_am_holder CASCADE"},
				{"public", "GRANT EXECUTE ON FUNCTION public.gin_index_check(regclass) TO PUBLIC"},
				{"cascade", "REVOKE EXECUTE ON FUNCTION public.gin_index_check(regclass) FROM PUBLIC"},
				{"empty", "REVOKE ALL ON FUNCTION public.gin_index_check(regclass) FROM gin_am_holder," + owner},
			} {
				run(fixture.Exec(phase.query, 0))
				check(phase.name)
				if !b.PG {
					ginAmcheckReplayACL(t, p)
					check(phase.name)
				}
			}
		})
	})
}

func ginAmcheckDefaultPrivileges(t *testing.T) {
	t.Helper()
	t.Run("installation_defaults", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"gin_am_default", "gin_am_no_default"})
		heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE ROLE gin_am_default", "CREATE ROLE gin_am_no_default",
				"GRANT USAGE ON SCHEMA public TO gin_am_default,gin_am_no_default",
				"ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO gin_am_default",
			} {
				run(fixture.Exec(q, 0))
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				for _, q := range []string{"RESET ROLE", "ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM gin_am_default"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error("GIN default ACL cleanup", q, err)
					}
				}
			}()
			run(fixture.Exec("CREATE EXTENSION amcheck", 0))
			run(fixture.Exec("CREATE TABLE gin_am_default_heap(a integer[])", 0))
			run(fixture.Exec("CREATE INDEX gin_am_default_i ON gin_am_default_heap USING gin(a)", 0))
			run(fixture.Exec("INSERT INTO gin_am_default_heap VALUES('{2,2}'),(NULL)", 2))
			var owner string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
				t.Fatal(err)
			}
			check := func(allowed bool) {
				t.Helper()
				grants := [][]any{{owner, owner, "YES"}}
				if allowed {
					grants = append(grants, []any{owner, "gin_am_default", "NO"})
				}
				s := fixture.Select("SELECT grantor::text,grantee::text,is_grantable::text FROM information_schema.routine_privileges WHERE routine_schema='public' AND routine_name='gin_index_check'", []string{"grantor", "grantee", "is_grantable"}, []string{"TEXT", "TEXT", "TEXT"}, grants)
				s.Bag = true
				heapBoth(t, p, b, c, s)
				heapBoth(t, p, b, c, fixture.Select("SELECT p.proacl=i.initprivs AS initial_acl FROM pg_proc p JOIN pg_init_privs i ON i.objoid=p.oid AND i.classoid='pg_proc'::regclass AND i.objsubid=0 AND i.privtype='e' WHERE p.oid='public.gin_index_check(regclass)'::regprocedure", []string{"initial_acl"}, []string{"BOOL"}, [][]any{{allowed}}))
				run(fixture.Exec("SET ROLE gin_am_no_default", 0))
				heapBoth(t, p, b, c, heapError("SELECT public.gin_index_check('public.gin_am_default_i')", heapDiagnostic("42501", "permission denied for function gin_index_check")))
				heapBoth(t, p, b, c, amcheckSelect("SELECT public.gin_index_check(NULL::regclass) AS ok", true))
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("SET ROLE gin_am_default", 0))
				s = amcheckSelect("SELECT public.gin_index_check('public.gin_am_default_i') AS ok", false)
				if !allowed {
					s = heapError(s.Query, heapDiagnostic("42501", "permission denied for function gin_index_check"))
				}
				heapBoth(t, p, b, c, s)
				heapBoth(t, p, b, c, heapError("SELECT a FROM public.gin_am_default_heap", heapDiagnostic("42501", "permission denied for table gin_am_default_heap")))
				run(fixture.Exec("RESET ROLE", 0))
			}
			check(true)
			// Installation has already copied the default grant into both the
			// live member ACL and its initial history. Remove only the independent
			// default declaration; replay must preserve those installed grants.
			run(fixture.Exec("ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM gin_am_default", 0))
			check(true)
			if !b.PG {
				ginAmcheckReplayACL(t, p)
				check(true)
			}
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION public.gin_index_check(regclass) FROM gin_am_default", 0))
			check(false)
			if !b.PG {
				ginAmcheckReplayACL(t, p)
				check(false)
			}
		})
	})
}

func ginAmcheckReplayACL(t *testing.T, p *fixture.Pair) {
	t.Helper()
	var snapshot bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil || snapshot.Len() == 0 {
		t.Fatal("GIN ACL Dump", err, snapshot.Len())
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
		t.Fatal("GIN ACL Restore", err)
	}
}
