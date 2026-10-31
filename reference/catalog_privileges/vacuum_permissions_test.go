package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVacuumPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"vacuum_actor", "vacuum_group", "vacuum_owner"})
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner := vacuumConn(t, p, b)
		run := func(s fixture.Step) { vacuumRun(t, p, b, owner, s) }
		for _, q := range []string{"CREATE ROLE vacuum_actor", "CREATE ROLE vacuum_group", "CREATE ROLE vacuum_owner", "CREATE TABLE vacuum_acl(id INT)", "GRANT USAGE ON SCHEMA public TO vacuum_actor,vacuum_group,vacuum_owner"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO vacuum_acl VALUES(1),(2)", 2))
		run(fixture.Exec("DELETE FROM vacuum_acl WHERE id=1", 1))
		var notices []fixture.Diagnostic
		_, actor, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		arun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, actor, rec, s) }
		arun(fixture.Exec("SET ROLE vacuum_actor", 0))
		noSelect := fixture.ImplicitError("SELECT id FROM vacuum_acl", fixture.Diagnostic{Code: "42501", Message: "permission denied for table vacuum_acl"})
		noSelect.SelectRows = true
		arun(noSelect)
		q := "VACUUM vacuum_acl"
		h := vacuumHeld(t, p, actor, q)
		if len(notices) != 0 {
			t.Fatalf("Prepare produced notices %#v", notices)
		}
		count := int64(0)
		attempt := func(allowed bool) {
			notices = nil
			fixture.RunStep(t, p.Ctx, h, rec, fixture.Exec(q, 0))
			if allowed {
				count++
				if len(notices) != 0 {
					t.Fatalf("authorized warnings %#v", notices)
				}
			} else {
				vacuumWarning(t, notices, "01000", `permission denied to vacuum "vacuum_acl", skipping it`)
			}
			// Poll the retained writer: observer reads cannot publish its batch.
			// Fresh writer commands reach genuine eligible idle boundaries.
			vacuumCount(t, p.Ctx, actor, "vacuum_acl", count)
			vacuumRows(t, p, b, owner, "vacuum_acl", 2)
			if count > 0 {
				run(fixture.Select("SELECT reltuples FROM pg_class WHERE oid='vacuum_acl'::regclass", []string{"reltuples"}, []string{"FLOAT4"}, [][]any{{float64(1)}}))
			}
		}
		attempt(false)
		for _, begin := range []string{"BEGIN", "BEGIN READ ONLY"} {
			arun(fixture.Exec(begin, 0))
			notices = nil
			fixture.RunStep(t, p.Ctx, h, rec, fixture.ImplicitError(q, fixture.Diagnostic{Code: "25001", Message: "VACUUM cannot run inside a transaction block"}))
			if len(notices) != 0 {
				t.Fatalf("ACL warning before transaction restriction %#v", notices)
			}
			arun(fixture.Exec("ROLLBACK", 0))
		}
		run(fixture.Exec("GRANT MAINTAIN ON vacuum_acl TO vacuum_actor", 0))
		attempt(true)
		run(fixture.Exec("REVOKE MAINTAIN ON vacuum_acl FROM vacuum_actor", 0))
		attempt(false)
		run(fixture.Exec("GRANT MAINTAIN ON vacuum_acl TO vacuum_group", 0))
		run(fixture.Exec("GRANT vacuum_group TO vacuum_actor", 0))
		attempt(true)
		run(fixture.Exec("REVOKE vacuum_group FROM vacuum_actor", 0))
		run(fixture.Exec("GRANT pg_maintain TO vacuum_actor", 0))
		attempt(true)
		run(fixture.Exec("REVOKE pg_maintain FROM vacuum_actor", 0))
		run(fixture.Exec("ALTER TABLE vacuum_acl OWNER TO vacuum_owner", 0))
		run(fixture.Exec("GRANT vacuum_owner TO vacuum_actor", 0))
		attempt(true)
		// MAINTAIN is independent of SELECT and row-security policy filtering.
		run(fixture.Exec("REVOKE vacuum_owner FROM vacuum_actor", 0))
		run(fixture.Exec("GRANT MAINTAIN ON vacuum_acl TO vacuum_actor", 0))
		run(fixture.Exec("ALTER TABLE vacuum_acl ENABLE ROW LEVEL SECURITY", 0))
		attempt(true)
		arun(fixture.Exec("RESET ROLE", 0))
		// The database-owner exception applies to ordinary nonshared relations.
		var database, originalOwner string
		if err := owner.QueryRowContext(p.Ctx, "SELECT datname,pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database()").Scan(&database, &originalOwner); err != nil {
			t.Fatal(err)
		}
		quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
		restoreOwner := "ALTER DATABASE " + quote(database) + " OWNER TO " + quote(originalOwner)
		run(fixture.Exec("REVOKE MAINTAIN ON vacuum_acl FROM vacuum_actor", 0))
		run(fixture.Exec("ALTER DATABASE "+quote(database)+" OWNER TO vacuum_actor", 0))
		defer func() {
			if _, err := owner.ExecContext(p.Ctx, restoreOwner); err != nil {
				t.Errorf("restore original database owner: %v", err)
			}
		}()
		arun(fixture.Exec("SET ROLE vacuum_actor", 0))
		attempt(true)
		notices = nil
		arun(fixture.Exec("VACUUM pg_catalog.pg_database", 0))
		vacuumWarning(t, notices, "01000", `permission denied to vacuum "pg_database", skipping it`)
		arun(fixture.Exec("RESET ROLE", 0))
	})
	t.Run("unsupported_relation_warning", func(t *testing.T) {
		vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			var notices []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			for _, q := range []string{"CREATE VIEW vacuum_view AS SELECT 1 AS id", "CREATE SEQUENCE vacuum_seq", "CREATE TABLE vacuum_index_table(id INT)", "CREATE INDEX vacuum_index ON vacuum_index_table(id)"} {
				fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec(q, 0))
			}
			for _, name := range []string{"vacuum_view", "vacuum_seq", "vacuum_index"} {
				notices = nil
				fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec("VACUUM "+name, 0))
				vacuumWarning(t, notices, "01000", `skipping "`+name+`" --- cannot vacuum non-tables or special system tables`)
			}
		})
	})
}
