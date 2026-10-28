//go:build !noservertests

package reference_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceClientEncodingCancellation(t *testing.T) {
	t.Run("wire", func(t *testing.T) {
		f := fixture.NewServerFixture(t)
		for _, b := range f.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				dsn := f.DSNFor(b)
				admin := serverAuthenticated(t, dsn)
				clientEncodingCommand(t, admin, "CREATE TABLE ce_cancel_rows(v int); INSERT INTO ce_cancel_rows VALUES(0)", "CREATE TABLE", "INSERT 0 1")
				for n := 1; n < 4096; n *= 2 {
					clientEncodingCommand(t, admin, fmt.Sprintf("INSERT INTO ce_cancel_rows SELECT v+%d FROM ce_cancel_rows", n), fmt.Sprintf("INSERT 0 %d", n))
				}
				for _, copyOut := range []bool{false, true} {
					t.Run(fmt.Sprintf("output_copy_%t", copyOut), func(t *testing.T) {
						w := serverDial(t, dsn)
						tcp := w.Conn.(*net.TCPConn)
						if err := tcp.SetReadBuffer(64 << 10); err != nil {
							t.Fatal(err)
						}
						serverWantCode(t, w.login(t, dsn, 196608), "")
						clientEncodingCommand(t, w, "SET client_encoding='LATIN1'; BEGIN", "SET", "BEGIN")
						payload := strings.Repeat("\xe9", 8192)
						q := "SELECT '" + payload + "'::text AS v FROM ce_cancel_rows"
						if copyOut {
							q = "COPY (" + q + ") TO STDOUT"
						}
						w.send(t, serverPacket('Q', serverCString(nil, q)))
						first := w.frame(t)
						wantTag := byte('T')
						if copyOut {
							wantTag = 'H'
						}
						if first.tag != wantTag {
							t.Fatalf("output admission %c %x", first.tag, first.body)
						}
						first = w.frame(t)
						wantTag = 'D'
						if copyOut {
							wantTag = 'd'
						}
						if first.tag != wantTag {
							t.Fatalf("first converted output %c %x", first.tag, first.body)
						}
						accepted := bytes.Clone(first.body)
						if copyOut {
							if !bytes.Equal(accepted, []byte(payload+"\n")) {
								t.Fatalf("first COPY row %x", accepted)
							}
						} else {
							serverWantRaw(t, []serverFrame{first}, [][][]byte{{[]byte(payload)}})
						}
						if b.PG {
							observer := f.RawPool(t, b)
							for {
								var blocked bool
								if err := observer.QueryRowContext(f.Ctx, "SELECT COALESCE(wait_event_type='Client' AND wait_event='ClientWrite',false) FROM pg_stat_activity WHERE pid=$1", w.pid).Scan(&blocked); err != nil {
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
						var diagnostic, statusFrames []serverFrame
						for {
							fr := w.frame(t)
							if fr.tag == 'Z' {
								if string(fr.body) != "E" {
									t.Fatalf("canceled ready %x", fr.body)
								}
								break
							}
							switch fr.tag {
							case 'D', 'd':
								if len(diagnostic) > 0 {
									t.Fatal("datum after error")
								}
								if copyOut {
									if fr.tag != 'd' || !bytes.Equal(fr.body, []byte(payload+"\n")) {
										t.Fatalf("partial converted COPY row %c %x", fr.tag, fr.body)
									}
								} else {
									if fr.tag != 'D' {
										t.Fatalf("unexpected row tag %c", fr.tag)
									}
									serverWantRaw(t, []serverFrame{fr}, [][][]byte{{[]byte(payload)}})
								}
								rows++
							case 'E':
								diagnostic = append(diagnostic, fr)
							case 'S':
								if len(diagnostic) != 1 {
									t.Fatalf("status before cancellation diagnostic: %s", clientEncodingTranscript([]serverFrame{fr}))
								}
								statusFrames = append(statusFrames, fr)
							case 'C', 'c':
								t.Fatalf("cancellation published completion %s", clientEncodingTranscript([]serverFrame{fr}))
							default:
								t.Fatalf("unexpected cancellation frame %c %x", fr.tag, fr.body)
							}
						}
						clientEncodingError(t, diagnostic, "57014", "canceling statement due to user request", "")
						clientEncodingStatus(t, statusFrames, "client_encoding", "UTF8")
						if len(statusFrames) != 1 {
							t.Fatalf("cancellation statuses %s", clientEncodingTranscript(statusFrames))
						}
						if rows >= 4096 {
							t.Fatalf("whole output escaped cancellation %d", rows)
						}
						clientEncodingCommand(t, w, "ROLLBACK; SET client_encoding='UTF8'", "ROLLBACK", "SET")
						serverScalar(t, w, "SELECT 42", "42")
						if !bytes.Equal(first.body, accepted) {
							t.Fatal("accepted frame backing changed across reuse")
						}
					})
				}
				t.Run("copy_input", func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					clientEncodingCommand(t, w, "CREATE TABLE ce_cancel_input(v text)", "CREATE TABLE")
					clientEncodingCommand(t, w, "SET client_encoding='LATIN1'; BEGIN", "SET", "BEGIN")
					serverCopyStart(t, w, "COPY ce_cancel_input FROM STDIN", true, 0, 1)
					// G and an unfinished first record hold the real input owner.
					serverObserveCopyInput(t, f, b, w)
					w.Conn = &serverCopyInputConn{Conn: w.Conn, ctx: f.Ctx}
					serverCancel(t, dsn, w.pid, w.key)
					w.send(t, serverPacket('d', []byte("\xe9\n")))
					frames := serverThrough(t, w, 'E')
					clientEncodingError(t, frames, "57014", "canceling statement due to user request", "COPY ce_cancel_input, line 1: \"\xe9\"")
					w.send(t, serverPacket('c', nil))
					w.send(t, serverPacket('S', nil))
					readyFrames := w.ready(t)
					serverWantReady(t, readyFrames, 'E')
					serverWantTags(t, readyFrames, "SZ")
					clientEncodingStatus(t, readyFrames, "client_encoding", "UTF8")
					clientEncodingCommand(t, w, "ROLLBACK", "ROLLBACK")
					serverScalar(t, w, "SELECT count(*) FROM ce_cancel_input", "0")
					serverScalar(t, w, "SELECT 42", "42")
				})
			})
		}
	})
	t.Run("stream_callbacks", func(t *testing.T) { clientEncodingCancelStreams(t) })
}

