package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strconv"
	"stuntgres/dual"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
	"time"
)

func TestReferenceDualConnectorConcurrency(t *testing.T) {
	p := fixture.NewScenario(t)
	d := newDualPair(t, p, dual.Options{}, nil)
	const workers, direct, prepared = 4, 25, 5
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()

	// issued[w] is worker w's parameter sequence: direct queries, then the
	// prepared statement's executions.
	issued := make([][]string, workers)
	for w := range workers {
		for i := range direct + prepared {
			issued[w] = append(issued[w], strconv.Itoa(w*1000+i))
		}
	}
	// Every worker holds its own connection before any starts, so the pool
	// cannot hand one worker's connection to another.
	conns := make([]*sql.Conn, workers)
	for w := range conns {
		c, err := d.DB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns[w] = c
	}
	barrier := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for w, c := range conns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- dualWorker(ctx, c, barrier, issued[w][:direct], issued[w][direct:])
		}()
	}
	close(barrier)
	joined := make(chan struct{})
	go func() {
		wg.Wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(fixture.Liveness):
		cancel()
		<-joined
		t.Fatal("workers did not finish")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	byConn := map[uint64][]dualRecord{}
	var order []uint64
	for _, r := range d.Log.records(t) {
		if _, ok := byConn[r.ConnectionID]; !ok {
			order = append(order, r.ConnectionID)
		}
		byConn[r.ConnectionID] = append(byConn[r.ConnectionID], r)
		if r.ResponseDiff != nil || r.Unmirrored != "" {
			t.Errorf("connection %d cycle %d %s: diff %q unmirrored %q", r.ConnectionID, r.Cycle, r.Call, r.ResponseDiff, r.Unmirrored)
		}
	}
	if len(byConn) != workers {
		t.Fatalf("%d connections, want %d", len(byConn), workers)
	}
	seen := map[string]bool{}
	for _, id := range order {
		var params []string
		for i, r := range byConn[id] {
			if r.Cycle != uint64(i+1) {
				t.Fatalf("connection %d: record %d has cycle %d", id, i, r.Cycle)
			}
			for _, prm := range r.Params {
				params = append(params, *prm.Value)
			}
		}
		if len(params) == 0 {
			t.Fatalf("connection %d issued nothing", id)
		}
		w, err := strconv.Atoi(params[0])
		if err != nil || w%1000 != 0 || w/1000 >= workers || seen[params[0]] {
			t.Fatalf("connection %d: first parameter %q", id, params[0])
		}
		seen[params[0]] = true
		if want := issued[w/1000]; !slices.Equal(params, want) {
			t.Errorf("connection %d parameters %q, want %q", id, params, want)
		}
	}
}

// dualWorker runs one worker's statements on its own connection and checks
// every caller result.
func dualWorker(ctx context.Context, c *sql.Conn, barrier <-chan struct{}, direct, prepared []string) error {
	defer c.Close()
	<-barrier
	check := func(got int64, err error, want string) error {
		if err != nil {
			return err
		}
		if strconv.FormatInt(got, 10) != want {
			return fmt.Errorf("result %d, want %s", got, want)
		}
		return nil
	}
	for _, v := range direct {
		n, _ := strconv.Atoi(v)
		var got int64
		err := c.QueryRowContext(ctx, "SELECT $1::int + 0", n).Scan(&got)
		if err := check(got, err, v); err != nil {
			return err
		}
	}
	stmt, err := c.PrepareContext(ctx, "SELECT $1::int * 1")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, v := range prepared {
		n, _ := strconv.Atoi(v)
		var got int64
		err := stmt.QueryRowContext(ctx, n).Scan(&got)
		if err := check(got, err, v); err != nil {
			return err
		}
	}
	return nil
}
