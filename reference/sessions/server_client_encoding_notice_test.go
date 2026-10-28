//go:build !noservertests

package reference_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceClientEncodingNoticeBackpressure(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			observer := f.RawPool(t, b)
			admin := serverAuthenticated(t, dsn)
			clientEncodingCommand(t, admin, "CREATE TABLE ce_notice_public(v int)", "CREATE TABLE")
			for _, extended := range []bool{false, true} {
				t.Run(fmt.Sprintf("extended_%t", extended), func(t *testing.T) {
					clientEncodingCommand(t, admin, "TRUNCATE ce_notice_public", "TRUNCATE TABLE")
					w := serverAuthenticated(t, dsn)
					tcp := w.Conn.(*net.TCPConn)
					if err := tcp.SetReadBuffer(4096); err != nil {
						t.Fatal(err)
					}
					clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
					// Bounded repeated notices can exhaust transport/proxy buffers;
					// only the observed ClientWrite below establishes backpressure.
					payload := strings.Repeat("\xe9", 8192)
					q := "DO $$ BEGIN INSERT INTO ce_notice_public VALUES(1); FOR i IN 1..8192 LOOP RAISE NOTICE USING MESSAGE='" + payload + "'; END LOOP; END $$"
					if extended {
						for _, packet := range [][]byte{serverParse("s", q), serverBind("p", "s", nil, nil), serverExecute("p", 0), serverPacket('H', nil)} {
							w.send(t, packet)
						}
						serverWantTags(t, []serverFrame{w.frame(t), w.frame(t)}, "12")
					} else {
						w.send(t, serverPacket('Q', serverCString(nil, q)))
					}
					// Read only the current frame header and a fixed literal prefix.
					// There is no background reader that could drain the large notice.
					var header [5]byte
					if _, err := io.ReadFull(w, header[:]); err != nil {
						t.Fatal("notice header", err)
					}
					size := int(binary.BigEndian.Uint32(header[1:])) - 4
					if size < 0 || size > (32<<20)-4 {
						t.Fatalf("invalid notice frame header %x", header)
					}
					if header[0] != 'N' {
						body := make([]byte, size)
						if _, err := io.ReadFull(w, body); err != nil {
							t.Fatal("unexpected frame body", err)
						}
						t.Fatalf("notice delivery produced %s", clientEncodingTranscript([]serverFrame{{tag: header[0], body: body}}))
					}
					if size <= len(payload) {
						t.Fatalf("notice frame was truncated: header%x payloadBytes%d", header, len(payload))
					}
					want := []byte("SNOTICE\x00VNOTICE\x00C00000\x00M" + strings.Repeat("\xe9", 32))
					prefix := make([]byte, len(want))
					if _, err := io.ReadFull(w, prefix); err != nil || !bytes.Equal(prefix, want) {
						t.Fatalf("actual notice prefix%x want%x error%v", prefix, want, err)
					}
					ctx, cancel := context.WithTimeout(f.Ctx, 15*time.Second)
					defer cancel()
					if b.PG {
						var state, waitType, wait string
						for {
							if err := observer.QueryRowContext(ctx, "SELECT state,COALESCE(wait_event_type,''),COALESCE(wait_event,'') FROM pg_stat_activity WHERE pid=$1", w.pid).Scan(&state, &waitType, &wait); err != nil {
								t.Fatalf("notice socket-write barrier: last state=%q wait=%q/%q error=%v", state, waitType, wait, err)
							}
							if waitType == "Client" && wait == "ClientWrite" {
								break
							}
							if state == "idle" || state == "idle in transaction" {
								t.Fatalf("notice workload completed before backpressure: state=%q wait=%q/%q", state, waitType, wait)
							}
							runtime.Gosched()
						}
					}
					// Native's exact admission/replacement-lock observation is the
					// companion net.Pipe mechanism test, not a size/timing inference.
					if err := w.Close(); err != nil {
						t.Fatal(err)
					}
					if b.PG {
						serverPipelineWaitGone(t, ctx, observer, w.pid)
					} else {
						serverPipelineWaitClosed(t, ctx, filepath.Join(f.Logdir, "server.log"), w.pid)
					}
					serverScalar(t, admin, "SELECT count(*) FROM ce_notice_public", "0")
					serverScalar(t, admin, "SELECT 42", "42")
					fresh := serverAuthenticated(t, dsn)
					serverScalar(t, fresh, "SELECT 42", "42")
				})
			}
		})
	}
}

// Idle controls have warnings but no live transaction incarnation to borrow.
// Sending the warning must not manufacture a transaction or lose its ordering.
func TestReferenceClientEncodingNoticeControl(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, extended := range []bool{false, true} {
				t.Run(fmt.Sprintf("extended_%t", extended), func(t *testing.T) {
					w := clientEncodingLogin(t, f.DSNFor(b), "LATIN1")
					for _, control := range []string{"COMMIT", "ROLLBACK"} {
						var frames []serverFrame
						if extended {
							for _, packet := range [][]byte{serverParse("", control), serverBind("", "", nil, nil), serverExecute("", 0), serverPacket('S', nil)} {
								w.send(t, packet)
							}
							frames = w.ready(t)
							serverWantTags(t, frames, "12NCZ")
						} else {
							frames = w.query(t, control)
							serverWantTags(t, frames, "NCZ")
						}
						clientEncodingDiagnostic(t, frames, 'N', map[byte]string{'S': "WARNING", 'V': "WARNING", 'C': "25P01", 'M': "there is no transaction in progress"})
						serverWantCode(t, frames, "")
						serverWantCompletions(t, frames, control)
						serverWantReady(t, frames, 'I')
						serverScalar(t, w, "SELECT 42", "42")
					}
				})
			}
		})
	}
}
