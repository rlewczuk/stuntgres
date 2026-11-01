package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVacuumFullAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"full_actor", "full_owner", "full_group"})
			fixture.SchemaReferenceCleanup(t, "full_private")
			vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				admin := vacuumConn(t, p, b)
				run := func(s fixture.Step) { vacuumRun(t, p, b, admin, s) }
				for _, q := range []string{"CREATE ROLE full_actor", "CREATE ROLE full_owner", "CREATE ROLE full_group", "GRANT USAGE ON SCHEMA public TO full_actor,full_owner,full_group", "CREATE TABLE full_acl(id INT PRIMARY KEY)", "INSERT INTO full_acl VALUES(1),(2)", "DELETE FROM full_acl WHERE id=1"} {
					affected := int64(0)
					if strings.HasPrefix(q, "INSERT") {
						affected = 2
					}
					if strings.HasPrefix(q, "DELETE") {
						affected = 1
					}
					run(fixture.Exec(q, affected))
				}
				var notices []fixture.Diagnostic
				_, actor, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				f := &fullVacuumCase{t: t, p: p, b: b, c: admin}
				q := "VACUUM FULL full_acl"
				h := vacuumHeld(t, p, actor, q)
				execute := func(s fixture.Step) {
					if prepared {
						fixture.RunStep(t, p.Ctx, h, rec, s)
					} else {
						fixture.RunStep(t, p.Ctx, actor, rec, s)
					}
				}
				arun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, actor, rec, s) }
				arun(fixture.Exec("SET ROLE full_actor", 0))
				attempt := func(allowed bool) {
					before := f.locator("full_acl")
					notices = nil
					execute(fixture.Exec(q, 0))
					if allowed {
						f.rewritten("full_acl", before)
						if len(notices) != 0 {
							t.Fatal(notices)
						}
					} else {
						f.unchanged("full_acl", before)
						vacuumWarning(t, notices, "01000", `permission denied to vacuum "full_acl", skipping it`)
					}
					vacuumRows(t, p, b, admin, "full_acl", 2)
				}
				attempt(false)
				run(fixture.Exec("GRANT MAINTAIN ON full_acl TO full_actor", 0))
				attempt(true)
				run(fixture.Exec("REVOKE MAINTAIN ON full_acl FROM full_actor", 0))
				attempt(false)
				run(fixture.Exec("GRANT MAINTAIN ON full_acl TO full_group", 0))
				run(fixture.Exec("GRANT full_group TO full_actor", 0))
				attempt(true)
				run(fixture.Exec("REVOKE full_group FROM full_actor", 0))
				run(fixture.Exec("ALTER TABLE full_acl OWNER TO full_owner", 0))
				run(fixture.Exec("GRANT full_owner TO full_actor", 0))
				attempt(true)
				arun(fixture.Exec("SET ROLE full_owner", 0))
				attempt(true)
				arun(fixture.Exec("RESET ROLE", 0))
				attempt(true)
				// Held execution resolves the current schema ACL, never Prepare's.
				run(fixture.Exec("CREATE SCHEMA full_private", 0))
				run(fixture.Exec("CREATE TABLE full_private.denied(id INT)", 0))
				arun(fixture.Exec("SET ROLE full_actor", 0))
				denied := fixture.ErrorDiagnostic("VACUUM FULL full_private.denied", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema full_private"))
				if prepared {
					vacuumRun(t, p, b, vacuumHeld(t, p, actor, denied.Query), denied)
				} else {
					arun(denied)
				}
				arun(fixture.Exec("RESET ROLE", 0))
				// Revoke an inherited maintenance role during a real AX wait.
				// Role membership changes need no target relation grant.
				run(fixture.Exec("REVOKE full_owner FROM full_actor", 0))
				run(fixture.Exec("GRANT full_group TO full_actor", 0))
				arun(fixture.Exec("SET ROLE full_actor", 0))
				holder := vacuumConn(t, p, b)
				vacuumRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
				defer holder.ExecContext(p.Ctx, "ROLLBACK")
				vacuumRows(t, p, b, holder, "full_acl", 2)
				beforeWait := f.locator("full_acl")
				waiter, blocker := fixture.LockPID(t, p.Ctx, actor), fixture.LockPID(t, p.Ctx, holder)
				waiting := &fullVacuumCase{t: t, p: p, b: b, c: actor, prepared: prepared, held: map[string]fixture.HeldStatement{q: h}}
				notices = nil
				_, join, finished := waiting.start(q)
				fixture.RelationLockPending(t, p, b, waiter, blocker, beforeWait.oid, "AccessExclusiveLock", finished)
				run(fixture.Exec("REVOKE full_group FROM full_actor", 0))
				vacuumRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
				out := join()
				fixture.DeleteReferenceCount(t, out.Result, out.Err, 0)
				vacuumWarning(t, notices, "01000", `permission denied to vacuum "full_acl", skipping it`)
				f.unchanged("full_acl", beforeWait)
				arun(fixture.Exec("RESET ROLE", 0))
				// Database-owner maintenance is independent of table ownership.
				var database, originalOwner string
				if err := admin.QueryRowContext(p.Ctx, "SELECT datname,pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database()").Scan(&database, &originalOwner); err != nil {
					t.Fatal(err)
				}
				quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
				restoreOwner := "ALTER DATABASE " + quote(database) + " OWNER TO " + quote(originalOwner)
				run(fixture.Exec("ALTER DATABASE "+quote(database)+" OWNER TO full_actor", 0))
				defer func() {
					if _, err := admin.ExecContext(p.Ctx, restoreOwner); err != nil {
						t.Error(err)
					}
				}()
				arun(fixture.Exec("SET ROLE full_actor", 0))
				attempt(true)
				arun(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec(restoreOwner, 0))
				for _, stmt := range []string{"CREATE VIEW full_view AS SELECT 1 AS id", "CREATE SEQUENCE full_seq", "CREATE INDEX full_named_index ON full_acl(id)"} {
					run(fixture.Exec(stmt, 0))
				}
				for _, name := range []string{"full_view", "full_seq", "full_named_index"} {
					notices = nil
					q := "VACUUM FULL " + name
					if prepared {
						fixture.RunStep(t, p.Ctx, vacuumHeld(t, p, actor, q), rec, fixture.Exec(q, 0))
					} else {
						arun(fixture.Exec(q, 0))
					}
					vacuumWarning(t, notices, "01000", `skipping "`+name+`" --- cannot vacuum non-tables or special system tables`)
				}
				// Foreign TEMP skips without pretending to own the peer's heap.
				run(fixture.Exec("CREATE TEMP TABLE full_peer_temp(id INT)", 0))
				var ns string
				if err := admin.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='full_peer_temp'::regclass").Scan(&ns); err != nil {
					t.Fatal(err)
				}
				notices = nil
				arun(fixture.Exec("VACUUM FULL "+ns+".full_peer_temp", 0))
				if len(notices) != 0 {
					t.Fatal(notices)
				}
				f.emptyProgress()
			})
		})
	}
}

