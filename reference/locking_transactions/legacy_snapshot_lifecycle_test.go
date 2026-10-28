package reference_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLegacySnapshotLifecycle(t *testing.T) {
	for _, mode := range []string{"unopened", "partial", "eof"} {
		t.Run(mode, func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := snapshotConn(t, p, b)
				q := "SELECT x FROM txid_snapshot_xip($1) AS s(x) ORDER BY x"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err := stmt.QueryContext(p.Ctx, "12:20:13,15,18")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				if mode == "partial" {
					var n int64
					if !rows.Next() {
						t.Fatal("missing first row")
					}
					if err := rows.Scan(&n); err != nil || n != 13 {
						t.Fatalf("first row %d: %v", n, err)
					}
				}
				if mode == "eof" {
					var got []int64
					for rows.Next() {
						var n int64
						if err := rows.Scan(&n); err != nil {
							t.Fatal(err)
						}
						got = append(got, n)
					}
					if err := rows.Err(); err != nil || !reflect.DeepEqual(got, []int64{13, 15, 18}) {
						t.Fatalf("complete source %v: %v", got, err)
					}
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, x, b.Recorder, legacySnapshotStep(b, q, []string{"x"}, []string{"INT8"}, nil, nil))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, legacySnapshotStep(b, q, []string{"x"}, []string{"INT8"}, [][]any{{int64(13)}, {int64(15)}, {int64(18)}}, "12:20:13,15,18"))
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					failed, err := stmt.QueryContext(ctx, "12:20:13,15,18")
					if failed != nil {
						failed.Close()
						t.Fatal("pre-canceled query exposed rows")
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("ordinary cancellation %v", err)
					}
					fixture.RunStep(t, p.Ctx, x, b.Recorder, legacySnapshotStep(b, q, []string{"x"}, []string{"INT8"}, [][]any{{int64(15)}}, "12:20:15"))
				}
			})
		})
	}
	t.Run("retained-bytes", func(t *testing.T) {
		legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := snapshotConn(t, p, b)
			stmt, err := c.PrepareContext(p.Ctx, "SELECT $1::txid_snapshot")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			var first, second any
			if err := stmt.QueryRowContext(p.Ctx, "12:16:14,14").Scan(&first); err != nil {
				t.Fatal(err)
			}
			if err := stmt.QueryRowContext(p.Ctx, "20:30:21").Scan(&second); err != nil {
				t.Fatal(err)
			}
			a, ok := first.([]byte)
			if !ok || string(a) != "12:16:14" {
				t.Fatalf("retained first %T %v", first, first)
			}
			z, ok := second.([]byte)
			if !ok || string(z) != "20:30:21" {
				t.Fatalf("second %T %v", second, second)
			}
		})
	})
}
