package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceConcurrentIndexReplacement(t *testing.T) {
	for _, restore := range []bool{false, true} {
		for _, snapshot := range []bool{false, true} {
			t.Run(fmtConcurrentCase(restore, snapshot, false), func(t *testing.T) {
				concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
					concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1),(2)", 2)
					rows, err := c.QueryContext(p.ctx, "SELECT n FROM t ORDER BY n")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.Columns()
					if err != nil || len(columns) != 1 || columns[0] != "n" {
						t.Fatal(columns, err)
					}
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
						t.Fatal(types, err)
					}
					if !rows.Next() {
						t.Fatal("missing first eager row", rows.Err())
					}
					var first int64
					if err := rows.Scan(&first); err != nil || first != 1 {
						t.Fatal(first, err)
					}
					if b.pg {
						// Native replacement has no SQL equivalent. PG replay independently builds a
						// fresh complete destination while the already delivered cursor stays owned.
						concurrentIndexExec(t, p, b, builder, "CREATE TABLE replacement(n INTEGER)", 0)
						concurrentIndexExec(t, p, b, builder, "INSERT INTO replacement VALUES(9)", 1)
						concurrentIndexExec(t, p, b, builder, "CREATE INDEX CONCURRENTLY replacement_ix ON replacement(n)", 0)
						concurrentIndexRows(t, p, b, builder, "SELECT n FROM replacement ORDER BY n", 9)
					} else {
						concurrentIndexExec(t, p, b, a, "BEGIN", 0)
						if snapshot {
							concurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
							concurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
							concurrentIndexRows(t, p, b, a, "SELECT n FROM t ORDER BY n", 1, 2)
						} else {
							concurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(3)", 1)
						}
						w := concurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", true)
						state := stuntgres.IndexState{Live: true}
						phase := "waiting for writers before build"
						if snapshot {
							state.Ready = true
							phase = "waiting for old snapshots"
						}
						concurrentIndexWait(t, p, b, "ix", state, 0, 0, phase)
						if restore {
							err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE t(n INTEGER); INSERT INTO t VALUES(9);"))
						} else {
							err = stuntgres.Reset(p.ctx, p.native)
						}
						if err != nil {
							t.Fatal(err)
						}
						err = w.join(t, p)
						if err == nil || err.Error() != "stuntgres: catalog replaced before publication" {
							t.Fatalf("old builder escaped fence: %v", err)
						}
						concurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
						if !restore {
							concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
							concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(9)", 1)
						}
						if _, found := concurrentIndexState(t, p, b, "ix"); found {
							t.Fatal("old index resurrected")
						}
						concurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ix ON t(n)", 0)
						concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
						concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 9)
						var before, after bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &before); err != nil {
							t.Fatal(err)
						}
						err = stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE bad(n INTEGER); INSERT INTO bad VALUES(1),(1); CREATE UNIQUE INDEX bad_ix ON bad(n);"))
						if err == nil {
							t.Fatal("duplicate Restore succeeded")
						}
						if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || before.String() != after.String() {
							t.Fatal("failed Restore changed destination", err)
						}
					}
					if !rows.Next() {
						t.Fatal("missing retained second row", rows.Err())
					}
					var second int64
					if err := rows.Scan(&second); err != nil || second != 2 {
						t.Fatal(second, err)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("unexpected retained rows", rows.Err())
					}
				})
			})
		}
	}
}

func TestReferenceConcurrentIndexFeatureSmoke(t *testing.T) {
	t.Run("writers", TestReferenceConcurrentIndexWriters)
	t.Run("hash", TestReferenceConcurrentIndexHash)
	t.Run("hash-invalid-query", TestReferenceConcurrentIndexHashInvalidQuery)
	t.Run("computed", func(t *testing.T) { concurrentIndexScenario(t, concurrentIndexComputed) })
	t.Run("snapshots", TestReferenceConcurrentIndexSnapshots)
	t.Run("invalid-recovery-and-replay", TestReferenceConcurrentIndexControls)
	t.Run("explicit", TestReferenceConcurrentIndexTransactionRestrictions)
	t.Run("nulls-not-distinct", TestReferenceConcurrentIndexNullsNotDistinct)
}
