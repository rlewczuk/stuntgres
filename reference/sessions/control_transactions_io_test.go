package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceControlTransactionsGatedIO(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "install", true: "cancel"}[canceled], func(t *testing.T) {
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
			r := &fixture.CatalogControlGatedReader{Started: make(chan struct{}), Release: make(chan struct{})}
			c, stop := context.WithCancel(ctx)
			defer stop()
			done := make(chan error, 1)
			go func() { done <- stuntgres.Restore(c, peer, r) }()
			released, joined := false, false
			defer func() {
				if !released {
					close(r.Release)
				}
				if !joined {
					<-done
				}
			}()
			select {
			case <-r.Started:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			fixture.CatalogControlSelect(t, ctx, peer, "SELECT v FROM t", false, 77)
			fixture.CatalogControlAffected(t, ctx, peer, "INSERT INTO t VALUES(88)", false, 1)
			fixture.CatalogControlSelect(t, ctx, a, "SELECT v FROM t ORDER BY v", false, 1, 77, 88)
			if canceled {
				stop()
			}
			close(r.Release)
			released = true
			err = <-done
			joined = true
			if canceled {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				fixture.CatalogControlAffected(t, ctx, a, "COMMIT", false, 0)
				fixture.CatalogControlSelect(t, ctx, peer, "SELECT v FROM t ORDER BY v", false, 1, 77, 88)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				_, err = a.ExecContext(ctx, "SELECT v FROM t")
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				fixture.CatalogControlSelect(t, ctx, peer, "SELECT v FROM replaced", false, 99)
			}
		})
	}
}
