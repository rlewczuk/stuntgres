package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

type fdwParallelCounts struct{ begins, ends, opened, closed int }

func (r *fdwParallelReader) parallelCounts() fdwParallelCounts {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fdwParallelCounts{r.begins, r.ends, r.opened, r.closed}
}
func fdwParallelBalanced(t *testing.T, r *fdwParallelReader) {
	t.Helper()
	c := r.parallelCounts()
	if c.begins != c.ends || c.opened != c.closed {
		t.Fatal("unreleased partition resources", c)
	}
	reader := r.snapshot()
	if reader.openPools != 0 || reader.openRows != 0 || reader.openCursors != 0 {
		t.Fatal("unreleased SQL capture", reader)
	}
}

// Gates are entered by real partition consumption in production execution. Every
// assertion-failure exit cancels and joins before touching provider configuration.
func fdwParallelGate(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fdwParallelReader, cancelWork bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	r.entered = make(chan int, 2)
	release := make(chan struct{})
	r.release = release
	r.partitions = 2
	type outcome struct {
		rows *sql.Rows
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		rows, err := c.QueryContext(ctx, "SELECT id FROM fdw_data ORDER BY id")
		done <- outcome{rows, err}
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			out := <-done
			if out.rows != nil {
				out.rows.Close()
			}
		}
		r.entered = nil
		r.release = nil
	}()
	seen := map[int]bool{}
	for range 2 {
		select {
		case index := <-r.entered:
			if seen[index] {
				t.Fatal("duplicate partition", index)
			}
			seen[index] = true
		case out := <-done:
			joined = true
			if out.rows != nil {
				out.rows.Close()
			}
			t.Fatal("query completed before independent partition consumption", out.err)
		case <-ctx.Done():
			t.Fatal("two real partitions did not enter", ctx.Err())
		}
	}
	if cancelWork {
		cancel()
	} else {
		close(release)
	}
	out := <-done
	joined = true
	if cancelWork {
		if out.rows != nil {
			out.rows.Close()
			t.Fatal("canceled eager query published rows")
		}
		if !errors.Is(out.err, context.Canceled) {
			t.Fatal(out.err)
		}
	} else {
		if out.err != nil {
			t.Fatal(out.err)
		}
		defer out.rows.Close()
		var got []int64
		for out.rows.Next() {
			var id int64
			if err := out.rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			got = append(got, id)
		}
		if err := out.rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, []int64{1, 2, 3, 4, 5, 6}) {
			t.Fatal(got)
		}
	}
	fdwParallelBalanced(t, r)
}

func fdwParallelFailures(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fdwParallelReader, run func(fixture.Step)) {
	t.Helper()
	for _, phase := range []string{"Begin", "Partition", "Iterate", "End", "EndShared"} {
		failure := errors.New("parallel provider failed at " + phase)
		r.hook = func(ctx context.Context, at string, index int) error {
			if at == phase && (index <= 0 || phase == "Partition") {
				return failure
			}
			return ctx.Err()
		}
		fdwHostQueryError(t, p.Ctx, c, "SELECT id FROM fdw_data ORDER BY id", failure)
		r.hook = nil
		fdwParallelBalanced(t, r)
		run(fdwParallelIDs("SELECT id FROM fdw_data ORDER BY id", 1, 2, 3, 4, 5, 6))
	}
	primary, secondary := errors.New("primary partition read"), errors.New("secondary partition cleanup")
	r.hook = func(ctx context.Context, phase string, index int) error {
		if phase == "Iterate" && index == 0 {
			return primary
		}
		if phase == "Iterate" {
			<-ctx.Done()
			return ctx.Err()
		}
		if phase == "End" || phase == "EndShared" {
			if ctx.Err() != nil {
				return errors.New("canceled cleanup")
			}
			return secondary
		}
		return ctx.Err()
	}
	fdwHostQueryError(t, p.Ctx, c, "SELECT id FROM fdw_data ORDER BY id", primary)
	r.hook = nil
	fdwParallelBalanced(t, r)
}
