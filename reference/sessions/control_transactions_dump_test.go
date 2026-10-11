package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceControlTransactionsGatedDump(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := fixture.NewScenario(t)
	fixture.CatalogControlSQLWitness(t, p)
	d := p.NativeConnector.Driver().(*stuntgres.Driver)
	db, peer := fixture.CatalogControlPool(t, d, "reference/"+t.Name()), fixture.CatalogControlPool(t, d, "reference/"+t.Name())
	peer.SetMaxOpenConns(1)
	fixture.CatalogControlAffected(t, ctx, peer, "CREATE TABLE t(v INT)", false, 0)
	fixture.CatalogControlAffected(t, ctx, peer, "INSERT INTO t VALUES(77)", false, 1)
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	defer a.ExecContext(context.Background(), "ROLLBACK")
	fixture.CatalogControlAffected(t, ctx, a, "BEGIN", false, 0)
	fixture.CatalogControlAffected(t, ctx, a, "INSERT INTO t VALUES(1)", false, 1)
	w := &fixture.CatalogControlGatedWriter{Started: make(chan struct{}), Release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- stuntgres.Dump(ctx, peer, w) }()
	released, joined := false, false
	defer func() {
		if !released {
			close(w.Release)
		}
		if !joined {
			<-done
		}
	}()
	select {
	case <-w.Started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	fixture.CatalogControlSelect(t, ctx, peer, "SELECT v FROM t", false, 77)
	fixture.CatalogControlAffected(t, ctx, peer, "INSERT INTO t VALUES(88)", false, 1)
	fixture.CatalogControlSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 77, 88)
	close(w.Release)
	released = true
	err = <-done
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	clone := fixture.CatalogControlPool(t, stuntgres.NewDriver(), t.Name()+"/clone")
	if err = stuntgres.Restore(ctx, clone, bytes.NewReader(w.Output.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.CatalogControlSelect(t, ctx, clone, "SELECT v FROM t", false, 77)
}
