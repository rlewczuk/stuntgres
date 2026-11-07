package reference_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"stuntgres/reference/fixture"
)

func embeddedValues(t *testing.T, ctx context.Context, c *pgx.Conn, q string, want []int) {
	t.Helper()
	rows, err := c.Query(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if rows.Err() != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: %v %v want %v", q, got, rows.Err(), want)
	}
}

func TestReferenceEmbeddedServerShutdown(t *testing.T) {
	t.Run("final_dump_restore", func(t *testing.T) {
		cfg := embeddedConfig()
		cfg.DumpDir = t.TempDir()
		cfg.LogDir = t.TempDir()
		f := fixture.NewEmbeddedServerFixture(t, cfg, fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase)
		var native *pgx.Conn
		for _, b := range f.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				c := embeddedConnect(t, f.Ctx, b, true)
				embeddedExec(t, f.Ctx, c, "CREATE TABLE embedded_persist(v integer); INSERT INTO embedded_persist VALUES (11),(22)")
				embeddedValues(t, f.Ctx, c, "SELECT v FROM embedded_persist ORDER BY v", []int{11, 22})
			})
			if b.DialFunc != nil {
				native = embeddedConnect(t, f.Ctx, b, true)
			}
		}
		for i := 1; i <= 2; i++ {
			if err := f.Server.Dump(f.Ctx); err != nil {
				t.Fatal(err)
			}
			if files := serverDumpFiles(t, cfg.DumpDir, fixture.ServerDatabase); len(files) != i {
				t.Fatalf("explicit dump %d: %v", i, files)
			}
		}
		embeddedExec(t, f.Ctx, native, "BEGIN; INSERT INTO embedded_persist VALUES (99)")
		addr := f.Server.Addr().String()
		embeddedShutdown(t, f.Server)
		files := serverDumpFiles(t, cfg.DumpDir, fixture.ServerDatabase)
		if len(files) != 3 {
			t.Fatalf("final dumps: %v", files)
		}
		stopped := false
		for _, r := range serverLogRecords(t, filepath.Join(cfg.LogDir, "server.log")) {
			if r["message"] == "server stopped" {
				stopped = true
			}
		}
		if !stopped {
			t.Fatal("Shutdown returned before stopped record")
		}
		if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
			c.Close()
			t.Fatal("listener survived Shutdown")
		}
		if c, err := f.Server.DialFunc()(f.Ctx, "arbitrary", "address"); err == nil {
			c.Close()
			t.Fatal("pipe admission survived Shutdown")
		}
		cfg.Restore = true
		cfg.LogDir = ""
		restored := embeddedStart(t, f.Ctx, cfg)
		c := embeddedConnect(t, f.Ctx, embeddedEndpoint(t, restored, fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase), true)
		embeddedValues(t, f.Ctx, c, "SELECT v FROM embedded_persist ORDER BY v", []int{11, 22})
		if *fixture.Mode {
			data, err := os.ReadFile(files[len(files)-1])
			if err != nil {
				t.Fatal(err)
			}
			oracle := embeddedConnect(t, f.Ctx, fixture.EmbeddedServerBackend{DSN: fixture.DSN}, false)
			embeddedExec(t, f.Ctx, oracle, "DROP TABLE embedded_persist")
			// Replay exporter bytes including its ownership directives unchanged.
			embeddedExec(t, f.Ctx, oracle, string(data))
			embeddedValues(t, f.Ctx, oracle, "SELECT v FROM embedded_persist ORDER BY v", []int{11, 22})
		}
	})
	t.Run("cancel_all_before_rollback", func(t *testing.T) {
		cfg := embeddedConfig()
		cfg.DumpDir = t.TempDir()
		f := fixture.NewEmbeddedServerFixture(t, cfg, fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase)
		for _, b := range f.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				owner := embeddedConnect(t, f.Ctx, b, true)
				waiter := embeddedConnect(t, f.Ctx, b, true)
				observer := embeddedConnect(t, f.Ctx, b, true)
				embeddedExec(t, f.Ctx, owner, "CREATE TABLE embedded_shutdown_lock(v integer); INSERT INTO embedded_shutdown_lock VALUES (1)")
				embeddedExec(t, f.Ctx, owner, "BEGIN; LOCK TABLE embedded_shutdown_lock IN ACCESS EXCLUSIVE MODE; INSERT INTO embedded_shutdown_lock VALUES (88)")
				worker, stop := context.WithCancel(f.Ctx)
				result := make(chan error, 1)
				joined := make(chan struct{})
				go func() {
					_, err := waiter.Exec(worker, "INSERT INTO embedded_shutdown_lock VALUES (99)")
					result <- err
					close(joined)
				}()
				t.Cleanup(func() {
					stop()
					select {
					case <-joined:
					case <-time.After(fixture.Liveness):
						t.Error("waiter did not join")
					}
				})
				observe, end := context.WithTimeout(f.Ctx, fixture.Liveness)
				defer end()
				for {
					var waiting bool
					err := observer.QueryRow(observe, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype='relation' AND relation='embedded_shutdown_lock'::regclass::oid AND mode='RowExclusiveLock' AND NOT granted)`).Scan(&waiting)
					if err != nil {
						t.Fatal(err)
					}
					if waiting {
						break
					}
					runtime.Gosched()
				}
				if b.DialFunc != nil {
					embeddedShutdown(t, f.Server)
					if err := embeddedJoin(t, f.Ctx, result); err == nil {
						t.Fatal("shutdown admitted waiter committed")
					}
					cfg.Restore = true
					restored := embeddedStart(t, f.Ctx, cfg)
					c := embeddedConnect(t, f.Ctx, embeddedEndpoint(t, restored, fixture.ServerUser, fixture.ServerPassword, fixture.ServerDatabase), true)
					embeddedValues(t, f.Ctx, c, "SELECT v FROM embedded_shutdown_lock ORDER BY v", []int{1})
				} else {
					// PostgreSQL has no embedded Shutdown API. Its shared SQL phase proves
					// the same admitted cancellation/rollback committed-state expectation.
					if err := waiter.PgConn().CancelRequest(f.Ctx); err != nil {
						t.Fatal(err)
					}
					embeddedDiagnostic(t, embeddedJoin(t, f.Ctx, result), "57014", "canceling statement due to user request")
					embeddedExec(t, f.Ctx, owner, "ROLLBACK")
					embeddedValues(t, f.Ctx, observer, "SELECT v FROM embedded_shutdown_lock ORDER BY v", []int{1})
				}
			})
		}
	})
	t.Run("deadline_does_not_abandon_cleanup", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.ScenarioBudget)
		defer cancel()
		gate := &embeddedStopGate{entered: make(chan struct{}), release: make(chan struct{})}
		cfg := embeddedConfig()
		cfg.LogWriter = gate
		s := embeddedStart(t, ctx, cfg)
		result := make(chan error, 1)
		go func() { result <- s.Shutdown(ctx) }()
		var once sync.Once
		release := func() { once.Do(func() { close(gate.release) }) }
		t.Cleanup(release)
		select {
		case <-gate.entered:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		expired, stop := context.WithDeadline(ctx, time.Now().Add(-time.Second))
		defer stop()
		if err := s.Shutdown(expired); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline waiter: %v", err)
		}
		select {
		case <-s.Done():
			t.Fatal("cleanup completed while owned writer blocked")
		default:
		}
		release()
		if err := embeddedJoin(t, ctx, result); err != nil {
			t.Fatal(err)
		}
		embeddedShutdown(t, s)
	})
}

type embeddedStopGate struct {
	entered, release chan struct{}
	once             sync.Once
}

func (g *embeddedStopGate) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"message":"server stopped"`)) {
		g.once.Do(func() { close(g.entered) })
		<-g.release
	}
	return len(p), nil
}
