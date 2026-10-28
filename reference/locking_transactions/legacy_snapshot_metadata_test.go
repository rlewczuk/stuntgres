package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLegacySnapshotMetadata(t *testing.T) {
	for _, kind := range []string{"value", "null", "empty", "current", "identity", "routines"} {
		t.Run(kind, func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				switch kind {
				case "value":
					identityBoth(t, p, b, b.DB, legacySnapshotStep(b, "SELECT '12:16:14,14'::txid_snapshot AS s,txid_snapshot_xmin('12:16:14') AS lo,txid_snapshot_xmax('12:16:14') AS hi,txid_visible_in_snapshot(13,'12:16:14') AS v,txid_status(1) AS status", []string{"s", "lo", "hi", "v", "status"}, []string{"TXID_SNAPSHOT", "INT8", "INT8", "BOOL", "TEXT"}, [][]any{{"12:16:14", int64(12), int64(16), true, "committed"}}))
				case "null":
					identityBoth(t, p, b, b.DB, legacySnapshotStep(b, "SELECT NULL::txid_snapshot AS s,txid_snapshot_xmin(NULL) AS lo,txid_visible_in_snapshot(NULL,NULL) AS v,txid_status(NULL) AS status", []string{"s", "lo", "v", "status"}, []string{"TXID_SNAPSHOT", "INT8", "BOOL", "TEXT"}, [][]any{{nil, nil, nil, nil}}))
				case "empty":
					identityBoth(t, p, b, b.DB, legacySnapshotStep(b, "SELECT x FROM txid_snapshot_xip(NULL) AS s(x)", []string{"x"}, []string{"INT8"}, nil))
				case "current":
					c := snapshotConn(t, p, b)
					fixture.LockExec(t, p.Ctx, c, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
					s := snapshotRead(t, p, c, nil)
					identityBoth(t, p, b, c, legacySnapshotStep(b, "SELECT txid_current_snapshot() AS s", []string{"s"}, []string{"TXID_SNAPSHOT"}, [][]any{{s}}))
					fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
				case "identity":
					identityBoth(t, p, b, b.DB, fixture.Select("SELECT pg_typeof(txid_current_snapshot())::text AS name,'txid_snapshot'::regtype::oid::bigint AS legacy,'pg_snapshot'::regtype::oid::bigint AS modern", []string{"name", "legacy", "modern"}, []string{"TEXT", "INT8", "INT8"}, [][]any{{"txid_snapshot", int64(2970), int64(5038)}}))
				case "routines":
					for _, tc := range []struct {
						signature string
						oid       int64
					}{{"txid_current()", 2943}, {"txid_current_if_assigned()", 3348}, {"txid_current_snapshot()", 2944}, {"txid_snapshot_xmin(txid_snapshot)", 2945}, {"txid_snapshot_xmax(txid_snapshot)", 2946}, {"txid_snapshot_xip(txid_snapshot)", 2947}, {"txid_visible_in_snapshot(bigint,txid_snapshot)", 2948}, {"txid_status(bigint)", 3360}} {
						identityBoth(t, p, b, b.DB, fixture.Select("SELECT "+snapshotQuote(tc.signature)+"::regprocedure::oid::bigint AS oid", []string{"oid"}, []string{"INT8"}, [][]any{{tc.oid}}))
					}
				}
			})
		})
	}
}

func TestReferenceLegacySnapshotHeld(t *testing.T) {
	t.Run("source", func(t *testing.T) {
		legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := snapshotConn(t, p, b)
			q := "SELECT x FROM txid_snapshot_xip($1) AS s(x) ORDER BY x"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				arg  any
				rows [][]any
			}{{"12:20:13,15,18", [][]any{{int64(13)}, {int64(15)}, {int64(18)}}}, {"12:20:", nil}, {nil, nil}, {[]byte(nil), nil}, {[]byte("12:20:13,15,18"), [][]any{{int64(13)}, {int64(15)}, {int64(18)}}}} {
				fixture.RunStep(t, p.Ctx, x, b.Recorder, legacySnapshotStep(b, q, []string{"x"}, []string{"INT8"}, tc.rows, tc.arg))
			}
			buf := []byte("12:20:13,15,18")
			rows, err := stmt.QueryContext(p.Ctx, buf)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for i := range buf {
				buf[i] = 'x'
			}
			var got []int64
			for rows.Next() {
				var n int64
				if err := rows.Scan(&n); err != nil {
					t.Fatal(err)
				}
				got = append(got, n)
			}
			if err := rows.Err(); err != nil || !reflect.DeepEqual(got, []int64{13, 15, 18}) {
				t.Fatalf("owned source %v: %v", got, err)
			}
		})
	})
	t.Run("scalar", func(t *testing.T) {
		legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := snapshotConn(t, p, b)
			q := "SELECT txid_snapshot_xmin($1) AS lo,txid_snapshot_xmax($1) AS hi,txid_visible_in_snapshot($2,$1) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				arg  any
				id   int64
				rows [][]any
			}{{"12:20:13,15,18", 14, [][]any{{int64(12), int64(20), true}}}, {"1:18446744073709551615:3", -1, [][]any{{int64(1), int64(-1), false}}}, {nil, 11, [][]any{{nil, nil, nil}}}, {[]byte(nil), 11, [][]any{{nil, nil, nil}}}, {"9223372036854775807:9223372036854775809:9223372036854775808", -9223372036854775808, [][]any{{int64(9223372036854775807), int64(-9223372036854775807), false}}}} {
				fixture.RunStep(t, p.Ctx, x, b.Recorder, legacySnapshotStep(b, q, []string{"lo", "hi", "v"}, []string{"INT8", "INT8", "BOOL"}, tc.rows, tc.arg, tc.id))
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, legacySnapshotError(q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type pg_snapshot: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad", int64(11)))
			fixture.RunStep(t, p.Ctx, x, b.Recorder, legacySnapshotStep(b, q, []string{"lo", "hi", "v"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{int64(10), int64(20), true}}, "10:20:", int64(11)))
		})
	})
}
