package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceTestTransactionIsolationConflicts(t *testing.T, postgres bool) {
	for _, isolation := range []string{"READ COMMITTED", "READ UNCOMMITTED", "REPEATABLE READ"} {
		for _, peer := range []string{"UPDATE t SET v=20 WHERE v=10", "DELETE FROM t WHERE v=10", "UPDATE t SET v=20 WHERE v=10;DELETE"} {
			for _, own := range []string{"UPDATE t SET v=30 WHERE v=10", "DELETE FROM t WHERE v=10"} {
				for _, prepared := range []bool{false, true} {
					t.Run(isolation+"/"+peer+"/"+own+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						_, a, b := fixture.IsolationReferenceConnections(t, fixture.NewScenario(t), postgres)
						ctx := context.Background()
						fixture.IsolationReferenceAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
						fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO t VALUES(10),(50)", false, 2)
						fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL "+isolation, false, 0)
						fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM t ORDER BY v", false, 10, 50)
						fixture.IsolationReferenceAffected(t, ctx, a, "INSERT INTO t VALUES(5)", false, 1)
						fixture.IsolationReferenceAffected(t, ctx, a, "DELETE FROM t WHERE v=50", false, 1)
						if peer == "UPDATE t SET v=20 WHERE v=10;DELETE" {
							fixture.IsolationReferenceAffected(t, ctx, b, "UPDATE t SET v=20 WHERE v=10", false, 1)
							fixture.IsolationReferenceAffected(t, ctx, b, "DELETE FROM t WHERE v=20", false, 1)
						} else {
							fixture.IsolationReferenceAffected(t, ctx, b, peer, false, 1)
						}
						if isolation == "REPEATABLE READ" {
							word := "update"
							if peer == "DELETE FROM t WHERE v=10" {
								word = "delete"
							}
							referenceIsolationError(t, a, own, "40001", "could not serialize access due to concurrent "+word, prepared)
							referenceIsolationError(t, a, "SELECT num_nonnulls(1)", "25P02", "current transaction is aborted, commands ignored until end of transaction block", false)
						} else {
							fixture.IsolationReferenceAffected(t, ctx, a, own, prepared, 0)
						}
						fixture.IsolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
						if peer == "UPDATE t SET v=20 WHERE v=10" {
							fixture.IsolationReferenceSelect(t, ctx, b, postgres, "SELECT v FROM t ORDER BY v", false, 20, 50)
						} else {
							fixture.IsolationReferenceSelect(t, ctx, b, postgres, "SELECT v FROM t ORDER BY v", false, 50)
						}
					})
				}
			}
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "multirow/direct", true: "multirow/prepared"}[prepared], func(t *testing.T) {
			_, a, b := fixture.IsolationReferenceConnections(t, fixture.NewScenario(t), postgres)
			ctx := context.Background()
			fixture.IsolationReferenceAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
			fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO t VALUES(1),(2),(3)", false, 3)
			fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", false, 0)
			fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM t ORDER BY v", false, 1, 2, 3)
			fixture.IsolationReferenceAffected(t, ctx, a, "INSERT INTO t VALUES(4)", false, 1)
			fixture.IsolationReferenceAffected(t, ctx, a, "DELETE FROM t WHERE v=3", false, 1)
			fixture.IsolationReferenceAffected(t, ctx, b, "UPDATE t SET v=20 WHERE v=2", false, 1)
			referenceIsolationError(t, a, "UPDATE t SET v=v+100", "40001", "could not serialize access due to concurrent update", prepared)
			fixture.IsolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
			fixture.IsolationReferenceSelect(t, ctx, b, postgres, "SELECT v FROM t ORDER BY v", false, 1, 3, 20)
		})
	}
}
func referenceTestTransactionIsolationChain(t *testing.T, postgres bool) {
	for _, end := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
		for _, aborted := range []bool{false, true} {
			t.Run(end+map[bool]string{false: "/healthy", true: "/aborted"}[aborted], func(t *testing.T) {
				_, a, b := fixture.IsolationReferenceConnections(t, fixture.NewScenario(t), postgres)
				ctx := context.Background()
				fixture.IsolationReferenceAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
				fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO t VALUES(1)", false, 1)
				fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", false, 0)
				fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM t", false, 1)
				fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO t VALUES(2)", false, 1)
				fixture.IsolationReferenceAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ COMMITTED READ WRITE", false, 0)
				if aborted {
					referenceIsolationError(t, a, "DELETE FROM t", "25006", "cannot execute DELETE in a read-only transaction", false)
				}
				fixture.IsolationReferenceAffected(t, ctx, a, end, false, 0)
				wantIsolation, wantAccess := "repeatable read", "on"
				if aborted {
					wantIsolation, wantAccess = "read committed", "off"
				}
				referenceIsolationShow(t, a, "transaction_isolation", wantIsolation)
				referenceIsolationShow(t, a, "transaction_read_only", wantAccess)
				referenceIsolationShow(t, a, "default_transaction_isolation", "read committed")
				referenceIsolationShow(t, a, "default_transaction_read_only", "off")
				fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM t ORDER BY v", false, 1, 2)
				fixture.IsolationReferenceAffected(t, ctx, b, "INSERT INTO t VALUES(3)", false, 1)
				if aborted {
					fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM t ORDER BY v", false, 1, 2, 3)
					fixture.IsolationReferenceAffected(t, ctx, a, "INSERT INTO t VALUES(4)", true, 1)
				} else {
					fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM t ORDER BY v", false, 1, 2)
					referenceIsolationError(t, a, "INSERT INTO t VALUES(4)", "25006", "cannot execute INSERT in a read-only transaction", true)
				}
				fixture.IsolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
				fixture.IsolationReferenceSelect(t, ctx, b, postgres, "SELECT v FROM t ORDER BY v", false, 1, 2, 3)
			})
		}
	}
}
func referenceTestTransactionIsolationOwnRows(t *testing.T, postgres bool) {
	_, a, b := fixture.IsolationReferenceConnections(t, fixture.NewScenario(t), postgres)
	ctx := context.Background()
	fixture.IsolationReferenceAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
	fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", false, 0)
	fixture.IsolationReferenceAffected(t, ctx, a, "INSERT INTO t VALUES(1)", true, 1)
	fixture.IsolationReferenceAffected(t, ctx, a, "UPDATE t SET v=2", true, 1)
	fixture.IsolationReferenceAffected(t, ctx, a, "UPDATE t SET v=3", true, 1)
	fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM t", true, 3)
	fixture.IsolationReferenceSelect(t, ctx, b, postgres, "SELECT v FROM t", false)
	fixture.IsolationReferenceAffected(t, ctx, a, "DELETE FROM t", true, 1)
	fixture.IsolationReferenceAffected(t, ctx, a, "UPDATE t SET v=4", true, 0)
	fixture.IsolationReferenceSelect(t, ctx, a, postgres, "SELECT v FROM t", false)
	fixture.IsolationReferenceAffected(t, ctx, a, "COMMIT", false, 0)
	fixture.IsolationReferenceSelect(t, ctx, b, postgres, "SELECT v FROM t", false)
}
func referenceTestTransactionIsolationSettingRoutes(t *testing.T, postgres bool) {
	_, a, _ := fixture.IsolationReferenceConnections(t, fixture.NewScenario(t), postgres)
	ctx := context.Background()
	fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN", false, 0)
	fixture.IsolationReferenceAffected(t, ctx, a, "SET transaction_isolation TO 'read uncommitted'", true, 0)
	referenceIsolationShow(t, a, "transaction_isolation", "read uncommitted")
	fixture.IsolationReferenceAffected(t, ctx, a, "SELECT set_config('transaction_isolation','read uncommitted',true)", false, 1)
	referenceIsolationError(t, a, "SET transaction_isolation TO 'read committed'", "25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query", true)
	fixture.IsolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
	for _, end := range []string{"COMMIT", "ROLLBACK"} {
		fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN", false, 0)
		fixture.IsolationReferenceAffected(t, ctx, a, "SET default_transaction_isolation TO 'repeatable read'", false, 0)
		fixture.IsolationReferenceAffected(t, ctx, a, "SET LOCAL default_transaction_isolation TO 'read uncommitted'", false, 0)
		fixture.IsolationReferenceAffected(t, ctx, a, end, false, 0)
		want := "read committed"
		if end == "COMMIT" {
			want = "repeatable read"
		}
		referenceIsolationShow(t, a, "default_transaction_isolation", want)
		fixture.IsolationReferenceAffected(t, ctx, a, "RESET default_transaction_isolation", true, 0)
	}
	fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN", false, 0)
	fixture.IsolationReferenceAffected(t, ctx, a, "SELECT set_config('default_transaction_isolation','repeatable read',false)", true, 1)
	referenceIsolationShow(t, a, "transaction_isolation", "read committed")
	referenceIsolationShow(t, a, "default_transaction_isolation", "repeatable read")
	fixture.IsolationReferenceAffected(t, ctx, a, "COMMIT", false, 0)
	fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN", false, 0)
	referenceIsolationShow(t, a, "transaction_isolation", "repeatable read")
	fixture.IsolationReferenceAffected(t, ctx, a, "RESET ALL", false, 0)
	fixture.IsolationReferenceAffected(t, ctx, a, "COMMIT", false, 0)
	referenceIsolationShow(t, a, "default_transaction_isolation", "read committed")
}

func TestReferenceTransactionIsolationConflicts(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		if postgres && !*fixture.Mode {
			continue
		}
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) { referenceTestTransactionIsolationConflicts(t, postgres) })
	}
	if *fixture.Mode {
		t.Run("queued", referenceIsolationQueuedConflicts)
	}
}

func TestReferenceTransactionIsolationChain(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		if postgres && !*fixture.Mode {
			continue
		}
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) { referenceTestTransactionIsolationChain(t, postgres) })
	}
}

func TestReferenceTransactionIsolationOwnRows(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		if postgres && !*fixture.Mode {
			continue
		}
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) { referenceTestTransactionIsolationOwnRows(t, postgres) })
	}
}

func TestReferenceTransactionIsolationSettingRoutes(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		if postgres && !*fixture.Mode {
			continue
		}
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) { referenceTestTransactionIsolationSettingRoutes(t, postgres) })
	}
}
