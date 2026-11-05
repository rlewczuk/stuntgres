package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDblinkConnectionCleanup(t *testing.T) {
	t.Run("physical-session-close", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			_, one := s.open(new("abandoned_one"))
			_, two := s.open(new("abandoned_two"))
			_, defaultPID := s.open(nil)
			if err := s.c.Close(); err != nil {
				s.t.Fatal(err)
			}
			if err := s.db.Close(); err != nil {
				s.t.Fatal(err)
			}
			s.closed = true
			for _, pid := range []int32{one, two, defaultPID} {
				if !s.b.PG {
					s.assertClosed(pid)
				} else {
					connectionGone(t, s.p.Ctx, pid)
				}
			}
		})
	})
	t.Run("physical-session-kill", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			_, named := s.open(new("killed_named"))
			_, defaultPID := s.open(nil)
			var local int64
			if err := s.c.QueryRowContext(s.p.Ctx, "SELECT pg_backend_pid()").Scan(&local); err != nil {
				t.Fatal(err)
			}
			var killed bool
			if err := s.b.DB.QueryRowContext(s.p.Ctx, "SELECT pg_terminate_backend($1)", local).Scan(&killed); err != nil || !killed {
				t.Fatal("physical session termination", killed, err)
			}
			if err := s.c.Close(); err != nil {
				t.Fatal(err)
			}
			if err := s.db.Close(); err != nil {
				t.Fatal(err)
			}
			s.closed = true
			for _, pid := range []int32{named, defaultPID} {
				if s.b.PG {
					connectionGone(t, s.p.Ctx, pid)
				} else {
					s.assertClosed(pid)
				}
			}
		})
	})
	t.Run("published-restore-close-error", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			if s.b.PG {
				return
			}
			cleanup := errors.New("genuine postpublication dblink close failure")
			s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.CloseError = cleanup })
			defer s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.CloseError = nil })
			_, pid := s.open(new("published_cleanup"))
			dump := fixture.ExtensionDump(t, s.p.Ctx, s.p.Native)
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump)); !errors.Is(err, cleanup) {
				t.Fatal("published cleanup result", err)
			}
			s.assertClosed(pid)
			s.list()
			s.run(connectionError("SELECT dbc_ext.dblink_disconnect('published_cleanup') AS value", "08003", `connection "published_cleanup" not available`))
		})
	})
	t.Run("nonnil-error-and-joined-close", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			if s.b.PG {
				return
			} // Explicit host-contract phase, not a PG SQL rule.
			primary := errors.New("genuine dblink provider publication rejected")
			cleanup := errors.New("genuine dblink provider close failure")
			s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
				f.AfterOpen = func(context.Context, *fixture.ExternalSQLWire) error { return primary }
				f.CloseError = cleanup
			})
			defer s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { *f = fixture.ExternalSQLFaults{} })
			before := len(s.provider.Snapshot())
			_, err := s.c.ExecContext(s.p.Ctx, "SELECT dbc_ext.dblink_connect($1::text,$2::text)", "failed", s.conninfo("dbc_failed_publication", false))
			if err == nil || !errors.Is(err, primary) || errors.Is(err, cleanup) {
				s.t.Fatal("primary/cleanup precedence", err)
			}
			if _, ok := err.(interface{ SQLState() string }); ok {
				s.t.Fatal("ordinary host failure fabricated SQLSTATE", err)
			}
			wires := s.provider.Snapshot()
			if len(wires) != before+1 {
				s.t.Fatal("Open replayed or missing genuine wire")
			}
			s.assertClosed(wires[len(wires)-1].Info().BackendPID)
			s.list()
		})
	})
	t.Run("pending-open-physical-close", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			if !s.b.PG {
				connectionPending(s, "close")
			}
		})
	})
	t.Run("pending-open-restore", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			if !s.b.PG {
				connectionPending(s, "restore")
			}
		})
	})
	t.Run("pending-open-reset", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			if !s.b.PG {
				connectionPending(s, "reset")
			}
		})
	})
	t.Run("canceled-connect-u-restores-role", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			if !s.b.PG {
				connectionDefinerInFlight(s, false)
			}
		})
	})
	t.Run("revoked-during-connect-u-closes-wire", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			if !s.b.PG {
				connectionDefinerInFlight(s, true)
			}
		})
	})
	t.Run("nil-success-is-not-a-handle", func(t *testing.T) {
		connectionCase(t, true, func(s *connectionScenario) {
			if s.b.PG {
				return
			}
			s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.NilSuccess = true })
			defer s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.NilSuccess = false })
			before := len(s.provider.Snapshot())
			rows, err := s.c.QueryContext(s.p.Ctx, "SELECT dbc_ext.dblink_connect($1::text,$2::text)", "nil", s.conninfo("dbc_nil_success", false))
			if rows != nil {
				rows.Close()
			}
			if err == nil {
				t.Fatal("nil provider result fabricated a connection")
			}
			if _, sql := err.(interface{ SQLState() string }); sql {
				t.Fatal("nil contract failure fabricated SQLSTATE", err)
			}
			wires := s.provider.Snapshot()
			if len(wires) != before+1 {
				t.Fatal("negative contract did not open genuine wire")
			}
			s.assertClosed(wires[len(wires)-1].Info().BackendPID)
			s.list()
		})
	})
}

