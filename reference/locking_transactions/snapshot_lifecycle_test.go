package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotLifecycle(t *testing.T) {
	for _, mode := range []string{"unopened", "partial", "eof"} {
		t.Run(mode, func(t *testing.T) {
			snapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := snapshotConn(t, p, b)
				q := "SELECT x FROM pg_snapshot_xip($1) AS s(x) ORDER BY x"
				held, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				rows, err := held.QueryContext(p.Ctx, "10:20:10,14,15")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				snapshotMetadata(t, b, rows, []string{"XID8"})
				if mode == "partial" || mode == "eof" {
					if !rows.Next() {
						t.Fatal("missing first member", rows.Err())
					}
					var first any
					if err := rows.Scan(&first); err != nil || fixture.Normalize(first) != "10" {
						t.Fatalf("first member %v: %v", first, err)
					}
					if mode == "eof" {
						for _, want := range []string{"14", "15"} {
							if !rows.Next() {
								t.Fatal("missing member", rows.Err())
							}
							var got string
							if err := rows.Scan(&got); err != nil || got != want {
								t.Fatalf("member %q want %q: %v", got, want, err)
							}
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal("source EOF", rows.Err())
						}
						if fixture.Normalize(first) != "10" {
							t.Fatal("retained first cell changed", first)
						}
					}
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal("repeated Close", err)
				}
				x := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
				fixture.RunStep(t, p.Ctx, x, b.Recorder, snapshotTypedStep(b, q, []string{"x"}, []string{"XID8"}, [][]any{{"10"}, {"14"}, {"15"}}, "10:20:10,14,15"))
				fixture.RunStep(t, p.Ctx, x, b.Recorder, snapshotTypedStep(b, q, []string{"x"}, []string{"XID8"}, nil, nil))
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					failed, err := held.QueryContext(ctx, "10:20:10,14,15")
					if failed != nil {
						failed.Close()
						t.Fatal("canceled query exposed rows")
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("pre-canceled query %v", err)
					}
					fixture.RunStep(t, p.Ctx, x, b.Recorder, snapshotTypedStep(b, q, []string{"x"}, []string{"XID8"}, [][]any{{"14"}}, "10:20:14"))
				}
			})
		})
	}
}
