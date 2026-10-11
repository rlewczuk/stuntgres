package reference_test

import (
	"context"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func referenceTestTransactionIsolationAbortRestoration(t *testing.T, postgres bool) {
	for _, baseline := range []struct{ mode, isolation, access string }{
		{"READ COMMITTED READ WRITE", "read committed", "off"},
		{"REPEATABLE READ READ ONLY", "repeatable read", "on"},
		{"READ UNCOMMITTED READ ONLY", "read uncommitted", "on"},
	} {
		for _, end := range []string{"COMMIT", "ROLLBACK", "COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
			for _, prepared := range []bool{false, true} {
				t.Run(baseline.mode+"/"+end+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					_, a, _ := fixture.IsolationReferenceConnections(t, fixture.NewScenario(t), postgres)
					ctx := context.Background()
					fixture.IsolationReferenceAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL "+baseline.mode, prepared, 0)
					fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL READ COMMITTED READ WRITE", prepared, 0)
					fixture.IsolationReferenceAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ UNCOMMITTED READ WRITE", prepared, 0)
					fixture.IsolationReferenceAffected(t, ctx, a, "SELECT num_nonnulls(1)", false, 1)
					referenceIsolationError(t, a, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ", "25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query", prepared)
					fixture.IsolationReferenceAffected(t, ctx, a, end, prepared, 0)
					referenceIsolationShow(t, a, "default_transaction_isolation", baseline.isolation)
					referenceIsolationShow(t, a, "default_transaction_read_only", baseline.access)
					referenceIsolationShow(t, a, "transaction_isolation", baseline.isolation)
					referenceIsolationShow(t, a, "transaction_read_only", baseline.access)
					if strings.Contains(end, "CHAIN") {
						fixture.IsolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
					}
					fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN", false, 0)
					referenceIsolationShow(t, a, "transaction_isolation", baseline.isolation)
					referenceIsolationShow(t, a, "transaction_read_only", baseline.access)
					fixture.IsolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
				})
			}
		}
	}
}

func TestReferenceTransactionIsolationAbortRestoration(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		if postgres && !*fixture.Mode {
			continue
		}
		t.Run(map[bool]string{false: "memory", true: "postgres"}[postgres], func(t *testing.T) { referenceTestTransactionIsolationAbortRestoration(t, postgres) })
	}
}

func TestReferenceTransactionIsolationChainedAbortBaseline(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		if postgres && !*fixture.Mode {
			continue
		}
		for _, end := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
			t.Run(map[bool]string{false: "memory/", true: "postgres/"}[postgres]+end, func(t *testing.T) {
				_, a, _ := fixture.IsolationReferenceConnections(t, fixture.NewScenario(t), postgres)
				ctx := context.Background()
				fixture.IsolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", false, 0)
				fixture.IsolationReferenceAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ UNCOMMITTED READ WRITE", false, 0)
				fixture.IsolationReferenceAffected(t, ctx, a, end, false, 0)
				referenceIsolationShow(t, a, "transaction_isolation", "repeatable read")
				referenceIsolationShow(t, a, "transaction_read_only", "on")
				fixture.IsolationReferenceAffected(t, ctx, a, "SET TRANSACTION ISOLATION LEVEL READ COMMITTED READ WRITE", true, 0)
				fixture.IsolationReferenceAffected(t, ctx, a, "SELECT num_nonnulls(1)", false, 1)
				referenceIsolationError(t, a, "SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED", "25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query", true)
				fixture.IsolationReferenceAffected(t, ctx, a, end, false, 0)
				referenceIsolationShow(t, a, "transaction_isolation", "repeatable read")
				referenceIsolationShow(t, a, "transaction_read_only", "on")
				defaults := "read committed"
				if end == "COMMIT AND CHAIN" {
					defaults = "read uncommitted"
				}
				referenceIsolationShow(t, a, "default_transaction_isolation", defaults)
				referenceIsolationShow(t, a, "default_transaction_read_only", "off")
				fixture.IsolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
			})
		}
	}
}
