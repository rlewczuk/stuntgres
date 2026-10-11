package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExclusionModeIdentity(t *testing.T) {
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET CONSTRAINTS ex DEFERRED", 0))
		run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
		run(fixture.Exec("SAVEPOINT s", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
		run(fixture.Exec("ROLLBACK TO s", 0))
		run(fixture.Exec("SET CONSTRAINTS ex DEFERRED", 0))
		run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT ex", 0))
		run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE INITIALLY IMMEDIATE", 0))
		// Same name, new incarnation: old named mode must not attach to it.
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.ExclusionReferenceRows("SELECT v FROM t", 1))
	})
}

func TestReferenceExclusionPreparedPeerOutcome(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit=%t", commit), func(t *testing.T) {
			fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(x fixture.Executor, s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, x, s) }
				const gid = "exclusion_peer_outcome"
				fixture.PreparedReferenceCleanup(t, b, gid)
				run(a, fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
				run(a, fixture.Exec("BEGIN", 0))
				run(a, fixture.Exec("INSERT INTO t VALUES(1)", 1))
				run(a, fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0))
				run(peer, fixture.Exec("SET lock_timeout='100ms'", 0))
				outcome := fixture.RowExecute(p.Ctx, peer, "INSERT INTO t VALUES(1)", false)
				if outcome.Result != nil {
					t.Fatal("timed-out exclusion insert returned result")
				}
				fixture.ExclusionReferenceWaitDiagnostic(t, outcome.Err, "55P03", "canceling statement due to lock timeout", b.PG)
				run(peer, fixture.Exec("SET lock_timeout=0", 0))
				command := "ROLLBACK PREPARED"
				if commit {
					command = "COMMIT PREPARED"
				}
				run(a, fixture.Exec(command+" '"+gid+"'", 0))
				if commit {
					run(peer, fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")))
				} else {
					run(peer, fixture.Exec("INSERT INTO t VALUES(1)", 1))
				}
				run(peer, fixture.ExclusionReferenceRows("SELECT v FROM t", 1))
			})
		})
	}
}

func TestReferenceExclusionAlterOwnerAdmission(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"exclusion_nonowner"})
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		if !b.PG {
			return
		} // Native ACL authority has independent internal coverage.
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, x, s) }
		run(a, fixture.Exec("CREATE ROLE exclusion_nonowner", 0))
		run(a, fixture.Exec("GRANT USAGE ON SCHEMA public TO exclusion_nonowner", 0))
		run(a, fixture.Exec("CREATE TABLE t(v INT)", 0))
		run(a, fixture.Exec("BEGIN", 0))
		run(a, fixture.ExclusionReferenceRows("SELECT v FROM t"))
		run(peer, fixture.Exec("SET SESSION AUTHORIZATION exclusion_nonowner", 0))
		defer peer.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run(peer, fixture.Exec("SET lock_timeout='100ms'", 0))
		// Ownership rejection precedes any AX wait behind A's reader.
		run(peer, fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", fixture.ControlsReferenceDiagnostic("42501", "must be owner of table t"))))
		run(a, fixture.Exec("ROLLBACK", 0))
		run(peer, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(peer, fixture.Exec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", 0))
	})
}

func TestReferenceExclusionSetConstraintsNoOwnerRequirement(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"exclusion_modes"})
	fixture.ExclusionReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		if !b.PG {
			return
		} // SQL grants are PostgreSQL's ACL control phase.
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, c, s) }
		run(fixture.Exec("CREATE ROLE exclusion_modes", 0))
		run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE)", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO exclusion_modes", 0))
		run(fixture.Exec("GRANT INSERT ON t TO exclusion_modes", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION exclusion_modes", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET CONSTRAINTS ex DEFERRED", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(1)", 2))
		d := fixture.ExclusionReferenceConflict("t", "ex", "v", "1", "1")
		d.Detail = "Key conflicts with existing key."
		run(fixture.ErrorDiagnostic("COMMIT", d))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.ExclusionReferenceRows("SELECT v FROM t"))
	})
}
