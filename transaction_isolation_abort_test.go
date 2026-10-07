package stuntgres_test

import (
	"context"
	"strings"
	"testing"
)

func TestTransactionIsolationAbortRestoration(t *testing.T) {
	for _, baseline := range []struct{ mode, isolation, access string }{
		{"READ COMMITTED READ WRITE", "read committed", "off"},
		{"REPEATABLE READ READ ONLY", "repeatable read", "on"},
		{"READ UNCOMMITTED READ ONLY", "read uncommitted", "on"},
	} {
		for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
			for _, prepared := range []bool{false, true} {
				t.Run(baseline.mode+"/"+end+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					_, a, _ := transactionConnections(t)
					ctx := context.Background()
					transactionAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL "+baseline.mode, prepared, 0)
					transactionAffected(t, ctx, a, "BEGIN ISOLATION LEVEL READ COMMITTED READ WRITE", prepared, 0)
					transactionAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ UNCOMMITTED READ WRITE", prepared, 0)
					transactionAffected(t, ctx, a, "SELECT num_nonnulls(1)", false, 1)
					isolationError(t, a, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ", "25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query", prepared)
					transactionAffected(t, ctx, a, end, prepared, 0)
					isolationShow(t, a, "default_transaction_isolation", baseline.isolation)
					isolationShow(t, a, "default_transaction_read_only", baseline.access)
					isolationShow(t, a, "transaction_isolation", baseline.isolation)
					isolationShow(t, a, "transaction_read_only", baseline.access)
					if strings.Contains(end, "CHAIN") {
						transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
					}
					transactionAffected(t, ctx, a, "BEGIN", false, 0)
					isolationShow(t, a, "transaction_isolation", baseline.isolation)
					isolationShow(t, a, "transaction_read_only", baseline.access)
					transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
				})
			}
		}
	}
}

func TestTransactionIsolationChainedAbortBaseline(t *testing.T) {
	for _, end := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
		t.Run(end, func(t *testing.T) {
			_, a, _ := transactionConnections(t)
			ctx := context.Background()
			transactionAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", false, 0)
			transactionAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ UNCOMMITTED READ WRITE", false, 0)
			transactionAffected(t, ctx, a, end, false, 0)
			isolationShow(t, a, "transaction_isolation", "repeatable read")
			isolationShow(t, a, "transaction_read_only", "on")
			transactionAffected(t, ctx, a, "SET TRANSACTION ISOLATION LEVEL READ COMMITTED READ WRITE", true, 0)
			transactionAffected(t, ctx, a, "SELECT num_nonnulls(1)", false, 1)
			isolationError(t, a, "SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED", "25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query", true)
			transactionAffected(t, ctx, a, end, false, 0)
			isolationShow(t, a, "transaction_isolation", "repeatable read")
			isolationShow(t, a, "transaction_read_only", "on")
			defaults := "read committed"
			if end == "COMMIT AND CHAIN" {
				defaults = "read uncommitted"
			}
			isolationShow(t, a, "default_transaction_isolation", defaults)
			isolationShow(t, a, "default_transaction_read_only", "off")
			transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
		})
	}
}

func TestTransactionIsolationQueuedConflictTimeout(t *testing.T) {
	for _, own := range []string{"UPDATE t SET v=30 WHERE v=10", "DELETE FROM t WHERE v=10"} {
		for _, prepared := range []bool{false, true} {
			t.Run(own+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				_, a, b := transactionConnections(t)
				ctx := context.Background()
				transactionAffected(t, ctx, b, "CREATE TABLE t(v INT)", false, 0)
				transactionAffected(t, ctx, b, "INSERT INTO t VALUES(10)", false, 1)
				transactionAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", false, 0)
				transactionSelect(t, ctx, a, "SELECT v FROM t", false, 10)
				transactionAffected(t, ctx, b, "BEGIN", false, 0)
				transactionAffected(t, ctx, b, "UPDATE t SET v=20", false, 1)
				transactionAffected(t, ctx, b, "DELETE FROM t", false, 1)
				transactionAffected(t, ctx, a, "SET statement_timeout='2ms'", false, 0)
				// The real row holder keeps this public Exec queued until its timer.
				isolationError(t, a, own, "57014", "canceling statement due to statement timeout", prepared)
				isolationError(t, a, "SELECT num_nonnulls(1)", "25P02", "current transaction is aborted, commands ignored until end of transaction block", false)
				transactionAffected(t, ctx, a, "ROLLBACK", false, 0)
				transactionAffected(t, ctx, b, "ROLLBACK", false, 0)
				transactionSelect(t, ctx, b, "SELECT v FROM t", false, 10)
			})
		}
	}
}
