package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleTransactionalSessionAuthorization(t *testing.T) {
	roleTransactionCases(t, []string{"ri_a", "ri_b", "ri_super"}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held bool, mode string, commit bool) {
		run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
		bootstrap := "postgres"
		if b.PG {
			bootstrap = "stuntgres"
		}
		for _, q := range []string{"CREATE ROLE ri_a LOGIN", "CREATE ROLE ri_b", "CREATE ROLE ri_super SUPERUSER", "GRANT ri_b,ri_super TO ri_a", "CREATE TABLE ri_protected(i INT)", "GRANT SELECT ON ri_protected TO ri_a", "GRANT USAGE ON SCHEMA public TO ri_a,ri_b"} {
			run(c, fixture.Exec(q, 0))
		}
		run(c, fixture.Exec("INSERT INTO ri_protected VALUES(7)", 1))
		e, finish := fixture.RoleTransactionBegin(t, p, b, c, mode)
		utility := func(q string) { fixture.RoleTransactionStep(t, p, b, e, held, fixture.Exec(q, 0)) }
		identity := func(session, current string) { run(e, fixture.RoleTransactionIdentity(session, current)) }
		read := fixture.Select("SELECT i FROM ri_protected", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}})
		if mode != "autocommit" {
			utility("SET LOCAL ROLE ri_b")
		}
		utility("SET SESSION AUTHORIZATION ri_a")
		identity("ri_a", "ri_a")
		run(e, read)
		run(b.DB, fixture.RoleTransactionIdentity(bootstrap, bootstrap))
		utility("SET ROLE ri_b")
		identity("ri_a", "ri_b")
		utility("SET ROLE NONE")
		identity("ri_a", "ri_a")
		utility("SET ROLE ri_b")
		utility("RESET ROLE")
		identity("ri_a", "ri_a")
		for _, reset := range []string{"SET SESSION AUTHORIZATION DEFAULT", "RESET SESSION AUTHORIZATION"} {
			utility(reset)
			identity(bootstrap, bootstrap)
			utility("SET SESSION AUTHORIZATION ri_a")
		}
		if mode != "autocommit" {
			utility("SAVEPOINT identity_outer")
			utility("SET SESSION AUTHORIZATION ri_b")
			identity("ri_b", "ri_b")
			utility("SAVEPOINT identity_inner")
			utility("RESET SESSION AUTHORIZATION")
			utility("ROLLBACK TO identity_inner")
			identity("ri_b", "ri_b")
			utility("RELEASE identity_inner")
			utility("ROLLBACK TO identity_outer")
			identity("ri_a", "ri_a")
			utility("SET SESSION AUTHORIZATION ri_b")
			utility("ROLLBACK TO identity_outer")
			identity("ri_a", "ri_a")
			utility("RELEASE identity_outer")
			// Failure must restore the marked identities, not resolve a fresh name.
			utility("SAVEPOINT identity_error")
			run(e, fixture.ErrorDiagnostic("SET ROLE ri_missing", fixture.ControlsReferenceDiagnostic("22023", `role "ri_missing" does not exist`)))
			run(e, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			utility("ROLLBACK TO identity_error")
			identity("ri_a", "ri_a")
			run(e, read)
			utility("RELEASE identity_error")
		}
		finish(commit)
		want := bootstrap
		if commit {
			want = "ri_a"
		}
		run(c, fixture.RoleTransactionIdentity(want, want))
		run(c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(c, fixture.RoleTransactionIdentity(bootstrap, bootstrap))
		// Session-only identity commands are legal even in a read-only block.
		readOnly, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { readOnly.Rollback() })
		fixture.RoleTransactionStep(t, p, b, readOnly, held, fixture.Exec("SET SESSION AUTHORIZATION ri_a", 0))
		run(readOnly, fixture.RoleTransactionIdentity("ri_a", "ri_a"))
		fixture.RoleTransactionStep(t, p, b, readOnly, held, fixture.Exec("SET ROLE ri_b", 0))
		run(readOnly, fixture.RoleTransactionIdentity("ri_a", "ri_b"))
		fixture.RoleTransactionStep(t, p, b, readOnly, held, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(readOnly, fixture.RoleTransactionIdentity(bootstrap, bootstrap))
		if err := readOnly.Rollback(); err != nil {
			t.Fatal(err)
		}
		// Authenticated identity, not effective SUPERUSER, controls this operation.
		pool := roleTransactionLoginPool(t, p, b, "ri_a")
		actor, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { actor.Close() })
		t.Cleanup(func() { actor.ExecContext(p.Ctx, "ROLLBACK") })
		physical, physicalFinish := fixture.RoleTransactionBegin(t, p, b, actor, mode)
		fixture.RoleTransactionStep(t, p, b, physical, held, fixture.Exec("SET ROLE ri_super", 0))
		run(physical, fixture.RoleTransactionIdentity("ri_a", "ri_super"))
		if mode != "autocommit" {
			run(physical, fixture.Exec("SAVEPOINT physical_mark", 0))
		}
		fixture.RoleTransactionStep(t, p, b, physical, held, fixture.ErrorDiagnostic("SET SESSION AUTHORIZATION ri_b", fixture.ControlsReferenceDiagnostic("42501", `permission denied to set session authorization "ri_b"`)))
		if mode != "autocommit" {
			run(physical, fixture.Exec("ROLLBACK TO physical_mark", 0))
		}
		run(physical, fixture.RoleTransactionIdentity("ri_a", "ri_super"))
		fixture.RoleTransactionStep(t, p, b, physical, held, fixture.Exec("SET SESSION AUTHORIZATION ri_a", 0))
		run(physical, fixture.RoleTransactionIdentity("ri_a", "ri_a"))
		fixture.RoleTransactionStep(t, p, b, physical, held, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(physical, fixture.RoleTransactionIdentity("ri_a", "ri_a"))
		physicalFinish(commit)
	})
}
