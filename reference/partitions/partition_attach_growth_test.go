package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func partitionAttachReaderGrowth(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run("reader_growth/"+b.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			run := func(s fixture.Step) { fixture.RunStep(t, ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE pa_growth_parent(k INT) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pa_growth_old PARTITION OF pa_growth_parent FOR VALUES FROM(20) TO(30)", 0))
			run(fixture.Exec("INSERT INTO pa_growth_old VALUES(21)", 1))
			run(fixture.Exec("CREATE TABLE pa_growth_candidate(k INT)", 0))
			run(fixture.Exec("INSERT INTO pa_growth_candidate VALUES(1)", 1))
			reader, err := b.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			query := "SELECT k FROM pa_growth_parent ORDER BY k"
			if b.PG {
				// This physical PG hierarchy-lock mechanism has an explicit native
				// boundary. Native exact resource/growth concurrency is exercised
				// against the real private manager, not invented pg_locks support.
				holder, err := b.DB.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				fixture.RunStep(t, ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE ONLY pa_growth_old IN ACCESS EXCLUSIVE MODE", 0))
				var holderPID int
				if err := holder.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&holderPID); err != nil {
					t.Fatal(err)
				}
				pid := fixture.LockPID(t, ctx, reader)
				type outcome struct {
					rows *sql.Rows
					err  error
				}
				done := make(chan outcome, 1)
				joined := false
				defer func() {
					cancel()
					holder.Rollback()
					if !joined {
						select {
						case got := <-done:
							if got.rows != nil {
								got.rows.Close()
							}
						case <-time.After(5 * time.Second):
							t.Error("growth reader did not join")
						}
					}
				}()
				go func() { rows, e := reader.QueryContext(ctx, query); done <- outcome{rows, e} }()
				fixture.LockWaitBehind(t, ctx, pid, holderPID, fixture.LockOID(t, ctx, "pa_growth_old"), "AccessShareLock")
				run(fixture.Exec("ALTER TABLE pa_growth_parent ATTACH PARTITION pa_growth_candidate FOR VALUES FROM(1) TO(3)", 0))
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-done:
					joined = true
					if got.err != nil {
						t.Fatal(got.err)
					}
					if got.rows == nil {
						t.Fatal("growth query lost rows")
					}
					defer got.rows.Close()
					columns, err := got.rows.Columns()
					if err != nil || !reflect.DeepEqual(columns, []string{"k"}) {
						t.Fatalf("growth columns %#v %v", columns, err)
					}
					types, err := got.rows.ColumnTypes()
					if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
						t.Fatal("growth metadata", types, err)
					}
					var values []int64
					for got.rows.Next() {
						var k int64
						if err := got.rows.Scan(&k); err != nil {
							t.Fatal(err)
						}
						values = append(values, k)
					}
					// PG18 retains this already admitted statement's old topology;
					// the separate fresh query below observes the new attachment.
					if err := got.rows.Err(); err != nil || !reflect.DeepEqual(values, []int64{21}) {
						t.Fatalf("admitted immutable hierarchy rows %v: %v", values, err)
					}
					if err := got.rows.Close(); err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			} else {
				// Public SQL independently proves fresh post-attachment capture.
				// Private manager units retain the actual wait/growth schedule.
				run(fixture.Exec("ALTER TABLE pa_growth_parent ATTACH PARTITION pa_growth_candidate FOR VALUES FROM(1) TO(3)", 0))
			}
			fixture.RunStep(t, ctx, reader, b.Recorder, referenceRangeRows(query, 1, 21))
		})
	}
}
