package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestExternalSQLProviderOwnership(t *testing.T) {
	p, provider, handler := externalSQLPhysicalScenario(t)
	c, b := externalSQLInstallNative(t, p)
	query := "SELECT id,label,flag,bytes FROM external_sql_data ORDER BY id"
	failedQuery := func() error {
		t.Helper()
		rows, err := c.QueryContext(p.Ctx, query)
		if rows != nil {
			rows.Close()
			t.Fatal("failed provider published Rows")
		}
		if err == nil {
			t.Fatal("provider contract failure succeeded")
		}
		return err
	}
	primary := errors.New("real provider publication failure")
	cleanup := errors.New("real provider close failure")
	for _, fault := range []error{primary, driver.ErrBadConn, driver.ErrSkip} {
		provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
			f.AfterOpen = func(context.Context, *fixture.ExternalSQLWire) error { return fault }
			f.CloseError = cleanup
		})
		before := len(provider.Snapshot())
		err := failedQuery()
		if fault == primary && !errors.Is(err, primary) {
			t.Fatal("cleanup replaced primary error", err)
		}
		if errors.Is(err, cleanup) || errors.Is(err, driver.ErrBadConn) || errors.Is(err, driver.ErrSkip) {
			t.Fatal("cleanup/sentinel identity escaped provider boundary", err)
		}
		if _, ok := err.(interface{ SQLState() string }); ok {
			t.Fatal("ordinary failure fabricated SQLSTATE", err)
		}
		wires := provider.Snapshot()
		if len(wires) != before+1 {
			t.Fatal("failed Open was replayed or not physically attempted")
		}
		externalSQLBackendGone(t, p.Ctx, wires[len(wires)-1].Info().BackendPID)
	}
	provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
		f.AfterOpen, f.CloseError, f.NilSuccess = nil, nil, true
	})
	if err := failedQuery(); !strings.Contains(err.Error(), "returned nil connection") {
		t.Fatal("nil success contract", err)
	}
	provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
		f.NilSuccess = false
		f.MutateResult = func(results []stuntgres.ExternalSQLResult) {
			for i := range results {
				if results[i].Tuple && results[i].RowCount > 0 {
					results[i].Cells = append(results[i].Cells, stuntgres.ExternalSQLCell{Text: "malformed actual-result width"})
				}
			}
		}
	})
	if err := failedQuery(); !strings.Contains(err.Error(), "row width") {
		t.Fatal("malformed actual remote descriptor accepted", err)
	}
	provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
		f.MutateResult = nil
	})
	for _, wire := range provider.Snapshot() {
		externalSQLBackendGone(t, p.Ctx, wire.Info().BackendPID)
	}

	var retained stuntgres.ExternalSQLSession
	var extra stuntgres.ExternalSQLConnection
	var retainedResults []stuntgres.ExternalSQLResult
	handler.action = func(ctx context.Context, r stuntgres.ForeignRequest, _ stuntgres.ExternalSQLConnection) error {
		// A second local physical session must execute while the real provider
		// callback runs: no engine/replacement read is held across this I/O.
		var answer int64
		if err := p.Native.QueryRowContext(ctx, "SELECT 42").Scan(&answer); err != nil {
			return err
		}
		if answer != 42 {
			return errors.New("nested native command changed value")
		}
		retained = r.ExternalSQL
		var err error
		extra, err = r.ExternalSQL.Open(ctx, stuntgres.ExternalSQLConnectRequest{ConninfoPresent: true, Conninfo: fixture.DSN, Role: "spoof", RoleOID: 999, ServerOID: 999, MappingOID: 999})
		if err != nil {
			return err
		}
		retainedResults, err = extra.Execute(ctx, "SELECT 7::int4 AS i,NULL::text AS n,''::text AS e")
		if err != nil {
			return err
		}
		_, mixed := r.ExternalSQL.Open(ctx, stuntgres.ExternalSQLConnectRequest{ConninfoPresent: true, Conninfo: fixture.DSN, ServerOptions: []stuntgres.ForeignOption{{Name: "host", Value: "not-used"}}})
		if mixed == nil {
			return errors.New("mixed conninfo/options accepted")
		}
		for _, invalid := range []stuntgres.ExternalSQLConnectRequest{
			{Conninfo: fixture.DSN}, // Nonempty text never implies presence.
			{ConninfoPresent: true, ServerOptions: []stuntgres.ForeignOption{}}, // Even explicit empty text is a distinct input form.
		} {
			if connection, err := r.ExternalSQL.Open(ctx, invalid); err == nil || connection != nil {
				return errors.New("invalid conninfo presence contract accepted")
			}
		}
		request := provider.LastRequestIdentity()
		if request.Role != r.Role || request.RoleOID != r.RoleOID || request.ServerOID != r.ServerOID || request.MappingOID != r.MappingOID || !request.MappingPresent {
			return errors.New("callback spoofed captured caller/mapping authority")
		}
		return nil
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, externalSQLProviderRows(false, query, externalSQLProviderExpectedRows()))
	handler.action = nil
	before := len(provider.Snapshot())
	if leaked, err := retained.Open(p.Ctx, stuntgres.ExternalSQLConnectRequest{ConninfoPresent: true, Conninfo: fixture.DSN}); err == nil || leaked != nil {
		if leaked != nil {
			leaked.Close(p.Ctx)
		}
		t.Fatal("retained operation capability opened after completion", err)
	}
	if len(provider.Snapshot()) != before {
		t.Fatal("stale capability reached transport")
	}
	want := []stuntgres.ExternalSQLCell{{Text: "7"}, {Null: true}, {Text: ""}}
	if len(retainedResults) != 1 || !reflect.DeepEqual(retainedResults[0].Cells, want) {
		t.Fatal("owned NULL/text result", retainedResults)
	}
	externalSQLCommand(t, p.Ctx, extra, "SELECT 'later'::text")
	pid := extra.Info().BackendPID
	if err := extra.Close(p.Ctx); err != nil {
		t.Fatal(err)
	}
	if err := extra.Close(p.Ctx); err != nil {
		t.Fatal("idempotent sticky Close", err)
	}
	if _, err := extra.Execute(p.Ctx, "SELECT 1"); err == nil {
		t.Fatal("closed connection executed")
	}
	if !reflect.DeepEqual(retainedResults[0].Cells, want) {
		t.Fatal("later activity/close mutated owned results")
	}
	externalSQLBackendGone(t, p.Ctx, pid)
	externalSQLContextOwnership(t, p, provider, handler, c)
	externalSQLTerminalErrorOwnership(t, p, provider, handler)

	// A provider Open that finishes after physical session end was requested
	// must close its actual PostgreSQL handle instead of publishing it. Close
	// cannot return until the callback and operation have both joined.
	dbc, err := p.NativeConnector.Connect(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	opened, canceled, release := make(chan int32, 1), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
		f.AfterOpen = func(ctx context.Context, wire *fixture.ExternalSQLWire) error {
			opened <- wire.Info().BackendPID
			select {
			case <-ctx.Done():
				close(canceled)
				<-release
			case <-release:
			}
			return nil
		}
	})
	queryDone, closeDone := make(chan struct{}), make(chan struct{})
	var queryRows driver.Rows
	var queryError, closeError error
	go func() {
		defer close(queryDone)
		queryRows, queryError = dbc.(driver.QueryerContext).QueryContext(p.Ctx, query, nil)
	}()
	defer func() {
		unblock()
		cleanup, end := context.WithTimeout(context.Background(), fixture.Liveness)
		defer end()
		select {
		case <-queryDone:
			if queryRows != nil {
				queryRows.Close()
			}
		case <-cleanup.Done():
			t.Error("publication worker failed to join")
		}
		_ = dbc.Close()
	}()
	var unpublishedPID int32
	select {
	case unpublishedPID = <-opened:
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	go func() { defer close(closeDone); closeError = dbc.Close() }()
	select {
	case <-canceled:
	case <-p.Ctx.Done():
		t.Fatal("session end did not cancel active Open", p.Ctx.Err())
	}
	select {
	case <-closeDone:
		t.Fatal("Close returned before joined Open cleanup")
	default:
	}
	unblock()
	select {
	case <-queryDone:
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	select {
	case <-closeDone:
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	if queryRows != nil || queryError == nil || closeError != nil {
		t.Fatal("publication/session-end result", queryRows, queryError, closeError)
	}
	externalSQLBackendGone(t, p.Ctx, unpublishedPID)
	provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
		f.AfterOpen = nil
	})
	externalSQLProviderValues(t, p, b, c, "external_sql_data")
}
