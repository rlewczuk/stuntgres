package reference_test

import (
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCollationSeedAlterLifecycle(t *testing.T) {
	dsn := "reference/" + t.Name()
	roleReferenceCleanup(t, []string{"coll_seed_owner"})
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		schemaReferenceCleanup(t, "coll_seed_schema")
		collationExec(t, p, b, `CREATE USER coll_seed_owner`, `CREATE SCHEMA coll_seed_schema AUTHORIZATION coll_seed_owner`, `CREATE COLLATION coll_seed_bootstrap_probe FROM pg_catalog."C"`)
		originalOwnerID := collationOwnerOID(t, b.db, "C")
		if originalOwnerID != collationOwnerOID(t, b.db, "coll_seed_bootstrap_probe") {
			t.Fatal("fixture actor is not actual bootstrap seed owner")
		}
		var originalOwner string
		if err := b.db.QueryRowContext(p.ctx, `SELECT current_user`).Scan(&originalOwner); err != nil {
			t.Fatal(err)
		}
		if b.pg {
			collationExec(t, p, b, `ALTER ROLE coll_seed_owner PASSWORD 'role-core-fixture'`)
		}
		actor := roleScenarioLoginPool(t, p.nativeConnector.Driver().(*stuntgres.Driver), b.pg, dsn, "coll_seed_owner")
		result, err := actor.ExecContext(p.ctx, `CREATE COLLATION coll_seed_schema.coll_seed_owner_probe FROM pg_catalog."C"`)
		deleteReferenceCount(t, result, err, 0)
		transferredOwnerID := collationOwnerOID(t, b.db, "coll_seed_owner_probe")
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
				_, _ = b.db.ExecContext(p.ctx, q)
			}
		})
		conn := sequenceCoreConn(t, p.ctx, b.db)
		defer conn.ExecContext(p.ctx, `ROLLBACK`)
		run := func(s referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		exec := func(queries ...string) {
			t.Helper()
			for _, q := range queries {
				run(referenceExec(q, 0))
			}
		}
		metadata := func(name string, owner int64) referenceStep {
			return referenceSelect(`SELECT collname,collprovider,collencoding,collowner::bigint AS owner FROM pg_catalog.pg_collation WHERE oid=950`, []string{"collname", "collprovider", "collencoding", "owner"}, []string{"NAME", "CHAR", "INT4", "INT8"}, [][]any{{name, "c", int64(-1), owner}})
		}
		mutations := []string{`ALTER COLLATION pg_catalog."C" RENAME TO coll_seed_c`, `ALTER COLLATION pg_catalog.coll_seed_c SET SCHEMA coll_seed_schema`, `ALTER COLLATION coll_seed_schema.coll_seed_c OWNER TO coll_seed_owner`}
		exec(`BEGIN`)
		if b.pg {
			exec(mutations...)
			run(metadata("coll_seed_c", transferredOwnerID))
			run(referenceSelect(`SELECT upper('é' COLLATE coll_seed_schema.coll_seed_c) AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"é"}}))
		} else {
			run(referenceErrorDiagnostic(mutations[0], referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
		}
		exec(`ROLLBACK`)
		run(metadata("C", originalOwnerID))
		// Native DDL is autocommit-only; successful seed metadata changes must still
		// preserve the pinned identity and immutable execution capability.
		exec(mutations...)
		run(metadata("coll_seed_c", transferredOwnerID))
		value := referenceSelect(`SELECT upper('é' COLLATE coll_seed_schema.coll_seed_c) AS v`, []string{"v"}, []string{"TEXT"}, [][]any{{"é"}})
		run(value)
		run(referencePrepared(value))
		exec(`ALTER COLLATION coll_seed_schema.coll_seed_c OWNER TO `+ownerSQL, `ALTER COLLATION coll_seed_schema.coll_seed_c SET SCHEMA pg_catalog`, `ALTER COLLATION pg_catalog.coll_seed_c RENAME TO "C"`)
		run(metadata("C", originalOwnerID))
		run(referenceErrorDiagnostic(`DROP COLLATION pg_catalog."C"`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: `cannot drop collation "C" because it is required by the database system`}))
		collationExec(t, p, b, `DROP COLLATION coll_seed_bootstrap_probe`, `DROP SCHEMA coll_seed_schema CASCADE`)
	})
}
