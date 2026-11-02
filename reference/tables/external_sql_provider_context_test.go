package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func externalSQLContextOwnership(t *testing.T, p *fixture.Pair, provider *externalSQLProvider, handler *externalSQLHandler, first *sql.Conn) {
	t.Helper()
	type authority struct {
		ctx        context.Context
		opener     stuntgres.ExternalSQLSession
		connection stuntgres.ExternalSQLConnection
	}
	available := make(chan authority, 1)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	var invocations atomic.Int32
	var a authority
	handler.action = func(ctx context.Context, r stuntgres.ForeignRequest, connection stuntgres.ExternalSQLConnection) error {
		if invocations.Add(1) == 1 {
			available <- authority{ctx, r.ExternalSQL, connection}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		before := len(provider.snapshot())
		foreign, err := a.opener.Open(ctx, stuntgres.ExternalSQLConnectRequest{Conninfo: fixture.DSN})
		if err == nil || foreign != nil {
			if foreign != nil {
				foreign.Close(ctx)
			}
			return errors.New("live opener accepted sibling callback context")
		}
		if len(provider.snapshot()) != before {
			return errors.New("foreign callback context reached transport")
		}
		if _, err := a.connection.Execute(ctx, "SELECT 1"); err == nil {
			return errors.New("owned connection accepted sibling execution context")
		}
		// Trusted host delegation retaining A's exact live bearer context is
		// explicit. It must not be confused with B's execution authority.
		delegated, err := a.opener.Open(a.ctx, stuntgres.ExternalSQLConnectRequest{Conninfo: fixture.DSN})
		if err != nil {
			return err
		}
		_, err = delegated.Execute(a.ctx, "SELECT 7")
		cleanup := delegated.Close(a.ctx)
		if err != nil {
			return err
		}
		return cleanup
	}
	var rows *sql.Rows
	var failure error
	done := make(chan struct{})
	const q = "SELECT id,label,flag,bytes FROM external_sql_data ORDER BY id"
	go func() { defer close(done); rows, failure = first.QueryContext(p.Ctx, q) }()
	defer func() {
		unblock()
		cleanup, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		select {
		case <-done:
			if rows != nil {
				rows.Close()
			}
		case <-cleanup.Done():
			t.Error("cross-session owner worker did not join")
		}
		handler.action = nil
	}()
	select {
	case a = <-available:
	case <-done:
		t.Fatal("first physical callback did not stay live", failure)
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	db, recorder := fixture.RecordedNativePool(t, p.NativeConnector)
	db.SetMaxIdleConns(0)
	defer db.Close()
	fixture.RunStep(t, p.Ctx, db, recorder, externalSQLProviderRows(false, q, externalSQLProviderExpectedRows()))
	unblock()
	select {
	case <-done:
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	if failure != nil || rows == nil {
		t.Fatal("correct first callback context failed", failure)
	}
	externalSQLOldRows(t, rows)
	if stale, err := a.opener.Open(a.ctx, stuntgres.ExternalSQLConnectRequest{Conninfo: fixture.DSN}); err == nil || stale != nil {
		if stale != nil {
			stale.Close(p.Ctx)
		}
		t.Fatal("completed callback opener accepted", err)
	}
}
