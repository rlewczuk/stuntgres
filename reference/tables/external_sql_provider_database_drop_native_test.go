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

// Delay the real provider Close, not a synthetic session/resource callback.
type externalSQLDropProvider struct {
	base    fixture.ExternalSQLProvider
	mu      sync.Mutex
	pid     int32
	entered chan struct{}
	release <-chan struct{}
}
type externalSQLDropWire struct {
	stuntgres.ExternalSQLConnection
	provider *externalSQLDropProvider
}

func (p *externalSQLDropProvider) Open(ctx context.Context, r stuntgres.ExternalSQLConnectRequest) (stuntgres.ExternalSQLConnection, error) {
	c, err := p.base.Open(ctx, r)
	if c == nil {
		return nil, err
	}
	return &externalSQLDropWire{c, p}, err
}
func (w *externalSQLDropWire) Close(ctx context.Context) error {
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

// Explicit native host phase, not a PostgreSQL callback-timing parity claim.
// Open has already dialed/authenticated a genuine PostgreSQL backend, and Close
// is held before touching another genuine backend. Both must drain before DROP.
func externalSQLDropDrain(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, provider *externalSQLDropProvider, owned *fixture.ExternalSQLWire, force string, executor fixture.Executor, prepared bool, independentHealthy func()) {
	opened, canceled, releaseOpen := make(chan *fixture.ExternalSQLWire, 1), make(chan struct{}), make(chan struct{})
	entered, releaseClose := make(chan struct{}, 1), make(chan struct{})
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
			return nil
		}
	})
	provider.mu.Lock()
	provider.pid, provider.entered, provider.release = owned.Info().BackendPID, entered, releaseClose
	provider.mu.Unlock()
	queryDone, dropDone := make(chan struct{}), make(chan struct{})
	queryStarted, dropStarted := false, false
	var queryErr, dropErr error
	var pending *fixture.ExternalSQLWire
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
					t.Error("externalSQL DROP worker did not join")
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
		var rows *sql.Rows
		if prepared {
			var stmt *sql.Stmt
			stmt, queryErr = c.PrepareContext(workerCtx, "SELECT id FROM external_sql_data")
			if queryErr != nil {
				return
			}
			defer stmt.Close()
			rows, queryErr = stmt.QueryContext(workerCtx)
		} else {
			rows, queryErr = c.QueryContext(workerCtx, "SELECT id FROM external_sql_data")
		}
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
			}
			if queryErr == nil {
				queryErr = rows.Err()
			}
		}
	}()
	select {
	case pending = <-opened:
	case <-queryDone:
		t.Fatal("foreign gateway did not enter actual Open", queryErr)
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
			if n != 0 || err != nil {
				dropErr = fmt.Errorf("DROP affected rows: %d, %v", n, err)
			}
		}
	}()
	select {
	case <-canceled:
	case <-dropDone:
		t.Fatal("DROP did not cancel pending foreign callback", dropErr)
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	health := func() {
		var n int
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_database WHERE datname='external_drop_target'").Scan(&n); err != nil || n != 1 {
			t.Fatal("metadata blocked during real resource drain", n, err)
		}
		if err := peer.QueryRowContext(p.Ctx, "SELECT 41").Scan(&n); err != nil || n != 41 {
			t.Fatal("peer blocked during real resource drain", n, err)
		}
		select {
		case <-dropDone:
			t.Fatal("DROP returned before genuine drain", dropErr)
		default:
		}
	}
	health()
	independentHealthy()
	openOnce.Do(func() { close(releaseOpen) })
	select {
	case <-entered:
	case <-dropDone:
		t.Fatal("DROP skipped actual owned Close", dropErr)
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
		t.Fatal("DROP/pending foreign query outcomes", dropErr, queryErr)
	}
	if pending.Activity().Closes != 1 {
		t.Fatal("late foreign Open not closed exactly once", pending.Activity())
	}
	externalSQLBackendGone(t, p.Ctx, pending.Info().BackendPID)
	if _, err := pending.Execute(p.Ctx, "SELECT 1"); err == nil {
		t.Fatal("late foreign Open resurrected after DROP")
	}
}

// Same opaque key and database name in another Driver must not grant the
// dropping cluster any authority over this genuine FDW/capability resource.
func externalSQLDropIndependent(t *testing.T, p *fixture.Pair, provider *externalSQLDropProvider) func() {
	t.Helper()
	d, handler := stuntgres.NewDriver(), &externalSQLHandler{}
	if err := d.RegisterExternalSQLProvider(stuntgres.ExternalSQLProviderDefinition{Name: "external_sql_fixture", Version: fixture.ExternalSQLFixtureVersion, Provider: provider}); err != nil {
		t.Fatal(err)
	}
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Name: "external_sql_fixture_handler"}, ExternalProvider: "external_sql_fixture", Wrapper: handler}); err != nil {
		t.Fatal(err)
	}
	connector, err := d.OpenConnector(p.NativeDSN)
	if err != nil {
		t.Fatal(err)
	}
	admin, _, ar := fixture.DatabaseConnectorScenarioPool(t, connector, false)
	fixture.RunStep(t, p.Ctx, admin, ar, fixture.Exec("CREATE DATABASE external_drop_target", 0))
	db, _, rec := fixture.DatabaseConnectorScenarioPool(t, connector.(*stuntgres.Connector).WithDatabase("external_drop_target"), false)
	c, err := db.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	b := fixture.Backend{DB: admin, Recorder: rec}
	externalSQLProviderInstallPhysical(t, b, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) })
	var owned stuntgres.ExternalSQLConnection
	handler.action = func(ctx context.Context, r stuntgres.ForeignRequest, _ stuntgres.ExternalSQLConnection) error {
		var err error
		owned, err = r.ExternalSQL.Open(ctx, stuntgres.ExternalSQLConnectRequest{})
		return err
	}
	fixture.RunStep(t, p.Ctx, c, rec, externalSQLProviderRows(false, "SELECT id,label,flag,bytes FROM external_sql_data ORDER BY id", externalSQLProviderExpectedRows()))
	handler.action = nil
	if owned == nil {
		t.Fatal("independent Driver did not retain genuine resource")
	}
	var wire *fixture.ExternalSQLWire
	for _, w := range provider.base.Snapshot() {
		if w.Info().BackendPID == owned.Info().BackendPID {
			wire = w
		}
	}
	if wire == nil {
		t.Fatal("independent Driver missing real transport")
	}
	before := wire.Activity()
	return func() {
		t.Helper()
		var v int
		if err := c.QueryRowContext(p.Ctx, "SELECT 59").Scan(&v); err != nil || v != 59 {
			t.Fatal("independent Driver selected owner unhealthy", v, err)
		}
		if wire.Activity() != before {
			t.Fatal("DROP touched independent Driver transport", wire.Activity(), before)
		}
		var n int
		if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1", wire.Info().BackendPID).Scan(&n); err != nil || n != 1 {
			t.Fatal("independent Driver real backend gone", n, err)
		}
		if _, err := owned.Execute(p.Ctx, "SELECT 67"); err != nil {
			t.Fatal("independent Driver real resource unusable", err)
		}
		before = wire.Activity()
	}
}
