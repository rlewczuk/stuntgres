package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConcurrentIndexReplacement(t *testing.T) {
	for _, restore := range []bool{false, true} {
		for _, snapshot := range []bool{false, true} {
			t.Run(fmtConcurrentCase(restore, snapshot, false), func(t *testing.T) {
				fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
					fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1),(2)", 2)
					rows, err := c.QueryContext(p.Ctx, "SELECT n FROM t ORDER BY n")
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
					if b.PG {
						// Native replacement has no SQL equivalent. PG replay independently builds a
						// fresh complete destination while the already delivered cursor stays owned.
						fixture.ConcurrentIndexExec(t, p, b, builder, "CREATE TABLE replacement(n INTEGER)", 0)
						fixture.ConcurrentIndexExec(t, p, b, builder, "INSERT INTO replacement VALUES(9)", 1)
						fixture.ConcurrentIndexExec(t, p, b, builder, "CREATE INDEX CONCURRENTLY replacement_ix ON replacement(n)", 0)
						fixture.ConcurrentIndexRows(t, p, b, builder, "SELECT n FROM replacement ORDER BY n", 9)
					} else {
						fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN", 0)
						if snapshot {
							fixture.ConcurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
							fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
							fixture.ConcurrentIndexRows(t, p, b, a, "SELECT n FROM t ORDER BY n", 1, 2)
						} else {
							fixture.ConcurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(3)", 1)
						}
						w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", true)
						state := stuntgres.IndexState{Live: true}
						phase := "waiting for writers before build"
						if snapshot {
							state.Ready = true
							phase = "waiting for old snapshots"
						}
						fixture.ConcurrentIndexWait(t, p, b, "ix", state, 0, 0, phase)
						if restore {
							err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE t(n INTEGER); INSERT INTO t VALUES(9);"))
						} else {
							err = stuntgres.Reset(p.Ctx, p.Native)
						}
						if err != nil {
							t.Fatal(err)
						}
						err = w.Join(t, p)
						if err == nil || err.Error() != "stuntgres: catalog replaced before publication" {
							t.Fatalf("old builder escaped fence: %v", err)
						}
						fixture.ConcurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
						if !restore {
							fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
							fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(9)", 1)
						}
						if _, found := fixture.ConcurrentIndexState(t, p, b, "ix"); found {
							t.Fatal("old index resurrected")
						}
						fixture.ConcurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ix ON t(n)", 0)
						fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
						fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 9)
						var before, after bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
							t.Fatal(err)
						}
						err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE bad(n INTEGER); INSERT INTO bad VALUES(1),(1); CREATE UNIQUE INDEX bad_ix ON bad(n);"))
						if err == nil {
							t.Fatal("duplicate Restore succeeded")
						}
						if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || before.String() != after.String() {
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
	t.Run("computed", func(t *testing.T) { fixture.ConcurrentIndexScenario(t, concurrentIndexComputed) })
	t.Run("snapshots", TestReferenceConcurrentIndexSnapshots)
	t.Run("invalid-recovery-and-replay", TestReferenceConcurrentIndexControls)
	t.Run("explicit", TestReferenceConcurrentIndexTransactionRestrictions)
	t.Run("nulls-not-distinct", TestReferenceConcurrentIndexNullsNotDistinct)
}