func connectionPending(s *connectionScenario, action string) {
	t := s.t
	physical, err := s.p.NativeConnector.Connect(s.p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	dump, incompatibleDump := "", ""
	if action != "close" {
		dump = fixture.ExtensionDump(t, s.p.Ctx, s.p.Native)
		raw, c, r, _ := connectionDestination(t, s.p, "pending-incompatible-version", true)
		for _, q := range []string{"CREATE SCHEMA dbc_ext", "CREATE EXTENSION dblink SCHEMA dbc_ext"} {
			fixture.RunStep(t, s.p.Ctx, c, r, fixture.Exec(q, 0))
		}
		incompatibleDump = fixture.ExtensionDump(t, s.p.Ctx, raw)
	}
	opened, canceled, release := make(chan int32, 1), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
		f.AfterOpen = func(ctx context.Context, w *fixture.ExternalSQLWire) error {
			opened <- w.Info().BackendPID
			select {
			case <-ctx.Done():
				close(canceled)
				<-release
			case <-release:
			}
			return nil // Deliberately late nonnil return after real cancellation.
		}
	})
	defer s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.AfterOpen = nil })
	queryDone, closeDone := make(chan struct{}), make(chan struct{})
	terminalStarted := false
	var rows driver.Rows
	var queryErr, closeErr error
	q := "SELECT dbc_ext.dblink_connect('pending'," + pq.QuoteLiteral(s.conninfo("dbc_pending", false)) + ") AS value"
	go func() {
		defer close(queryDone)
		if s.prepared {
			statement, err := physical.(driver.ConnPrepareContext).PrepareContext(s.p.Ctx, q)
			if err != nil {
				queryErr = err
				return
			}
			defer statement.Close()
			rows, queryErr = statement.(driver.StmtQueryContext).QueryContext(s.p.Ctx, nil)
		} else {
			rows, queryErr = physical.(driver.QueryerContext).QueryContext(s.p.Ctx, q, nil)
		}
	}()
	defer func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		select {
		case <-queryDone:
			if rows != nil {
				rows.Close()
			}
		case <-ctx.Done():
			t.Error("pending command did not join")
		}
		if terminalStarted {
			select {
			case <-closeDone:
			case <-ctx.Done():
				t.Error("terminal control did not join")
			}
		}
		physical.Close()
	}()
	var pid int32
	select {
	case pid = <-opened:
	case <-queryDone:
		t.Fatal("pending command failed before real Open", queryErr)
	case <-s.p.Ctx.Done():
		t.Fatal(s.p.Ctx.Err())
	}
	if action != "close" {
		for _, invalid := range []string{dump + "\nINVALID SQL;", incompatibleDump} {
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(invalid)); err == nil {
				t.Fatal("invalid/incompatible replacement accepted while Open pending")
			}
			select {
			case <-canceled:
				t.Fatal("failed replacement canceled live pending Open")
			default:
			}
		}
		ctx, cancel := context.WithCancel(s.p.Ctx)
		err := stuntgres.Restore(ctx, s.p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("failed canceled Restore", err)
		}
		select {
		case <-canceled:
			t.Fatal("canceled failed replacement canceled pending Open")
		default:
		}
	}
	terminalStarted = true
	go func() {
		defer close(closeDone)
		switch action {
		case "restore":
			closeErr = stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump))
		case "reset":
			closeErr = stuntgres.Reset(s.p.Ctx, s.p.Native)
		default:
			closeErr = physical.Close()
		}
	}()
	select {
	case <-canceled:
	case <-s.p.Ctx.Done():
		t.Fatal("terminal operation failed to cancel pending Open", s.p.Ctx.Err())
	}
	select {
	case <-closeDone:
		t.Fatal("terminal operation returned before late wire cleanup")
	default:
	}
	unblock()
	select {
	case <-queryDone:
	case <-s.p.Ctx.Done():
		t.Fatal("query join", s.p.Ctx.Err())
	}
	select {
	case <-closeDone:
	case <-s.p.Ctx.Done():
		t.Fatal("close join", s.p.Ctx.Err())
	}
	if rows != nil || queryErr == nil || closeErr != nil {
		t.Fatal("pending publication outcome", rows, queryErr, closeErr)
	}
	s.assertClosed(pid)
	s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.AfterOpen = nil })
	if action == "reset" {
		s.exec("CREATE SCHEMA dbc_ext")
		s.exec("CREATE EXTENSION dblink SCHEMA dbc_ext")
	}
	if action != "close" {
		s.list()
		app := fmt.Sprintf("dbc_after_pending_%d", connectionSequence.Add(1))
		connectionPhysicalText(s, physical, "SELECT dbc_ext.dblink_connect('next_generation',"+pq.QuoteLiteral(s.conninfo(app, false))+") AS value", "OK")
		next := s.pid(app)
		connectionPhysicalText(s, physical, "SELECT dbc_ext.dblink_disconnect('next_generation') AS value", "OK")
		s.assertClosed(next)
	}
	if err := physical.Close(); err != nil {
		t.Fatal("repeated physical Close", err)
	}
}

