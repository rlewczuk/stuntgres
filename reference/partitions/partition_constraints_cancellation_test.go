package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionConstraintsCancellation(t *testing.T) {
	partitionConstraintCase(t, "blocked_constraint_drop", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		setup := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintTree(setup, "PRIMARY KEY(k,payload)")
		setup(fixture.Exec("INSERT INTO pc_root VALUES(1,'sentinel')", 1))
		query := "ALTER TABLE pc_root DROP CONSTRAINT pc_root_pkey"
		partitionConstraintBlockedCancellation(t, p, b, prepared, query)
		setup(partitionConstraintEdge("pc_a_pkey", "pc_root_pkey", "pc_a"))
		setup(fixture.Select("SELECT k,payload FROM pc_root", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "sentinel"}}))
		setup(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(1,'sentinel')", "pc_a", "pc_a_pkey", "Key (k, payload)=(1, sentinel) already exists."))
		setup(fixture.Exec(query, 0))
		setup(fixture.Exec("INSERT INTO pc_root VALUES(1,'sentinel')", 1))
	})
}

func partitionConstraintBlockedCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool, query string) {
	t.Helper()
	holder, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
	defer holder.ExecContext(p.Ctx, "ROLLBACK")
	fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
	if b.PG {
		fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE pc_root IN ACCESS EXCLUSIVE MODE", 0)
	} else {
		fixture.LockExec(t, p.Ctx, holder, "INSERT INTO pc_root VALUES(2,'private')", 1)
	}
	if b.PG {
		pid, blocker := fixture.LockPID(t, p.Ctx, waiter), fixture.LockPID(t, p.Ctx, holder)
		done := fixture.RowAsync(t, p.Ctx, waiter, query, prepared, false)
		fixture.RowWait(t, p.Ctx, pid, blocker)
		var acknowledged bool
		if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&acknowledged); err != nil || !acknowledged {
			t.Fatal("server cancellation acknowledgement", acknowledged, err)
		}
		outcome := fixture.LockResult(t, done)
		want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
		fixture.AssertDiagnostic(t, outcome.Err, &want)
		if outcome.Result != nil {
			t.Fatal("canceled operation published result", outcome.Result)
		}
	} else {
		fixture.LockExec(t, p.Ctx, waiter, "SET statement_timeout='50ms'", 0)
		step := referenceRangeError(query, "57014", "canceling statement due to statement timeout", fixture.Diagnostic{})
		step.Prepared = prepared
		fixture.RunStep(t, p.Ctx, waiter, b.Recorder, step)
		fixture.LockExec(t, p.Ctx, waiter, "SET statement_timeout=0", 0)
	}
	fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
}

func TestReferencePartitionConstraintsIndexCancellation(t *testing.T) {
	for _, operation := range []string{"build", "attach", "drop"} {
		partitionConstraintCase(t, "blocked_"+operation, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			partitionConstraintManualTree(run)
			run(fixture.Exec("INSERT INTO pc_root VALUES(1,'sentinel')", 1))
			query := "CREATE UNIQUE INDEX pc_new_idx ON pc_root(k,payload)"
			switch operation {
			case "attach":
				query = "ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx"
			case "drop":
				run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx", 0))
				query = "DROP INDEX pc_root_idx"
			}
			partitionConstraintBlockedCancellation(t, p, b, prepared, query)
			run(fixture.Select("SELECT k,payload FROM pc_root", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "sentinel"}}))
			run(partitionConstraintIndexState("pc_root_idx", false))
			if operation == "drop" {
				run(partitionConstraintEdge("pc_a_idx", "pc_root_idx", "pc_a"))
			} else {
				run(fixture.Select("SELECT count(*)::int4 AS n FROM pg_depend WHERE objid='pc_a_idx'::regclass::OID AND deptype='P'", []string{"n"}, []string{"INT4"}, [][]any{{int64(0)}}))
			}
			if operation == "build" {
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_new_idx'", []string{"relname"}, []string{"NAME"}, nil))
			}
			run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(1,'sentinel')", "pc_a", "pc_a_idx", "Key (k, payload)=(1, sentinel) already exists."))
			run(fixture.Exec(query, 0))
			switch operation {
			case "build":
				run(partitionConstraintIndexState("pc_new_idx", true))
				run(partitionConstraintEdge("pc_a_idx", "pc_new_idx", "pc_a"))
			case "attach":
				run(partitionConstraintEdge("pc_a_idx", "pc_root_idx", "pc_a"))
			case "drop":
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_root_idx' OR relname='pc_a_idx'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Exec("INSERT INTO pc_root VALUES(1,'sentinel')", 1))
			}
		})
	}
}
