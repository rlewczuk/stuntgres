package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateRestartAuthorization(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "restart_owner")
	truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
		truncateExec(t, f, f.A, "CREATE ROLE restart_actor", 0)
		defer func() {
			f.A.ExecContext(p.Ctx, "ROLLBACK")
			f.A.ExecContext(p.Ctx, "RESET ROLE")
			f.A.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS restart_owner CASCADE")
			f.A.ExecContext(p.Ctx, "DROP OWNED BY restart_actor")
			f.A.ExecContext(p.Ctx, "DROP ROLE restart_actor")
		}()
		truncateExec(t, f, f.A, "GRANT USAGE ON SCHEMA public TO restart_actor", 0)
		truncateExec(t, f, f.A, "CREATE SCHEMA restart_owner AUTHORIZATION restart_actor", 0)
		truncateExec(t, f, f.A, "SET ROLE restart_actor", 0)
		truncateExec(t, f, f.A, "CREATE TABLE restart_owner.own_t(id SERIAL)", 0)
		truncateExec(t, f, f.A, "INSERT INTO restart_owner.own_t DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "REVOKE ALL ON SEQUENCE restart_owner.own_t_id_seq FROM restart_actor", 0)
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateRestartSeed(t, f)
		q := "TRUNCATE restart_owner.own_t,t RESTART IDENTITY"
		x := truncateRestartRoute(t, p, f.A, q, held)
		truncateExec(t, f, f.A, "SET ROLE restart_actor", 0)
		// Prepare was authorized; execution must consult the current role/table ACL.
		truncateRestartError(t, f, x, q, truncateWant("42501", "permission denied for table t"))
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateExec(t, f, f.A, "GRANT TRUNCATE ON t TO restart_actor", 0)
		for _, grant := range []bool{false, true} {
			if grant {
				truncateExec(t, f, f.A, "GRANT USAGE,UPDATE,SELECT ON SEQUENCE t_id_seq TO restart_actor", 0)
			}
			truncateExec(t, f, f.A, "SET ROLE restart_actor", 0)
			truncateRestartError(t, f, x, q, truncateWant("42501", "must be owner of sequence t_id_seq"))
			truncateExec(t, f, f.A, "RESET ROLE", 0)
			truncateRows(t, f, f.A, "t", 1, 2)
			truncateRows(t, f, f.A, "restart_owner.own_t", 1)
			truncateRestartState(t, f, f.A, "t_id_seq", 2, true)
			truncateRestartState(t, f, f.A, "restart_owner.own_t_id_seq", 1, true)
		}
		// Ownership, not ordinary UPDATE/USAGE/SELECT, permits restart.
		ownQ := "TRUNCATE restart_owner.own_t RESTART IDENTITY"
		own := truncateRestartRoute(t, p, f.A, ownQ, held)
		truncateExec(t, f, f.A, "SET ROLE restart_actor", 0)
		truncateExec(t, f, own, ownQ, 0)
		truncateExec(t, f, f.A, "RESET ROLE", 0)
		truncateRestartState(t, f, f.A, "restart_owner.own_t_id_seq", 1, false)
		truncateRows(t, f, f.A, "restart_owner.own_t")
		// Exact missing-name diagnostic, preserving all earlier roots/generators.
		missingQ := "TRUNCATE t,missing RESTART IDENTITY"
		truncateRestartError(t, f, truncateRestartRoute(t, p, f.A, missingQ, held), missingQ, truncateWant("42P01", `relation "missing" does not exist`))
		truncateRows(t, f, f.A, "t", 1, 2)
		truncateRestartState(t, f, f.A, "t_id_seq", 2, true)
		// Restore caller authority; the original held handle is still usable.
		truncateExec(t, f, x, q, 0)
		truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
		truncateExec(t, f, f.A, "INSERT INTO restart_owner.own_t DEFAULT VALUES", 1)
		truncateRows(t, f, f.A, "t", 1)
		truncateRows(t, f, f.A, "restart_owner.own_t", 1)
	})
}
