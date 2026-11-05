package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func externalSQLWaitBlocked(t *testing.T, ctx context.Context, pid int32) {
	t.Helper()
	for {
		var waiting bool
		q := "SELECT EXISTS(SELECT 1 FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid WHERE a.pid=$1 AND a.state='active' AND a.wait_event_type='Lock' AND l.locktype='advisory' AND NOT l.granted)"
		if err := fixture.Admin.QueryRowContext(ctx, q, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("provider query never acquired real lock wait", pid, err)
		}
		runtime.Gosched()
	}
}

func externalSQLLock(t *testing.T, p *fixture.Pair, key int64) {
	t.Helper()
	holder, err := fixture.Admin.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		if _, err := holder.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
			t.Error(err)
		}
		if err := holder.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := holder.ExecContext(p.Ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceExternalSQLProviderCancellation(t *testing.T) {
	p, provider, handler := externalSQLPhysicalScenario(t)
	const key int64 = 718031
	externalSQLLock(t, p, key)
	query := fmt.Sprintf("SELECT pg_advisory_xact_lock(%d)", key)
	raw := externalSQLRawConnection(t, p, provider, stuntgres.ExternalSQLConnectRequest{ConninfoPresent: true, Conninfo: fixture.DSN})
	for _, transaction := range []bool{false, true} {
		if transaction {
			externalSQLCommand(t, p.Ctx, raw, "BEGIN")
		}
		var results []stuntgres.ExternalSQLResult
		var failure error
		done := make(chan struct{})
		go func() { defer close(done); results, failure = raw.Execute(p.Ctx, query) }()
		func() {
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				_ = raw.Cancel(ctx)
				select {
				case <-done:
				case <-ctx.Done():
					t.Error("raw canceled worker did not join", ctx.Err())
				}
			}()
			externalSQLWaitBlocked(t, p.Ctx, raw.Info().BackendPID)
			if err := raw.Cancel(p.Ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
			want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
			fixture.AssertDiagnostic(t, failure, &want)
			if len(results) != 0 {
				t.Fatal("canceled Execute published rows", results)
			}
		}()
		if transaction {
			_, err := raw.Execute(p.Ctx, "SELECT 42")
			want := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
			fixture.AssertDiagnostic(t, err, &want)
			externalSQLCommand(t, p.Ctx, raw, "ROLLBACK")
		}
		got := externalSQLCommand(t, p.Ctx, raw, "SELECT 42 AS answer")
		if len(got) != 1 || got[0].RowCount != 1 || len(got[0].Cells) != 1 || got[0].Cells[0].Null || got[0].Cells[0].Text != "42" {
			t.Fatal("reused canceled connection", got)
		}
	}
	if err := raw.Cancel(p.Ctx); err != nil {
		t.Fatal("idle cancellation dispatch", err)
	}
	got := externalSQLCommand(t, p.Ctx, raw, "SELECT 42")
	if len(got) != 1 || got[0].Cells[0].Text != "42" {
		t.Fatal("idle cancel fabricated a canceled result", got)
	}

	c, b := externalSQLInstallNative(t, p)
	for _, prepared := range []bool{false, true} {
		for _, callerCancel := range []bool{false, true} {
			func() {
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				started := make(chan stuntgres.ExternalSQLConnection, 1)
				handler.action = func(ctx context.Context, _ stuntgres.ForeignRequest, connection stuntgres.ExternalSQLConnection) error {
					started <- connection
					_, err := connection.Execute(ctx, query)
					return err
				}
				const q = "SELECT id,label,flag,bytes FROM external_sql_data ORDER BY id"
				var stmt *sql.Stmt
				if prepared {
					before := len(provider.Snapshot())
					var err error
					stmt, err = c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					if len(provider.Snapshot()) != before {
						t.Fatal("Describe opened an external connection")
					}
				}
				var rows *sql.Rows
				var failure error
				done := make(chan struct{})
				go func() {
					defer close(done)
					if stmt != nil {
						rows, failure = stmt.QueryContext(ctx)
					} else {
						rows, failure = c.QueryContext(ctx, q)
					}
				}()
				var connection stuntgres.ExternalSQLConnection
				defer func() {
					cancel()
					cleanup, end := context.WithTimeout(context.Background(), fixture.Liveness)
					defer end()
					if connection != nil {
						_ = connection.Cancel(cleanup)
					}
					select {
					case <-done:
						if rows != nil {
							rows.Close()
						}
					case <-cleanup.Done():
						t.Error("native canceled worker did not join")
					}
				}()
				select {
				case connection = <-started:
				case <-done:
					t.Fatal("scan ended before lock admission", failure)
				case <-p.Ctx.Done():
					t.Fatal(p.Ctx.Err())
				}
				externalSQLWaitBlocked(t, p.Ctx, connection.Info().BackendPID)
				if callerCancel {
					cancel()
				} else if err := connection.Cancel(p.Ctx); err != nil {
					t.Fatal(err)
				}
				select {
				case <-done:
				case <-p.Ctx.Done():
					t.Fatal(p.Ctx.Err())
				}
				if rows != nil {
					t.Fatal("canceled public query exposed Rows")
				}
				if callerCancel {
					if !errors.Is(failure, context.Canceled) {
						t.Fatal("caller cancellation identity", failure)
					}
				} else {
					want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					fixture.AssertDiagnostic(t, failure, &want)
				}
				externalSQLBackendGone(t, p.Ctx, connection.Info().BackendPID)
			}()
		}
	}
	handler.action = nil
	externalSQLProviderValues(t, p, b, c, "external_sql_data")
}
