package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceControlTransactionsGatedDump(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := newReferenceScenario(t)
	catalogControlSQLWitness(t, p)
	d := p.nativeConnector.Driver().(*stuntgres.Driver)
	db, peer := catalogControlPool(t, d, "reference/"+t.Name()), catalogControlPool(t, d, "reference/"+t.Name())
	peer.SetMaxOpenConns(1)
	catalogControlAffected(t, ctx, peer, "CREATE TABLE t(v INT)", false, 0)
	catalogControlAffected(t, ctx, peer, "INSERT INTO t VALUES(77)", false, 1)
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	defer a.ExecContext(context.Background(), "ROLLBACK")
	catalogControlAffected(t, ctx, a, "BEGIN", false, 0)
	catalogControlAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	w := &catalogControlGatedWriter{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- stuntgres.Dump(ctx, peer, w) }()
	released, joined := false, false
	defer func() {
		if !released {
			close(w.release)
		}
		if !joined {
			<-done
		}
	}()
	select {
	case <-w.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	catalogControlSelect(t, ctx, peer, "SELECT v FROM t", false, 77)
	catalogControlAffected(t, ctx, peer, "INSERT INTO t VALUES(88)", false, 1)
	catalogControlSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 77, 88)
	close(w.release)
	released = true
	err = <-done
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	clone := catalogControlPool(t, stuntgres.NewDriver(), t.Name()+"/clone")
	if err = stuntgres.Restore(ctx, clone, bytes.NewReader(w.output.Bytes())); err != nil {
		t.Fatal(err)
	}
	catalogControlSelect(t, ctx, clone, "SELECT v FROM t", false, 77)
}
