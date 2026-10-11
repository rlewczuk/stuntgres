package reference_test

import (
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCollationSeedAlterLifecycle(t *testing.T) {
	dsn := "reference/" + t.Name()
	fixture.RoleReferenceCleanup(t, []string{"coll_seed_owner"})
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.SchemaReferenceCleanup(t, "coll_seed_schema")
		fixture.CollationExec(t, p, b, `CREATE USER coll_seed_owner`, `CREATE SCHEMA coll_seed_schema AUTHORIZATION coll_seed_owner`, `CREATE COLLATION coll_seed_bootstrap_probe FROM pg_catalog."C"`)
		originalOwnerID := fixture.CollationOwnerOID(t, b.DB, "C")
		if originalOwnerID != fixture.CollationOwnerOID(t, b.DB, "coll_seed_bootstrap_probe") {
			t.Fatal("fixture actor is not actual bootstrap seed owner")
		}
		var originalOwner string
		if err := b.DB.QueryRowContext(p.Ctx, `SELECT current_user`).Scan(&originalOwner); err != nil {
			t.Fatal(err)
		}
		if b.PG {
			fixture.CollationExec(t, p, b, `ALTER ROLE coll_seed_owner PASSWORD 'role-core-fixture'`)
		}
		actor := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), b.PG, dsn, "coll_seed_owner")
		result, err := actor.ExecContext(p.Ctx, `CREATE COLLATION coll_seed_schema.coll_seed_owner_probe FROM pg_catalog."C"`)
		fixture.DeleteReferenceCount(t, result, err, 0)
		transferredOwnerID := fixture.CollationOwnerOID(t, b.DB, "coll_seed_owner_probe")
		if transferredOwnerID == originalOwnerID {
			t.Fatal("ordinary authenticated creator adopted bootstrap identity")
		}
		ownerSQL := `"` + strings.ReplaceAll(originalOwner, `"`, `""`) + `"`
		// Restore every possible intermediate autocommit state even on a fatal assertion.
		t.Cleanup(func() {
			for _, q := range []string{
				`ALTER COLLATION pg_catalog."C" OWNER TO ` + ownerSQL,
				`ALTER COLLATION pg_catalog.coll_seed_c OWNER TO ` + ownerSQL,
				`ALTER COLLATION coll_seed_schema.coll_seed_c OWNER TO ` + ownerSQL,
				`ALTER COLLATION coll_seed_schema.coll_seed_c SET SCHEMA pg_catalog`,
				`ALTER COLLATION pg_catalog.coll_seed_c RENAME TO "C"`,
			} {
				_, _ = b.DB.ExecContext(p.Ctx, q)
			}
		})
		conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		defer conn.ExecContext(p.Ctx, `ROLLBACK`)
		run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		exec := func(queries ...string) {
			t.Helper()
			for _, q := range queries {
				run(fixture.Exec(q, 0))
			}
		}
		metadata := func(name string, owner int64) fixture.Step {
			return fixture.Select(`SELECT collname,collprovider,collencoding,collowner::bigint AS owner FROM pg_catalog.pg_collation WHERE oid=950`, []string{"collname", "collprovider", "collencoding", "owner"}, []string{"NAME", "CHAR", "INT4", "INT8"}, [][]any{{name, "c", int64(-1), owner}})
		}
		mutations := []string{`ALTER COLLATION pg_catalog."C" RENAME TO coll_seed_c`, `ALTER COLLATION pg_catalog.coll_seed_c SET SCHEMA coll_seed_schema`, `ALTER COLLATION coll_seed_schema.coll_seed_c OWNER TO coll_seed_owner`}
		exec(`BEGIN`)
		if b.PG {
			exec(mutations...)
			run(metadata("coll_seed_c", transferredOwnerID))
			run(fixture.Select(`SELECT upper('é' COLLATE coll_seed_schema.coll_seed_c) AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"é"}}))
		} else {
			run(fixture.ErrorDiagnostic(mutations[0], fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
		}
		exec(`ROLLBACK`)
		run(metadata("C", originalOwnerID))
		// Native DDL is autocommit-only; successful seed metadata changes must still
		// preserve the pinned identity and immutable execution capability.
		exec(mutations...)
		run(metadata("coll_seed_c", transferredOwnerID))
		value := fixture.Select(`SELECT upper('é' COLLATE coll_seed_schema.coll_seed_c) AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"é"}})
		run(value)
		run(fixture.Prepared(value))
		exec(`ALTER COLLATION coll_seed_schema.coll_seed_c OWNER TO `+ownerSQL, `ALTER COLLATION coll_seed_schema.coll_seed_c SET SCHEMA pg_catalog`, `ALTER COLLATION pg_catalog.coll_seed_c RENAME TO "C"`)
		run(metadata("C", originalOwnerID))
		run(fixture.ErrorDiagnostic(`DROP COLLATION pg_catalog."C"`, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: `cannot drop collation "C" because it is required by the database system`}))
		fixture.CollationExec(t, p, b, `DROP COLLATION coll_seed_bootstrap_probe`, `DROP SCHEMA coll_seed_schema CASCADE`)
	})
}
