//go:build !noservertests

package reference_test

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceServerCopyCancellation(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, peer.query(t, "CREATE TABLE server_copy_cancel(v int); INSERT INTO server_copy_cancel VALUES (99)"), "")
			for _, version := range []uint32{196608, 196610} {
				w := serverDial(t, dsn)
				serverWantCode(t, w.login(t, dsn, version), "")
				serverCopyStart(t, w, "COPY server_copy_cancel FROM STDIN", false, 0, 1)
				w.send(t, serverPacket('d', []byte("1\n")))
				serverCancel(t, dsn, w.pid, w.key)
				// PostgreSQL can defer QueryCancel while receiving the next complete
				// frontend frame. End input after the acknowledged cancel so the
				// backend reaches its interrupt check, never wait on an idle sender.
				w.send(t, serverPacket('c', nil))
				frames := w.ready(t)
				serverWantCode(t, frames, "57014")
				serverWantCompletions(t, frames)
				serverScalar(t, w, "SELECT 42", "42")
				serverScalar(t, peer, "SELECT count(*) FROM server_copy_cancel", "1")
			}
		})
	}
}
func TestReferenceServerCopyBackpressure(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			owner := serverAuthenticated(t, dsn)
			serverWantCode(t, owner.query(t, "CREATE TABLE server_copy_output(v int); INSERT INTO server_copy_output VALUES (0)"), "")
			for n := 1; n < 4096; n *= 2 {
				serverWantCode(t, owner.query(t, fmt.Sprintf("INSERT INTO server_copy_output SELECT v+%d FROM server_copy_output", n)), "")
			}
			w := serverDial(t, dsn)
			tcp := w.Conn.(*net.TCPConn)
			if err := tcp.SetReadBuffer(64 << 10); err != nil {
				t.Fatal(err)
			}
			serverWantCode(t, w.login(t, dsn, 196610), "")
			serverWantCode(t, w.query(t, "BEGIN"), "")
			w.send(t, serverPacket('Q', serverCString(nil, "COPY (SELECT '"+strings.Repeat("x", 8192)+"'::text FROM server_copy_output) TO STDOUT")))
			if fr := w.frame(t); fr.tag != 'H' {
				t.Fatalf("CopyOut %c", fr.tag)
			}
			if fr := w.frame(t); fr.tag != 'd' || len(fr.body) != 8193 {
				t.Fatalf("first complete CopyData %c length=%d", fr.tag, len(fr.body))
			}
			if b.PG {
				ctx, cancel := context.WithTimeout(f.Ctx, 10*time.Second)
				defer cancel()
				observer := f.RawPool(t, b)
				for {
					var blocked bool
					if err := observer.QueryRowContext(ctx, "SELECT COALESCE(wait_event='ClientWrite',false) FROM pg_stat_activity WHERE pid=$1", w.pid).Scan(&blocked); err != nil {
						t.Fatal(err)
					}
					if blocked {
						break
					}
					runtime.Gosched()
				}
			}
			serverCancel(t, dsn, w.pid, w.key)
			if err := tcp.SetReadBuffer(1 << 20); err != nil {
				t.Fatal(err)
			}
			rows := 1
			code := ""
			complete := false
			for {
				fr := w.frame(t)
				switch fr.tag {
				case 'd':
					if code != "" {
						t.Fatal("data after error")
					}
					rows++
				case 'c', 'C':
					complete = true
				case 'E':
					code = serverFields(fr.body)['C']
				case 'Z':
					if code != "57014" || complete || rows >= 4096 || string(fr.body) != "E" {
						t.Fatalf("cancel rows=%d code=%s complete=%v ready=%s", rows, code, complete, fr.body)
					}
					serverWantCode(t, w.query(t, "ROLLBACK"), "")
					serverScalar(t, w, "SELECT 42", "42")
					return
				case 'N', 'S':
				default:
					t.Fatalf("unexpected stream frame %c", fr.tag)
				}
			}
		})
	}
}
func TestReferenceServerPhase2Lifetime(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			owner := serverAuthenticated(t, dsn)
			serverWantCode(t, owner.query(t, "CREATE TABLE server_phase2_lifetime(v int); INSERT INTO server_phase2_lifetime VALUES (99)"), "")
			for _, mode := range []string{"portal", "copy"} {
				w := serverAuthenticated(t, dsn)
				if mode == "portal" {
					serverWantCode(t, w.query(t, "BEGIN"), "")
					serverWantCode(t, serverCycle(t, w, serverParse("s", "SELECT v FROM server_phase2_lifetime"), serverBind("p", "s", nil, nil), serverExecute("p", 1)), "")
				} else {
					serverCopyStart(t, w, "COPY server_phase2_lifetime FROM STDIN", false, 0, 1)
					w.send(t, serverPacket('d', []byte("1\n")))
				}
				w.Close()
				serverWantCode(t, owner.query(t, "BEGIN; LOCK TABLE server_phase2_lifetime IN ACCESS EXCLUSIVE MODE; ROLLBACK"), "")
				serverScalar(t, owner, "SELECT count(*) FROM server_phase2_lifetime", "1")
			}
		})
	}
	// Actual child shutdown with active COPY must join; reference process belongs to the common oracle.
	if b, ok := f.NativeBackend(); ok {
		w := serverAuthenticated(t, f.DSNFor(b))
		serverWantCode(t, w.query(t, "CREATE TABLE server_shutdown_copy(v int)"), "")
		serverCopyStart(t, w, "COPY server_shutdown_copy FROM STDIN", false, 0, 1)
		w.send(t, serverPacket('d', []byte("1\n")))
		f.Stop(t)
	}
}