func TestReferenceVacuumFullIndexRebuild(t *testing.T) {
	fullVacuumCases(t, func(f *fullVacuumCase) {
		// A real domain recipe executes again during rebuilding, unlike lazy
		// VACUUM's produced-key remap. A sequence independently counts rows.
		f.run(fixture.Exec("CREATE SEQUENCE full_recipe_calls", 0))
		f.run(fixture.Exec("CREATE DOMAIN full_recipe_domain AS INT CHECK(nextval('full_recipe_calls')>0)", 0))
		f.run(fixture.Exec("CREATE TABLE full_recipe(id INT)", 0))
		f.run(fixture.Exec("INSERT INTO full_recipe VALUES(1),(2),(3)", 3))
		f.run(fixture.Exec("CREATE INDEX full_recipe_ix ON full_recipe(((id::full_recipe_domain)::INT))", 0))
		var calls int64
		if err := f.c.QueryRowContext(f.p.Ctx, "SELECT last_value FROM full_recipe_calls").Scan(&calls); err != nil {
			f.t.Fatal(err)
		}
		if calls != 3 {
			f.t.Fatal("initial recipe evaluations", calls)
		}
		f.run(fixture.Exec("DELETE FROM full_recipe WHERE id=1", 1))
		before, ix := f.locator("full_recipe"), f.locator("full_recipe_ix")
		f.command(fixture.Exec("VACUUM (FULL,INDEX_CLEANUP OFF) full_recipe", 0))
		f.rewritten("full_recipe", before)
		f.rewritten("full_recipe_ix", ix)
		f.run(fixture.Select("SELECT last_value FROM full_recipe_calls", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(5)}}))
		f.run(fixture.Exec("CREATE FUNCTION full_recipe_fail(i INT) RETURNS INT LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN RETURN i; END $$", 0))
		f.run(fixture.Exec("CREATE INDEX full_recipe_failure ON full_recipe(full_recipe_fail(id))", 0))
		f.run(fixture.Exec("CREATE OR REPLACE FUNCTION full_recipe_fail(i INT) RETURNS INT LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN RAISE EXCEPTION 'full recipe rejected'; END $$", 0))
		before, ix = f.locator("full_recipe"), f.locator("full_recipe_failure")
		want := fixture.ControlsReferenceDiagnostic("P0001", "full recipe rejected")
		want.Context = "PL/pgSQL function full_recipe_fail(integer) line 1 at RAISE"
		f.command(fixture.ErrorDiagnostic("VACUUM FULL full_recipe", want))
		f.unchanged("full_recipe", before)
		f.unchanged("full_recipe_failure", ix)
		vacuumRows(f.t, f.p, f.b, f.c, "full_recipe", 2, 3)
		for _, q := range []string{
			"CREATE ROLE full_safe_owner", "CREATE ROLE full_safe_actor",
			"GRANT USAGE ON SCHEMA public TO full_safe_owner,full_safe_actor",
			"CREATE TABLE full_safe(id INT)", "CREATE FUNCTION full_safe_key(i INT) RETURNS INT LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN RETURN i; END $$",
			"CREATE INDEX full_safe_ix ON full_safe(full_safe_key(id))",
			"ALTER FUNCTION full_safe_key(INT) OWNER TO full_safe_owner",
			"ALTER TABLE full_safe OWNER TO full_safe_owner",
			"REVOKE EXECUTE ON FUNCTION full_safe_key(INT) FROM PUBLIC",
			"GRANT MAINTAIN ON full_safe TO full_safe_actor",
		} {
			f.run(fixture.Exec(q, 0))
		}
		f.run(fixture.Exec("INSERT INTO full_safe VALUES(1)", 1))
		f.run(fixture.Exec("CREATE OR REPLACE FUNCTION full_safe_key(i INT) RETURNS INT LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN IF current_user <> 'full_safe_owner' THEN RAISE EXCEPTION 'wrong maintenance owner'; END IF; IF current_setting('search_path') <> 'pg_catalog, pg_temp' THEN RAISE EXCEPTION 'unsafe maintenance path'; END IF; RETURN i; END $$", 0))
		safeBefore := f.locator("full_safe")
		f.run(fixture.Exec("SET ROLE full_safe_actor", 0))
		f.run(fixture.Exec("SET search_path=public,pg_catalog", 0))
		f.command(fixture.Exec("VACUUM FULL full_safe", 0))
		f.run(fixture.Select("SELECT current_user,current_setting('search_path') AS path", []string{"current_user", "path"}, []string{"NAME", "TEXT"}, [][]any{{"full_safe_actor", "public, pg_catalog"}}))
		f.run(fixture.Exec("RESET ROLE", 0))
		f.rewritten("full_safe", safeBefore)
		f.emptyProgress()
	}, "full_safe_owner", "full_safe_actor")
}
