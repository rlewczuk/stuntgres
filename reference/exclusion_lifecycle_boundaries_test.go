package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceExclusionModeIdentity(t *testing.T) {
	exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		c := concurrentIndexConn(t, p, b)
		run := func(s referenceStep) { exclusionReferenceRun(t, p, b, c, s) }
		run(referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE)", 0))
		run(referenceExec("INSERT INTO t VALUES(1)", 1))
		run(referenceExec("BEGIN", 0))
		run(referenceExec("SET CONSTRAINTS ex DEFERRED", 0))
		run(referenceExec("SET CONSTRAINTS ALL IMMEDIATE", 0))
		run(referenceExec("SAVEPOINT s", 0))
		run(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
		run(referenceExec("ROLLBACK TO s", 0))
		run(referenceExec("SET CONSTRAINTS ex DEFERRED", 0))
		run(referenceExec("ALTER TABLE t DROP CONSTRAINT ex", 0))
		run(referenceExec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE INITIALLY IMMEDIATE", 0))
		// Same name, new incarnation: old named mode must not attach to it.
		run(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
		run(referenceExec("ROLLBACK", 0))
		run(exclusionReferenceRows("SELECT v FROM t", 1))
	})
}

func TestReferenceExclusionPreparedPeerOutcome(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit=%t", commit), func(t *testing.T) {
			exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, peer := referenceLockConnections(t, p.ctx, b.db)
				run := func(x referenceExecutor, s referenceStep) { exclusionReferenceRun(t, p, b, x, s) }
				const gid = "exclusion_peer_outcome"
				preparedReferenceCleanup(t, b, gid)
				run(a, referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =))", 0))
				run(a, referenceExec("BEGIN", 0))
				run(a, referenceExec("INSERT INTO t VALUES(1)", 1))
				run(a, referenceExec("PREPARE TRANSACTION '"+gid+"'", 0))
				run(peer, referenceExec("SET lock_timeout='100ms'", 0))
				outcome := referenceRowExecute(p.ctx, peer, "INSERT INTO t VALUES(1)", false)
				if outcome.result != nil {
					t.Fatal("timed-out exclusion insert returned result")
				}
				exclusionReferenceWaitDiagnostic(t, outcome.err, "55P03", "canceling statement due to lock timeout", b.pg)
				run(peer, referenceExec("SET lock_timeout=0", 0))
				command := "ROLLBACK PREPARED"
				if commit {
					command = "COMMIT PREPARED"
				}
				run(a, referenceExec(command+" '"+gid+"'", 0))
				if commit {
					run(peer, referenceErrorDiagnostic("INSERT INTO t VALUES(1)", exclusionReferenceConflict("t", "ex", "v", "1", "1")))
				} else {
					run(peer, referenceExec("INSERT INTO t VALUES(1)", 1))
				}
				run(peer, exclusionReferenceRows("SELECT v FROM t", 1))
			})
		})
	}
}

func TestReferenceExclusionAlterOwnerAdmission(t *testing.T) {
	roleReferenceCleanup(t, []string{"exclusion_nonowner"})
	exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		if !b.pg {
			return
		} // Native ACL authority has independent internal coverage.
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		run := func(x referenceExecutor, s referenceStep) { exclusionReferenceRun(t, p, b, x, s) }
		run(a, referenceExec("CREATE ROLE exclusion_nonowner", 0))
		run(a, referenceExec("GRANT USAGE ON SCHEMA public TO exclusion_nonowner", 0))
		run(a, referenceExec("CREATE TABLE t(v INT)", 0))
		run(a, referenceExec("BEGIN", 0))
		run(a, exclusionReferenceRows("SELECT v FROM t"))
		run(peer, referenceExec("SET SESSION AUTHORIZATION exclusion_nonowner", 0))
		defer peer.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
		run(peer, referenceExec("SET lock_timeout='100ms'", 0))
		// Ownership rejection precedes any AX wait behind A's reader.
		run(peer, referencePrepared(referenceErrorDiagnostic("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", controlsReferenceDiagnostic("42501", "must be owner of table t"))))
		run(a, referenceExec("ROLLBACK", 0))
		run(peer, referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(peer, referenceExec("ALTER TABLE t ADD CONSTRAINT ex EXCLUDE(v WITH =)", 0))
	})
}

func TestReferenceExclusionSetConstraintsNoOwnerRequirement(t *testing.T) {
	roleReferenceCleanup(t, []string{"exclusion_modes"})
	exclusionReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		if !b.pg {
			return
		} // SQL grants are PostgreSQL's ACL control phase.
		c := concurrentIndexConn(t, p, b)
		run := func(s referenceStep) { exclusionReferenceRun(t, p, b, c, s) }
		run(referenceExec("CREATE ROLE exclusion_modes", 0))
		run(referenceExec("CREATE TABLE t(v INT,CONSTRAINT ex EXCLUDE(v WITH =) DEFERRABLE)", 0))
		run(referenceExec("GRANT USAGE ON SCHEMA public TO exclusion_modes", 0))
		run(referenceExec("GRANT INSERT ON t TO exclusion_modes", 0))
		run(referenceExec("SET SESSION AUTHORIZATION exclusion_modes", 0))
		defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
		run(referenceExec("BEGIN", 0))
		run(referenceExec("SET CONSTRAINTS ex DEFERRED", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(1)", 2))
		d := exclusionReferenceConflict("t", "ex", "v", "1", "1")
		d.Detail = "Key conflicts with existing key."
		run(referenceErrorDiagnostic("COMMIT", d))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(exclusionReferenceRows("SELECT v FROM t"))
	})
}
