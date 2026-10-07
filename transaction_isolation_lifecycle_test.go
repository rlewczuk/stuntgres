package stuntgres_test

import (
	"context"
	"testing"
)

func TestTransactionIsolationConflicts(t *testing.T) {
	for _, isolation := range []string{"READ COMMITTED", "READ UNCOMMITTED", "REPEATABLE READ"} {
		for _, peer := range []string{"UPDATE t SET v=20 WHERE v=10", "DELETE FROM t WHERE v=10", "UPDATE t SET v=20 WHERE v=10;DELETE"} {
			for _, own := range []string{"UPDATE t SET v=30 WHERE v=10", "DELETE FROM t WHERE v=10"} {
				for _, prepared := range []bool{false, true} {
					t.Run(isolation+"/"+peer+"/"+own+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						_, a, b := transactionConnections(t)
						ctx := context.Background()
						transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
						transactionAffected(t, ctx, b, "INSERT INTO t VALUES(10),(50)", false, 2)
						transactionAffected(t, ctx, a, "BEGIN ISOLATION LEVEL "+isolation, false, 0)
						transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 10, 50)
						transactionAffected(t, ctx, a, "INSERT INTO t VALUES(5)", false, 1)
						transactionAffected(t, ctx, a, "DELETE FROM t WHERE v=50", false, 1)
						if peer == "UPDATE t SET v=20 WHERE v=10;DELETE" {
							transactionAffected(t, ctx, b, "UPDATE t SET v=20 WHERE v=10", false, 1)
							transactionAffected(t, ctx, b, "DELETE FROM t WHERE v=20", false, 1)
						} else {
							transactionAffected(t, ctx, b, peer, false, 1)
						}
						if isolation == "REPEATABLE READ" {
							word := "update"
							if peer == "DELETE FROM t WHERE v=10" {
								word = "delete"
							}
							isolationError(t, a, own, "40001", "could not serialize access due to concurrent "+word, prepared)
							isolationError(t, a, "SELECT num_nonnulls(1)", "25P02", "current transaction is aborted, commands ignored until end of transaction block", false)
						} else {
							transactionAffected(t, ctx, a, own, prepared, 0)
						}
						transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
						if peer == "UPDATE t SET v=20 WHERE v=10" {
							transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 20, 50)
						} else {
							transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 50)
						}
					})
				}
			}
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "multirow/direct", true: "multirow/prepared"}[prepared], func(t *testing.T) {
			_, a, b := transactionConnections(t)
			ctx := context.Background()
			transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
			transactionAffected(t, ctx, b, "INSERT INTO t VALUES(1),(2),(3)", false, 3)
			transactionAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", false, 0)
			transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 2, 3)
			transactionAffected(t, ctx, a, "INSERT INTO t VALUES(4)", false, 1)
			transactionAffected(t, ctx, a, "DELETE FROM t WHERE v=3", false, 1)
			transactionAffected(t, ctx, b, "UPDATE t SET v=20 WHERE v=2", false, 1)
			isolationError(t, a, "UPDATE t SET v=v+100", "40001", "could not serialize access due to concurrent update", prepared)
			transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
			transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 3, 20)
		})
	}
}
func TestTransactionIsolationChain(t *testing.T) {
	for _, end := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
		for _, aborted := range []bool{false, true} {
			t.Run(end+map[bool]string{false: "/healthy", true: "/aborted"}[aborted], func(t *testing.T) {
				_, a, b := transactionConnections(t)
				ctx := context.Background()
				transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
				transactionAffected(t, ctx, b, "INSERT INTO t VALUES(1)", false, 1)
				transactionAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", false, 0)
				transactionSelect(t, ctx, a, "SELECT v FROM t", false, 1)
				transactionAffected(t, ctx, b, "INSERT INTO t VALUES(2)", false, 1)
				transactionAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ COMMITTED READ WRITE", false, 0)
				if aborted {
					isolationError(t, a, "DELETE FROM t", "25006", "cannot execute DELETE in a read-only transaction", false)
				}
				transactionAffected(t, ctx, a, end, false, 0)
				wantIsolation, wantAccess := "repeatable read", "on"
				if aborted {
					wantIsolation, wantAccess = "read committed", "off"
				}
				isolationShow(t, a, "transaction_isolation", wantIsolation)
				isolationShow(t, a, "transaction_read_only", wantAccess)
				isolationShow(t, a, "default_transaction_isolation", "read committed")
				isolationShow(t, a, "default_transaction_read_only", "off")
				transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 2)
				transactionAffected(t, ctx, b, "INSERT INTO t VALUES(3)", false, 1)
				if aborted {
					transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 2, 3)
					transactionAffected(t, ctx, a, "INSERT INTO t VALUES(4)", true, 1)
				} else {
					transactionSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 2)
					isolationError(t, a, "INSERT INTO t VALUES(4)", "25006", "cannot execute INSERT in a read-only transaction", true)
				}
				transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
				transactionSelect(t, ctx, b, "SELECT v FROM t ORDER BY v", false, 1, 2, 3)
			})
		}
	}
}
func TestTransactionIsolationOwnRows(t *testing.T) {
	_, a, b := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
	transactionAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", false, 0)
	transactionAffected(t, ctx, a, "INSERT INTO t VALUES(1)", true, 1)
	transactionAffected(t, ctx, a, "UPDATE t SET v=2", true, 1)
	transactionAffected(t, ctx, a, "UPDATE t SET v=3", true, 1)
	transactionSelect(t, ctx, a, "SELECT v FROM t", true, 3)
	transactionSelect(t, ctx, b, "SELECT v FROM t", false)
	transactionAffected(t, ctx, a, "DELETE FROM t", true, 1)
	transactionAffected(t, ctx, a, "UPDATE t SET v=4", true, 0)
	transactionSelect(t, ctx, a, "SELECT v FROM t", false)
	transactionAffected(t, ctx, a, "COMMIT", false, 0)
	transactionSelect(t, ctx, b, "SELECT v FROM t", false)
}
func TestTransactionIsolationSettingRoutes(t *testing.T) {
	_, a, _ := transactionConnections(t)
	ctx := context.Background()
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "SET transaction_isolation TO 'read uncommitted'", true, 0)
	isolationShow(t, a, "transaction_isolation", "read uncommitted")
	transactionAffected(t, ctx, a, "SELECT set_config('transaction_isolation','read uncommitted',true)", false, 1)
	isolationError(t, a, "SET transaction_isolation TO 'read committed'", "25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query", true)
	transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
	for _, end := range []string{"COMMIT", "ROLLBACK"} {
		transactionAffected(t, ctx, a, "BEGIN", false, 0)
		transactionAffected(t, ctx, a, "SET default_transaction_isolation TO 'repeatable read'", false, 0)
		transactionAffected(t, ctx, a, "SET LOCAL default_transaction_isolation TO 'read uncommitted'", false, 0)
		transactionAffected(t, ctx, a, end, false, 0)
		want := "read committed"
		if end == "COMMIT" {
			want = "repeatable read"
		}
		isolationShow(t, a, "default_transaction_isolation", want)
		transactionAffected(t, ctx, a, "RESET default_transaction_isolation", true, 0)
	}
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	transactionAffected(t, ctx, a, "SELECT set_config('default_transaction_isolation','repeatable read',false)", true, 1)
	isolationShow(t, a, "transaction_isolation", "read committed")
	isolationShow(t, a, "default_transaction_isolation", "repeatable read")
	transactionAffected(t, ctx, a, "COMMIT", false, 0)
	transactionAffected(t, ctx, a, "BEGIN", false, 0)
	isolationShow(t, a, "transaction_isolation", "repeatable read")
	transactionAffected(t, ctx, a, "RESET ALL", false, 0)
	transactionAffected(t, ctx, a, "COMMIT", false, 0)
	isolationShow(t, a, "default_transaction_isolation", "read committed")
}
