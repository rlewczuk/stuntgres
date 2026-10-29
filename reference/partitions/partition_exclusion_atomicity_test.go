package reference_test

import (
	"context"
	"database/sql/driver"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionExclusionAtomicity(t *testing.T) {
	partitionExclusionCase(t, "late_recursive_build", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
		run(fixture.Exec("CREATE TABLE pe_low PARTITION OF pe_root FOR VALUES FROM('10.0.0.0') TO('10.0.0.128')", 0))
		run(fixture.Exec("CREATE TABLE pe_other PARTITION OF pe_root DEFAULT", 0))
		run(fixture.Exec("ALTER TABLE pe_low ADD CONSTRAINT pe_local EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
		var oid, indexOID int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid,conindid FROM pg_constraint WHERE conrelid='pe_low'::regclass::OID AND conname='pe_local'").Scan(&oid, &indexOID); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)'),(2,'10.0.0.129','[1,5)'),(3,'10.0.0.129','empty'),(4,'10.0.0.129','[3,7)')", 4))
		d := fixture.ControlsReferenceDiagnostic("23P01", `could not create exclusion constraint "pe_other_tenant_during_excl"`)
		d.Schema, d.Table, d.Constraint = "public", "pe_other", "pe_other_tenant_during_excl"
		d.Detail = "Key (tenant, during)=(10.0.0.129, [1,5)) conflicts with key (tenant, during)=(10.0.0.129, [3,7))."
		run(fixture.ErrorDiagnostic("ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", d))
		run(fixture.Select("SELECT t.relname,c.conname,c.oid=$1::OID AS same_constraint,c.conindid=$2::OID AS same_index FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid WHERE t.relname IN ('pe_root','pe_low','pe_other') AND c.contype='x' ORDER BY t.relname,c.conname", []string{"relname", "conname", "same_constraint", "same_index"}, []string{"NAME", "NAME", "BOOL", "BOOL"}, [][]any{{"pe_low", "pe_local", true, true}}, oid, indexOID))
		run(partitionAdoptionGraph([][]any{partitionAdoptionRow("pe_low", "pe_local", nil, true, true, "i", true)}))
		run(partitionExclusionIDs(1, 2, 3, 4))
		run(partitionExclusionConflict("INSERT INTO pe_root VALUES(5,'10.0.0.1','[3,7)')", "pe_low", "pe_local", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
		// No hierarchy DELETE is introduced: repair existing data by detaching
		// the bad branch, then prove failed ADD released every generated name.
		run(fixture.Exec("ALTER TABLE pe_root DETACH PARTITION pe_other", 0))
		run(fixture.Exec("ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
		run(fixture.Select("SELECT c.conname FROM pg_constraint c WHERE c.conrelid='pe_low'::regclass::OID AND c.contype='x' ORDER BY c.conname", []string{"conname"}, []string{"NAME"}, [][]any{{"pe_local"}}))
		run(partitionAdoptionSame("pe_low", [2]int64{oid, indexOID}))
		run(partitionAdoptionGraph([][]any{
			partitionAdoptionRow("pe_low", "pe_local", "pe_ex", false, true, "i", true),
			partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", true),
		}))
	})
	partitionExclusionCase(t, "transaction_and_savepoint_rollback", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { s.Prepared = prepared; fixture.ExclusionReferenceRun(t, p, b, c, s) }
		partitionExclusionTree(run, "")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)')", 1))
		run(fixture.Exec("SAVEPOINT keep_first", 0))
		run(partitionExclusionConflict("INSERT INTO pe_root VALUES(2,'10.0.0.129','[1,5)'),(3,'10.0.0.1','[3,7)')", "pe_low", "pe_low_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
		run(fixture.Exec("ROLLBACK TO keep_first", 0))
		run(partitionExclusionIDs(1))
		run(fixture.Exec("INSERT INTO pe_root VALUES(2,'10.0.0.129','[1,5)')", 1))
		run(fixture.Exec("ROLLBACK", 0))
		run(partitionExclusionIDs())
		run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)'),(2,'10.0.0.129','[1,5)')", 2))
		run(partitionExclusionGraph())
	})
	t.Run("authorization", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pe_actor_direct", "pe_actor_prepared"})
		partitionExclusionCase(t, "denied_parent_and_write", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			c := fixture.ConcurrentIndexConn(t, p, b)
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) { s.Prepared = prepared; fixture.ExclusionReferenceRun(t, p, b, c, s) }
			partitionExclusionTree(run, "")
			actor := "pe_actor_direct"
			if prepared {
				actor = "pe_actor_prepared"
			}
			run(fixture.Exec("CREATE ROLE "+actor, 0))
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor, 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
			run(referenceRangeError("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", "42501", "must be owner of table pe_root", fixture.Diagnostic{}))
			run(referenceRangeError("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)')", "42501", "permission denied for table pe_root", fixture.Diagnostic{}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(partitionExclusionGraph())
			run(partitionExclusionIDs())
		})
	})
	for _, operation := range []string{"recursive_add", "attach"} {
		partitionExclusionCase(t, "canceled_"+operation, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			defer a.ExecContext(p.Ctx, "ROLLBACK")
			run := func(x fixture.Executor, s fixture.Step) {
				s.Prepared = prepared
				fixture.ExclusionReferenceRun(t, p, b, x, s)
			}
			run(a, fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
			q := "ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist(" + partitionExclusionKeys + ")"
			rows := [][]any{partitionAdoptionRow("pe_low", "pe_local", nil, true, true, "i", true)}
			if operation == "attach" {
				run(a, fixture.Exec(q, 0))
				run(a, fixture.Exec("CREATE TABLE pe_low(id INT,tenant INET,during INT4RANGE)", 0))
				q = "ALTER TABLE pe_root ATTACH PARTITION pe_low DEFAULT"
				rows = append(rows, partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", true))
			} else {
				run(a, fixture.Exec("CREATE TABLE pe_low PARTITION OF pe_root DEFAULT", 0))
			}
			run(a, fixture.Exec("ALTER TABLE pe_low ADD CONSTRAINT pe_local EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
			ids := partitionAdoptionIdentity(t, p, a, "pe_low")
			run(a, fixture.Exec("INSERT INTO pe_low VALUES(1,'10.0.0.1','[1,5)')", 1))
			// Prepare before acquiring the blocker, not inside the canceled worker.
			held, err := peer.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			run(a, fixture.Exec("BEGIN", 0))
			run(a, fixture.Select("SELECT id FROM pe_low ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			if !b.PG {
				// Preserve the independent admitted statement-timeout diagnostic.
				// The timer is armed only after preparing this separate borrower.
				timed, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_ = timed.Raw(func(any) error { return driver.ErrBadConn })
					_ = timed.Close()
				})
				timedStmt, err := timed.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer timedStmt.Close()
				fixture.RunStep(t, p.Ctx, timed, b.Recorder, fixture.Exec("SET statement_timeout='50ms'", 0))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: timed, Statement: timedStmt, SQL: q}, b.Recorder, referenceRangeError(q, "57014", "canceling statement due to statement timeout", fixture.Diagnostic{}))
				if err := timed.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
					t.Fatal("discard timed borrower", err)
				}
			}
			fixture.RelationLockActiveCancel(t, p, b, peer, q, func(ctx context.Context) fixture.RelationOutcome {
				var out fixture.RelationOutcome
				if prepared {
					out.Result, out.Err = held.ExecContext(ctx)
				} else {
					out.Result, out.Err = peer.ExecContext(ctx, q)
				}
				return out
			})
			run(a, fixture.Exec("ROLLBACK", 0))
			run(peer, partitionAdoptionSame("pe_low", ids))
			run(peer, partitionAdoptionGraph(rows))
			run(peer, partitionExclusionConflict("INSERT INTO pe_low VALUES(2,'10.0.0.1','[3,7)')", "pe_low", "pe_local", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
			run(peer, fixture.Select("SELECT id FROM pe_low ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(peer, fixture.Exec(q, 0))
			run(peer, partitionAdoptionSame("pe_low", ids))
			run(peer, partitionExclusionConflict("INSERT INTO pe_root VALUES(2,'10.0.0.1','[3,7)')", "pe_low", "pe_local", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
		})
	}
}
