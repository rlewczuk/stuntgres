package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func datetimeExtractAtomicity(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
	run(referenceExec("CREATE TABLE dx_atomic(id integer,f text,s timestamp,v float8)", 0))
	run(referenceExec("INSERT INTO dx_atomic VALUES(1,'year',TIMESTAMP '2000-01-01',2000)", 1))
	want := referenceSelect("SELECT id,v FROM dx_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(2000)}})
	for _, prepared := range []bool{false, true} {
		q := "INSERT INTO dx_atomic(id,v) VALUES(2,date_part($1::text,$2::timestamp)),(3,date_part($3::text,$4::timestamp))"
		s := referenceErrorDiagnostic(q, datetimeExtractFieldDiagnostic("22023", "bogus", "timestamp without time zone"), "year", "2001-01-01", "bogus", "2002-01-01")
		s.prepared, s.errorStage = prepared, "execute"
		run(s)
		run(want)
		q = "UPDATE dx_atomic SET v=date_part($1::text,s)"
		s = referenceErrorDiagnostic(q, datetimeExtractFieldDiagnostic("22023", "bogus", "timestamp without time zone"), "bogus")
		s.prepared, s.errorStage = prepared, "execute"
		run(s)
		run(want)
	}
	if !b.pg {
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		for _, q := range []string{"SELECT EXTRACT(year FROM s) FROM dx_atomic", "INSERT INTO dx_atomic(id,v) VALUES(2,date_part('year',TIMESTAMP '2001-01-01'))"} {
			result, err := c.ExecContext(ctx, q)
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("precanceled result=%v err=%v", result, err)
			}
		}
		run(want)
	}
}

func TestReferenceDatetimeExtractAtomicityCancellation(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		datetimeExtractAtomicity(t, p, b, c)
		stmt, err := c.PrepareContext(p.ctx, "SELECT date_part('year',$1::timestamp) AS year")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if b.pg {
			referenceSessionCancel(t, p.ctx, c, true, true)
		}
		x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: "SELECT date_part('year',$1::timestamp) AS year"}
		referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect(x.query, []string{"year"}, []string{"FLOAT8"}, [][]any{{float64(2000)}}, "2000-01-01"))
	})
}

func TestReferenceDatetimeExtractConcurrentHeldFrames(t *testing.T) {
	datetimeExtractScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		// A DB statement can use separate physical sessions; explicit timestamp
		// fields avoid inventing a pooled-session setting guarantee.
		q := "SELECT date_part($1::text,$2::timestamp),EXTRACT(second FROM $2::timestamp)"
		stmt, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		ctx, cancel := context.WithCancel(p.ctx)
		defer cancel()
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for _, tc := range []struct {
			field, source, second string
			want                  float64
		}{{"year", "2000-01-01 00:00:28.123456", "28.123456", 2000}, {"minute", "2001-01-01 00:42:07.654321", "7.654321", 42}} {
			workers.Add(1)
			go func(field, source, second string, want float64) {
				defer workers.Done()
				select {
				case <-start:
				case <-ctx.Done():
					results <- ctx.Err()
					return
				}
				for range 8 {
					var f float64
					var n string
					if err := stmt.QueryRowContext(ctx, field, source).Scan(&f, &n); err != nil {
						results <- err
						return
					}
					if f != want || n != second {
						results <- fmt.Errorf("frame %s: %g %s want %g %s", field, f, n, want, second)
						return
					}
				}
				results <- nil
			}(tc.field, tc.source, tc.second, tc.want)
		}
		// Cleanup is registered before admission and joins every worker, even on failure.
		t.Cleanup(func() { cancel(); workers.Wait() })
		close(start)
		for range 2 {
			select {
			case err := <-results:
				if err != nil {
					t.Error(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		workers.Wait()
	})
}
