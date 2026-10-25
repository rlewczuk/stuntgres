package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckHeapIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"heap_reader"})
	fixture.SchemaReferenceCleanup(t, "heap_moved")
	heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		heapInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("INSERT INTO heap_t VALUES(1,'private')", 1))
		run(fixture.Exec("CREATE ROLE heap_reader", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO heap_reader", 0))
		heapBoth(t, p, b, c, heapEmpty("SELECT * FROM verify_heapam('heap_t')"))
		q := "SELECT * FROM verify_heapam($1::regclass)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		run(fixture.Exec("SET ROLE heap_reader", 0))
		t.Run("default-execute-denied", func(t *testing.T) {
			d := heapDiagnostic("42501", "permission denied for function verify_heapam")
			heapBoth(t, p, b, c, heapError(q, d, "heap_t"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, heapError(q, d, "heap_t"))
			// Installed EXECUTE admission precedes NULL/physical target checks.
			heapBoth(t, p, b, c, heapError("SELECT * FROM verify_heapam(NULL::regclass,false,true)", d))
		})
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION verify_heapam"+heapSignature+" TO heap_reader", 0))
		run(fixture.Exec("SET ROLE heap_reader", 0))
		t.Run("granted-without-select", func(t *testing.T) {
			d := heapDiagnostic("42501", "permission denied for table heap_t")
			heapBoth(t, p, b, c, heapError("SELECT * FROM heap_t", d))
			heapBoth(t, p, b, c, heapEmpty(q, "heap_t"))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, heapEmpty(q, "heap_t"))
		})
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION verify_heapam"+heapSignature+" FROM heap_reader", 0))
		run(fixture.Exec("SET ROLE heap_reader", 0))
		t.Run("held-current-acl", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, heapError(q, heapDiagnostic("42501", "permission denied for function verify_heapam"), "heap_t"))
		})
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("CREATE VIEW heap_identity_view AS SELECT * FROM verify_heapam('heap_t')", 0))
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='verify_heapam'").Scan(&oid); err != nil { t.Fatal(err) }
		run(fixture.Exec("ALTER FUNCTION verify_heapam"+heapSignature+" RENAME TO heap_verify", 0))
		run(fixture.Exec("CREATE SCHEMA heap_moved", 0))
		run(fixture.Exec("ALTER FUNCTION heap_verify"+heapSignature+" SET SCHEMA heap_moved", 0))
		t.Run("renamed-installed-identity", func(t *testing.T) {
			heapBoth(t, p, b, c, fixture.Select("SELECT oid::bigint AS identity FROM pg_proc WHERE proname='heap_verify'", []string{"identity"}, []string{"INT8"}, [][]any{{oid}}))
			heapBoth(t, p, b, c, heapEmpty("SELECT * FROM heap_moved.heap_verify('heap_t')"))
			heapBoth(t, p, b, c, heapEmpty("SELECT * FROM heap_identity_view"))
		})
		// Reinstall creates fresh authority, rather than reviving the renamed
		// member or its dependent stored view through matching spelling alone.
		run(fixture.Exec("DROP EXTENSION amcheck CASCADE", 0))
		run(fixture.Exec("CREATE EXTENSION amcheck SCHEMA heap_moved", 0))
		t.Run("fresh-install", func(t *testing.T) {
			heapBoth(t, p, b, c, heapEmpty("SELECT * FROM heap_moved.verify_heapam('heap_t')"))
			heapBoth(t, p, b, c, fixture.Select("SELECT proname FROM pg_proc WHERE proname='heap_verify'", []string{"proname"}, []string{"NAME"}, nil))
			heapBoth(t, p, b, c, fixture.Select("SELECT relname FROM pg_class WHERE relname='heap_identity_view'", []string{"relname"}, []string{"NAME"}, nil))
		})
	})
}
