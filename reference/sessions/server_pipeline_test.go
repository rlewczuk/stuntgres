package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// These are physical-session lifetime witnesses, not just client-side EOF
// checks. The holder stays locked until the victim's cleanup is observed.
// PostgreSQL's single frontend processor has different framing order from the
// native concurrent reader; its independently specified branch below does not
// relax the shared rollback and resource-release outcomes.
func TestReferenceServerPipelineLifetime(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			owner := serverAuthenticated(t, dsn)
			serverWantCode(t, owner.query(t, "CREATE TABLE server_pipeline_lock(v integer); INSERT INTO server_pipeline_lock VALUES (1); CREATE TABLE server_pipeline_writes(v integer); INSERT INTO server_pipeline_writes VALUES (1)"), "")
			for _, scenario := range []string{"pipelined_disconnect", "short_header", "oversized_header"} {
				t.Run(scenario, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(f.Ctx, 10*time.Second)
					defer cancel()
					victim := serverDial(t, dsn)
					if b.PG && scenario == "pipelined_disconnect" {
						// PG otherwise need not detect a lost client during a lock
						// wait. This is a real server GUC, not a test-side cancel.
						serverWantCode(t, victim.login(t, dsn, 196608, "options", "-c client_connection_check_interval=10ms"), "")
					} else {
						serverWantCode(t, victim.login(t, dsn, 196608), "")
					}
					serverWantCode(t, victim.query(t, "BEGIN; CREATE TEMP TABLE server_pipeline_temp(v integer); INSERT INTO server_pipeline_writes VALUES (9)"), "")
					serverWantCode(t, owner.query(t, "BEGIN; LOCK TABLE server_pipeline_lock IN ACCESS EXCLUSIVE MODE"), "")
					// On assertion failure release the real holder as well, so
					// fixture shutdown never depends on the bug being fixed.
					defer func() { serverWantCode(t, owner.query(t, "ROLLBACK"), "") }()
					victim.send(t, serverPacket('Q', []byte("INSERT INTO server_pipeline_lock VALUES (99)\x00")))
					var observer *sql.DB
					if b.PG {
						observer = f.RawPool(t, b)
						serverObserveCancelWait(t, ctx, observer, int(victim.pid), owner.pid, "server_pipeline_lock")
					}
					if scenario == "pipelined_disconnect" {
						// The second full message consumes the one queued message
						// slot while the first command cannot acquire the holder's
						// lock. EOF must remain observable under that backpressure.
						victim.send(t, serverPacket('Q', []byte("INSERT INTO server_pipeline_writes VALUES (100)\x00")))
						victim.Close()
					} else {
						length := uint32(3)
						if scenario == "oversized_header" {
							length = (16 << 20) + 1
							if b.PG {
								// PG's genuine allocation ceiling is larger than
								// native's 16MiB transport bound. Reject at its
								// own ceiling without allocating/sending a body.
								length = 0x7fffffff
							}
						}
						victim.send(t, append([]byte{'Q'}, serverPut32(nil, length)...))
						if b.PG {
							// PG cannot inspect the queued header while waiting.
							// Cancel only this observed command, keeping the holder
							// locked. Its ordinary canceled-command boundary comes
							// before rejection of the invalid next message length.
							serverCancel(t, dsn, victim.pid, victim.key)
							frames := victim.ready(t)
							serverWantCode(t, frames, "57014")
							if serverTags(frames) != "EZ" || string(frames[len(frames)-1].body) != "E" {
								t.Fatal(serverFrameSummary(frames))
							}
							// Existing pinned short-length contracts establish that
							// PG reports invalid lengths to its server log and closes
							// without a tagged FATAL. Native promises a wire FATAL.
						} else {
							fatal := victim.frame(t)
							want := "08P01"
							if scenario == "oversized_header" {
								want = "54000"
							}
							serverWantCode(t, []serverFrame{fatal}, want)
							if fatal.tag != 'E' || serverFields(fatal.body)['S'] != "FATAL" {
								t.Fatalf("first response must be FATAL, not ordinary cancellation/ReadyForQuery: %v", fatal)
							}
						}
						if frame, err := victim.readFrame(); err != io.EOF {
							t.Fatalf("fatal framing must close without another response/ReadyForQuery: %v, %v", frame, err)
						}
					}
					// Native has no public exact relation-queue observer. Its
					// connection_id is the authenticated BackendKeyData ID; the
					// operational close event is emitted by c.finish only after
					// RequestEnd/WaitEnd, including TEMP/transaction cleanup.
					// PG supplies the exact pg_locks wait above and actual backend
					// disappearance here. Neither branch unlocks to force cleanup.
					if b.PG {
						serverPipelineWaitGone(t, ctx, observer, victim.pid)
					} else {
						serverPipelineWaitClosed(t, ctx, filepath.Join(f.Logdir, "server.log"), victim.pid)
					}
					serverWantCode(t, owner.query(t, "COMMIT"), "")
					peer := serverAuthenticated(t, dsn)
					serverWantCode(t, peer.query(t, "BEGIN; LOCK TABLE server_pipeline_lock IN ACCESS EXCLUSIVE MODE; COMMIT"), "")
					serverScalar(t, peer, "SELECT count(*) FROM server_pipeline_lock", "1")
					serverScalar(t, peer, "SELECT v FROM server_pipeline_lock", "1")
					serverScalar(t, peer, "SELECT count(*) FROM server_pipeline_writes", "1")
					serverScalar(t, peer, "SELECT v FROM server_pipeline_writes", "1")
					serverScalar(t, peer, "SELECT 42", "42")
				})
			}
		})
	}
}

func serverPipelineWaitGone(t *testing.T, ctx context.Context, observer *sql.DB, pid uint32) {
	t.Helper()
	for {
		var alive bool
		if err := observer.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1)", pid).Scan(&alive); err != nil {
			t.Fatal("physical PostgreSQL session must disappear before holder unlock", err)
		}
		if !alive {
			return
		}
		runtime.Gosched()
	}
}

func serverPipelineWaitClosed(t *testing.T, ctx context.Context, path string, id uint32) {
	t.Helper()
	for {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// A live JSONL writer can leave a partial final record in this snapshot.
		// Inspect only complete records, never substitute an arbitrary peer's
		// connection-close event for this authenticated physical session.
		for _, line := range bytes.Split(data, []byte{'\n'})[:bytes.Count(data, []byte{'\n'})] {
			var record map[string]any
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatal("invalid operational JSONL", err)
			}
			if record["message"] == "connection closed" && record["connection_id"] == float64(id) {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("physical native session %d did not finish cleanup before holder unlock: %v", id, ctx.Err())
		default:
			runtime.Gosched()
		}
	}
}