type clientEncodingReadBarrier struct {
	ctx       context.Context
	entered   chan struct{}
	prefix    []byte
	announced bool
}

func (r *clientEncodingReadBarrier) Read(p []byte) (int, error) {
	if len(r.prefix) > 0 {
		n := copy(p, r.prefix)
		r.prefix = r.prefix[n:]
		return n, nil
	}
	if !r.announced {
		r.announced = true
		close(r.entered)
	}
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

type clientEncodingWriteBarrier struct {
	ctx     context.Context
	entered chan []byte
	first   bool
}

func (w *clientEncodingWriteBarrier) Write(p []byte) (int, error) {
	if !w.first {
		w.first = true
		w.entered <- bytes.Clone(p)
	}
	<-w.ctx.Done()
	return 0, w.ctx.Err()
}

func clientEncodingCancelStreams(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			pool := p.Native
			if b.PG {
				pool = p.CopyPostgres
			}
			c, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{"CREATE TABLE ce_cancel_stream(v text)", "SET client_encoding='LATIN1'"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			var pid int
			if b.PG {
				if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			reader := &clientEncodingReadBarrier{ctx: ctx, entered: make(chan struct{}), prefix: []byte(strings.Repeat("\xe9", 65536) + "\n")}
			type outcome struct {
				result stuntgres.CopyResult
				err    error
			}
			done := make(chan outcome, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("canceled COPY FROM worker did not join")
					}
				}
			}()
			go func() {
				r, err := fixture.CopyFrom(ctx, c, b.PG, "COPY ce_cancel_stream FROM STDIN", reader)
				done <- outcome{r, err}
			}()
			select {
			case <-reader.entered:
			case result := <-done:
				joined = true
				t.Fatalf("COPY ended before next admitted read: %+v", result)
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
			if b.PG {
				for {
					var reading bool
					if err := p.Postgres.QueryRowContext(p.Ctx, "SELECT COALESCE(state='active' AND wait_event_type='Client' AND wait_event='ClientRead',false) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&reading); err != nil {
						t.Fatal("stream COPY admission", err)
					}
					if reading {
						break
					}
					runtime.Gosched()
				}
			}
			cancel()
			var result outcome
			select {
			case result = <-done:
				joined = true
			case <-p.Ctx.Done():
				t.Fatal("COPY FROM join", p.Ctx.Err())
			}
			if result.result != (stuntgres.CopyResult{}) {
				t.Fatalf("canceled input success %+v", result)
			}
			if !b.PG && !errors.Is(result.err, context.Canceled) {
				t.Fatalf("native cancellation identity %v", result.err)
			}
			if result.err == nil {
				t.Fatal("canceled stream succeeded")
			}
			// A canceled PostgreSQL stream may retire its transport. Borrow a fresh
			// owner only there; native must remain reusable on this exact Conn.
			if b.PG {
				c.Close()
				c, err = pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
			}
			var count int
			if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM ce_cancel_stream").Scan(&count); err != nil || count != 0 {
				t.Fatalf("input prefix %d %v", count, err)
			}
			if _, err := c.ExecContext(p.Ctx, "SET client_encoding='LATIN1'"); err != nil {
				t.Fatal(err)
			}
			if _, err := c.ExecContext(p.Ctx, `INSERT INTO ce_cancel_stream VALUES(U&'\00E9')`); err != nil {
				t.Fatal(err)
			}
			// Native API callback ownership is an explicit native-only control phase.
			// PG COPY output cancellation is exercised above with raw CancelRequest.
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				writer := &clientEncodingWriteBarrier{ctx: ctx, entered: make(chan []byte, 1)}
				outputJoined := false
				defer func() {
					cancel()
					if !outputJoined {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("canceled COPY TO worker did not join")
						}
					}
				}()
				go func() {
					r, err := stuntgres.CopyTo(ctx, c, "COPY ce_cancel_stream TO STDOUT", writer)
					done <- outcome{r, err}
				}()
				var accepted []byte
				select {
				case accepted = <-writer.entered:
				case <-p.Ctx.Done():
					t.Fatal(p.Ctx.Err())
				}
				if !bytes.Equal(accepted, []byte{0xe9, '\n'}) {
					t.Fatalf("writer bytes %x", accepted)
				}
				cancel()
				select {
				case result = <-done:
					outputJoined = true
				case <-p.Ctx.Done():
					t.Fatal("COPY TO join", p.Ctx.Err())
				}
				if result.result != (stuntgres.CopyResult{}) || !errors.Is(result.err, context.Canceled) {
					t.Fatalf("canceled output %+v", result)
				}
				if err := c.QueryRowContext(p.Ctx, "SELECT 42").Scan(&count); err != nil || count != 42 {
					t.Fatalf("same owner %d %v", count, err)
				}
				if !bytes.Equal(accepted, []byte{0xe9, '\n'}) {
					t.Fatal("retained callback bytes changed")
				}
			}
		})
	}
}

// Compile-time interfaces express the actual callback contract, not a mock stream.
var _ io.Reader = (*clientEncodingReadBarrier)(nil)
var _ io.Writer = (*clientEncodingWriteBarrier)(nil)
