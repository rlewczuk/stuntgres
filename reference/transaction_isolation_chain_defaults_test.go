//go:build reference

package reference_test

import (
	"context"
	"strings"
	"testing"
)

func TestReferenceTransactionIsolationChainDefaultsMatrix(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		for _, baseline := range []struct{ mode, iso, access, activeMode, activeIso, activeAccess string }{
			{"READ COMMITTED READ WRITE", "read committed", "off", "REPEATABLE READ READ ONLY", "repeatable read", "on"},
			{"REPEATABLE READ READ ONLY", "repeatable read", "on", "READ COMMITTED READ WRITE", "read committed", "off"},
		} {
			for _, end := range []string{"COMMIT AND CHAIN", "ROLLBACK AND CHAIN"} {
				for _, aborted := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						t.Run(map[bool]string{false: "memory/", true: "postgres/"}[postgres]+baseline.mode+"/"+end+map[bool]string{false: "/healthy", true: "/aborted"}[aborted]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
							_, a, _ := isolationReferenceConnections(t, postgres)
							ctx := context.Background()
							isolationReferenceAffected(t, ctx, a, "CREATE TABLE t(v INT)", false, 0)
							isolationReferenceAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL "+baseline.mode, prepared, 0)
							isolationReferenceAffected(t, ctx, a, "BEGIN ISOLATION LEVEL "+baseline.activeMode, prepared, 0)
							isolationReferenceAffected(t, ctx, a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL "+baseline.activeMode, prepared, 0)
							if aborted {
								isolationReferenceAffected(t, ctx, a, "SELECT num_nonnulls(1)", false, 1)
								referenceIsolationError(t, a, "SET TRANSACTION ISOLATION LEVEL "+strings.ToUpper(baseline.iso), "25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query", prepared)
							}
							isolationReferenceAffected(t, ctx, a, end, prepared, 0)
							currentIso, currentAccess := baseline.activeIso, baseline.activeAccess
							if aborted {
								currentIso, currentAccess = baseline.iso, baseline.access
							}
							defaultIso, defaultAccess := baseline.iso, baseline.access
							if !aborted && end == "COMMIT AND CHAIN" {
								defaultIso, defaultAccess = baseline.activeIso, baseline.activeAccess
							}
							referenceIsolationShow(t, a, "transaction_isolation", currentIso)
							referenceIsolationShow(t, a, "transaction_read_only", currentAccess)
							referenceIsolationShow(t, a, "default_transaction_isolation", defaultIso)
							referenceIsolationShow(t, a, "default_transaction_read_only", defaultAccess)
							if currentAccess == "on" {
								referenceIsolationError(t, a, "INSERT INTO t VALUES(1)", "25006", "cannot execute INSERT in a read-only transaction", prepared)
							} else {
								isolationReferenceAffected(t, ctx, a, "INSERT INTO t VALUES(1)", prepared, 1)
							}
							isolationReferenceAffected(t, ctx, a, "ROLLBACK", false, 0)
							isolationReferenceSelect(t, ctx, a, "SELECT v FROM t", false)
						})
					}
				}
			}
		}
	}
}