func connectionPhysicalText(s *connectionScenario, physical driver.Conn, q, want string) {
	s.t.Helper()
	var rows driver.Rows
	var err error
	if s.prepared {
		statement, e := physical.(driver.ConnPrepareContext).PrepareContext(s.p.Ctx, q)
		if e != nil {
			s.t.Fatal(e)
		}
		defer statement.Close()
		rows, err = statement.(driver.StmtQueryContext).QueryContext(s.p.Ctx, nil)
	} else {
		rows, err = physical.(driver.QueryerContext).QueryContext(s.p.Ctx, q, nil)
	}
	if err != nil {
		s.t.Fatal("same physical Owner next generation", err)
	}
	defer rows.Close()
	columns := rows.Columns()
	metadata, ok := rows.(driver.RowsColumnTypeDatabaseTypeName)
	if len(columns) != 1 || columns[0] != "value" || !ok || metadata.ColumnTypeDatabaseTypeName(0) != "TEXT" {
		s.t.Fatal("physical result descriptor before Next", columns)
	}
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil || values[0] != want {
		s.t.Fatal("physical next-generation result", values, err)
	}
	if err := rows.Next(values); err != io.EOF {
		s.t.Fatal("physical next-generation row count", err)
	}
}

func connectionDefinerInFlight(s *connectionScenario, revoke bool) {
	t := s.t
	s.exec("SET application_name='dbc_outer_inflight'")
	s.exec("ALTER FUNCTION dbc_ext.dblink_connect_u(text,text) SET application_name='dbc_inner_inflight'")
	s.exec("CREATE ROLE dbc_reader")
	s.exec("GRANT USAGE ON SCHEMA dbc_ext TO dbc_reader")
	s.exec("GRANT EXECUTE ON FUNCTION dbc_ext.dblink_connect_u(text,text) TO dbc_reader")
	s.exec("SET SESSION AUTHORIZATION dbc_reader")
	defer s.exec("RESET SESSION AUTHORIZATION")
	ctx, cancel := context.WithCancel(s.p.Ctx)
	defer cancel()
	opened := make(chan int32, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) {
		f.AfterOpen = func(ctx context.Context, w *fixture.ExternalSQLWire) error {
			opened <- w.Info().BackendPID
			if revoke {
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			<-ctx.Done()
			return ctx.Err()
		}
	})
	defer s.provider.ConfigureFaults(func(f *fixture.ExternalSQLFaults) { f.AfterOpen = nil })
	q := "SELECT dbc_ext.dblink_connect_u($1::text,$2::text) AS value"
	executor := fixture.Executor(s.c)
	if s.prepared {
		st, err := s.c.PrepareContext(s.p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		executor = fixture.HeldStatement{Executor: s.c, Statement: st, SQL: q}
	}
	done := make(chan struct{})
	var queryErr error
	conninfo := s.conninfo("dbc_canceled_definer", false)
	go func() {
		defer close(done)
		rows, err := executor.QueryContext(ctx, q, "canceled", conninfo)
		queryErr = err
		if rows != nil {
			rows.Close()
		}
	}()
	defer func() {
		cancel()
		unblock()
		cleanup, end := context.WithTimeout(context.Background(), fixture.Liveness)
		defer end()
		select {
		case <-done:
		case <-cleanup.Done():
			t.Error("canceled definer command did not join")
		}
	}()
	var pid int32
	select {
	case pid = <-opened:
	case <-s.p.Ctx.Done():
		t.Fatal("actual Open did not start", s.p.Ctx.Err())
	}
	if revoke {
		if _, err := s.p.Native.ExecContext(s.p.Ctx, "REVOKE EXECUTE ON FUNCTION dbc_ext.dblink_connect_u(text,text) FROM dbc_reader"); err != nil {
			t.Fatal(err)
		}
		unblock()
	} else {
		cancel()
	}
	select {
	case <-done:
	case <-s.p.Ctx.Done():
		t.Fatal("canceled definer command join", s.p.Ctx.Err())
	}
	if revoke {
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for function dblink_connect_u"}
		fixture.AssertDiagnostic(t, queryErr, &want)
	} else if !errors.Is(queryErr, context.Canceled) {
		t.Fatal("canceled genuine Open", queryErr)
	}
	s.assertClosed(pid)
	s.list()
	s.run(fixture.Select("SELECT current_user::text AS current,session_user::text AS session", []string{"current", "session"}, []string{"TEXT", "TEXT"}, [][]any{{"dbc_reader", "dbc_reader"}}))
	s.run(connectionSettingText("SELECT current_setting('application_name') AS value", "dbc_outer_inflight", s.b.PG))
}
