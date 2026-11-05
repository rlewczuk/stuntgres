package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The wrapper delays only a genuine transport's Close. It supplies no SQL,
// connection identity or resource membership of its own.
type dropDblinkProvider struct {
	base    fixture.ExternalSQLProvider
	mu      sync.Mutex
	pid     int32
	entered chan struct{}
	release <-chan struct{}
}

type dropDblinkWire struct {
	stuntgres.ExternalSQLConnection
	provider *dropDblinkProvider
}

func (p *dropDblinkProvider) Open(ctx context.Context, r stuntgres.ExternalSQLConnectRequest) (stuntgres.ExternalSQLConnection, error) {
	c, err := p.base.Open(ctx, r)
	if c == nil {
		return nil, err
	}
	return &dropDblinkWire{c, p}, err
}

func (w *dropDblinkWire) Close(ctx context.Context) error {
	p := w.provider
	p.mu.Lock()
	pid, entered, release := p.pid, p.entered, p.release
	p.mu.Unlock()
	if pid == w.Info().BackendPID {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}
	return w.ExternalSQLConnection.Close(ctx)
}

// Host-only phase: real late Open and real Close are independently withheld.
// Receiving cancellation proves FORCE reached the owner; the close callback
// proves terminal drain, not public membership, controls DROP completion.
func dropDblinkDrain(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, provider *dropDblinkProvider, conninfo string, pid int32, force string, executor fixture.Executor, prepared bool, independentHealthy func()) {
	opened, canceled, releaseOpen := make(chan *fixture.ExternalSQLWire, 1), make(chan struct{}), make(chan struct{})
	closeEntered, releaseClose := make(chan struct{}, 1), make(chan struct{})
	var openOnce, closeOnce sync.Once
	workerCtx, cancelWorkers := context.WithCancel(p.Ctx)
	unblock := func() { openOnce.Do(func() { close(releaseOpen) }); closeOnce.Do(func() { close(releaseClose) }) }
	provider.base.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
		f.AfterOpen = func(ctx context.Context, w *fixture.ExternalSQLWire) error {
			opened <- w
			select {
			case <-ctx.Done():
				close(canceled)
				<-releaseOpen
			case <-releaseOpen:
			}
			return nil // Actual nonnil, cancellation-ignoring late return.
		}
	})
	provider.mu.Lock()
	provider.pid, provider.entered, provider.release = pid, closeEntered, releaseClose
	provider.mu.Unlock()
	queryDone, dropDone := make(chan struct{}), make(chan struct{})
	queryStarted, dropStarted := false, false
	var queryErr, dropErr error
	var pending *fixture.ExternalSQLWire
	// Register all release/cancel/join cleanup before launching either worker.
	defer func() {
		cancelWorkers()
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		for _, item := range []struct {
			started bool
			done    <-chan struct{}
		}{{queryStarted, queryDone}, {dropStarted, dropDone}} {
			if item.started {
				select {
				case <-item.done:
				case <-ctx.Done():
					t.Error("DROP resource worker did not join")
				}
			}
		}
		provider.base.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.AfterOpen = nil })
		provider.mu.Lock()
		provider.pid = 0
		provider.mu.Unlock()
	}()
	queryStarted = true
	go func() {
		defer close(queryDone)
		var value string
		if prepared {
			statement, err := c.PrepareContext(workerCtx, "SELECT dbc_ext.dblink_connect('pending',$1::text)")
			if err != nil {
				queryErr = err
				return
			}
			defer statement.Close()
			queryErr = statement.QueryRowContext(workerCtx, conninfo).Scan(&value)
		} else {
			queryErr = c.QueryRowContext(workerCtx, "SELECT dbc_ext.dblink_connect('pending',$1::text)", conninfo).Scan(&value)
		}
	}()
	select {
	case pending = <-opened:
	case <-queryDone:
		t.Fatal("pending Open did not enter real provider", queryErr)
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	dropStarted = true
	go func() {
		defer close(dropDone)
		var result sql.Result
		result, dropErr = executor.ExecContext(workerCtx, force)
		if dropErr == nil {
			n, err := result.RowsAffected()
			if err != nil || n != 0 {
				dropErr = fmt.Errorf("DROP affected rows: %d, %v", n, err)
			}
		}
	}()
	select {
	case <-canceled:
	case <-dropDone:
		t.Fatal("DROP completed without draining pending callback", dropErr)
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	health := func() {
		// These genuine same-cluster reads need metadata. Completing while the
		// callbacks are held rules out Mu spanning either wait.
		var count int
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_database WHERE datname='dbc_drop_target'").Scan(&count); err != nil || count != 1 {
			t.Fatal("metadata did not progress during resource drain", count, err)
		}
		if err := peer.QueryRowContext(p.Ctx, "SELECT 17").Scan(&count); err != nil || count != 17 {
			t.Fatal("peer owner did not progress during resource drain", count, err)
		}
		select {
		case <-dropDone:
			t.Fatal("DROP returned while real resource blocked", dropErr)
		default:
		}
	}
	health()
	independentHealthy()
	openOnce.Do(func() { close(releaseOpen) })
	select {
	case <-closeEntered:
	case <-dropDone:
		t.Fatal("DROP skipped genuine Close barrier", dropErr)
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	health()
	independentHealthy()
	closeOnce.Do(func() { close(releaseClose) })
	select {
	case <-dropDone:
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	select {
	case <-queryDone:
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	if dropErr != nil || queryErr == nil {
		t.Fatal("FORCE/pending command results", dropErr, queryErr)
	}
	if pending.Activity().Closes != 1 {
		t.Fatal("late actual Open not closed exactly once", pending.Activity())
	}
	connectionGone(t, p.Ctx, pending.Info().BackendPID)
	if _, err := pending.Execute(p.Ctx, "SELECT 1"); err == nil {
		t.Fatal("late real Open resurrected after DROP")
	}
}

// Independent Drivers deliberately reuse the original opaque key and selected
// database spelling. Neither spelling is authority over this real resource.
func dropDblinkIndependent(t *testing.T, p *fixture.Pair, provider *dropDblinkProvider) func() {
	t.Helper()
	d := stuntgres.NewDriver()
	if err := d.RegisterExternalSQLProvider(stuntgres.ExternalSQLProviderDefinition{Name: "drop_dblink", Version: fixture.ExternalSQLFixtureVersion, Provider: provider}); err != nil {
		t.Fatal(err)
	}
	if err := d.BindDblinkExternalSQLProvider("drop_dblink"); err != nil {
		t.Fatal(err)
	}
	connector, err := d.OpenConnector(p.NativeDSN)
	if err != nil {
		t.Fatal(err)
	}
	admin, _, ar := fixture.DatabaseConnectorScenarioPool(t, connector, false)
	fixture.RunStep(t, p.Ctx, admin, ar, fixture.Exec("CREATE DATABASE dbc_drop_target", 0))
	db, _, rec := fixture.DatabaseConnectorScenarioPool(t, connector.(*stuntgres.Connector).WithDatabase("dbc_drop_target"), false)
	c, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	b := fixture.Backend{DB: admin, Recorder: rec}
	s := &connectionScenario{t: t, p: p, b: b, c: c, db: db, provider: &provider.base}
	s.exec("CREATE SCHEMA dbc_ext")
	s.exec("CREATE EXTENSION dblink SCHEMA dbc_ext")
	_, pid := s.open(new("owned"))
	wire := s.wire(pid)
	before := wire.Activity()
	return func() {
		t.Helper()
		s.list("owned")
		var v int
		if err := c.QueryRowContext(p.Ctx, "SELECT 53").Scan(&v); err != nil || v != 53 {
			t.Fatal("independent Driver selected owner unhealthy", v, err)
		}
		if wire.Activity() != before {
			t.Fatal("DROP touched independent Driver transport", wire.Activity(), before)
		}
		var n int
		if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&n); err != nil || n != 1 {
			t.Fatal("independent Driver actual backend gone", n, err)
		}
		if _, err := wire.Execute(p.Ctx, "SELECT 61"); err != nil {
			t.Fatal("independent Driver real resource unusable", err)
		}
		before = wire.Activity()
	}
}
