package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func concurrentAtomicScans(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			reader, writer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, reader, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE concurrent_base(i INT,s TEXT)", 0))
			run(fixture.Exec("INSERT INTO concurrent_base VALUES(1,'old'),(2,'old')", 2))
			run(fixture.Exec("CREATE MATERIALIZED VIEW concurrent_mv AS SELECT i,s FROM concurrent_base", 2))
			concurrentIndex(run)
			ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
			done := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					select {
					case <-done:
					case <-cleanup.Done():
						t.Error("scan worker cleanup", cleanup.Err())
					}
				}
			}()
			go func() {
				for i := range 20 {
					value := "new"
					if i%2 == 1 {
						value = "old"
					}
					result, err := writer.ExecContext(ctx, "UPDATE concurrent_base SET s=$1", value)
					if err != nil {
						done <- err
						return
					}
					count, err := result.RowsAffected()
					if err != nil || count != 2 {
						done <- fmt.Errorf("base count %d: %v", count, err)
						return
					}
					result, err = writer.ExecContext(ctx, concurrentRefreshSQL)
					if err != nil {
						done <- err
						return
					}
					count, err = result.RowsAffected()
					if err != nil || count != 0 {
						done <- fmt.Errorf("refresh count %d: %v", count, err)
						return
					}
				}
				done <- nil
			}()
			for range 40 {
				rows, err := reader.QueryContext(ctx, "SELECT a.i,a.s,b.s AS peer FROM concurrent_mv a JOIN concurrent_mv b ON a.i=b.i ORDER BY a.i")
				if err != nil {
					t.Fatal("coherent scan", err)
				}
				cols, err := rows.ColumnTypes()
				if err != nil {
					rows.Close()
					t.Fatal(err)
				}
				fixture.TableDDLColumnTypes(t, b.PG, cols, []string{"i", "s", "peer"}, []string{"INT4", "TEXT", "TEXT"}, []bool{true, true, true})
				var incarnation string
				for _, want := range []int64{1, 2} {
					if !rows.Next() {
						rows.Close()
						t.Fatal("missing atomic row", rows.Err())
					}
					var i int64
					var value, peer string
					if err := rows.Scan(&i, &value, &peer); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					if incarnation == "" {
						incarnation = value
					}
					if i != want || (value != "old" && value != "new") || value != incarnation || peer != incarnation {
						rows.Close()
						t.Fatal("mixed incarnation", i, value, peer, incarnation)
					}
				}
				extra := rows.Next()
				err = rows.Err()
				rows.Close()
				if extra || err != nil {
					t.Fatal("atomic scan tail", extra, err)
				}
			}
			select {
			case err := <-done:
				joined = true
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			run(refreshBag("concurrent_mv", [][]any{{int64(1), "old"}, {int64(2), "old"}}))
		})
	}
}
