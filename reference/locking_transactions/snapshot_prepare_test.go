package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotPrepareInsideTransaction(t *testing.T) {
	for _, isolation := range []string{"READ COMMITTED", "REPEATABLE READ", "SERIALIZABLE"} {
		for _, noSource := range []bool{false, true} {
			name := isolation + "/table"
			if noSource {
				name = isolation + "/no-source"
			}
			t.Run(name, func(t *testing.T) {
				snapshotRemedyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, bumper, o := snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b)
					fixture.LockExec(t, p.Ctx, bumper, "CREATE TABLE snapshot_prepare_rows(id integer)", 0)
					fixture.LockExec(t, p.Ctx, bumper, "INSERT INTO snapshot_prepare_rows VALUES(1)", 1)
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO snapshot_prepare_rows VALUES(2)", 1)
					id := identityID(t, p, a)
					snapshotBump(t, p, bumper, id)
					query := "SELECT pg_current_snapshot()::text AS s,pg_visible_in_snapshot($1::xid8,pg_current_snapshot()) AS visible,(SELECT count(*) FROM snapshot_prepare_rows) AS n"
					columns, types := []string{"s", "visible", "n"}, []string{"TEXT", "BOOL", "INT8"}
					if noSource {
						query = "SELECT pg_current_snapshot()::text AS s,pg_visible_in_snapshot($1::xid8,pg_current_snapshot()) AS visible"
						columns, types = []string{"s", "visible"}, []string{"TEXT", "BOOL"}
					}
					fixture.LockExec(t, p.Ctx, o, "BEGIN ISOLATION LEVEL "+isolation, 0)
					// Nothing on O between BEGIN and this actual public Prepare:
					// in particular, no identity or data query can prime its snapshot.
					held, err := o.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal("prepare inside transaction", err)
					}
					t.Cleanup(func() { held.Close() })
					fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
					check := func(visible bool) string {
						out := snapshotRemedyRead(held.QueryContext(p.Ctx, id))
						snapshotRemedyShape(t, out, columns, types, 1)
						s, ok := out.values[0][0].(string)
						if !ok {
							t.Fatalf("snapshot text %T", out.values[0][0])
						}
						count := int64(1)
						ids := [][]any{{int64(1)}}
						if visible {
							count, ids = 2, [][]any{{int64(1)}, {int64(2)}}
						}
						want := []any{s, visible}
						if !noSource {
							want = append(want, count)
						}
						if !reflect.DeepEqual(out.values[0], want) {
							t.Fatalf("prepared result %v want %v", out.values[0], want)
						}
						snapshotRemedyPeer(t, p, b, o, s, id, visible)
						snapshotBoth(t, p, b, o, fixture.Select("SELECT count(*) AS n FROM snapshot_prepare_rows", []string{"n"}, []string{"INT8"}, [][]any{{count}}))
						snapshotBoth(t, p, b, o, fixture.Select("SELECT id FROM snapshot_prepare_rows ORDER BY id", []string{"id"}, []string{"INT4"}, ids))
						return s
					}
					saved := check(isolation == "READ COMMITTED")
					fixture.LockExec(t, p.Ctx, o, "COMMIT", 0)
					// Reuse the SAME statement after ending its preparation transaction.
					_ = check(true)
					snapshotVisible(t, p, b, o, id, saved, isolation == "READ COMMITTED")
				})
			})
		}
	}
}
