package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"stuntgres"
	"stuntgres/reference/fixture"
)

type copyBinaryBlockedReader struct {
	prefix, rest copyTextChunks
	barrier      *copyTextBarrier
	released     bool
}

func (r *copyBinaryBlockedReader) Read(p []byte) (int, error) {
	if r.prefix.offset < len(r.prefix.text) {
		return r.prefix.Read(p)
	}
	if !r.released {
		r.barrier.wait()
		r.released = true
	}
	return r.rest.Read(p)
}

type copyBinaryErrorReader struct {
	prefix     copyTextChunks
	err        error
	panicValue any
	closed     bool
}

func (r *copyBinaryErrorReader) Read(p []byte) (int, error) {
	if r.prefix.offset < len(r.prefix.text) {
		return r.prefix.Read(p)
	}
	if r.panicValue != nil {
		panic(r.panicValue)
	}
	return 0, r.err
}
func (r *copyBinaryErrorReader) Close() error { r.closed = true; return nil }

type copyBinaryWriterCloser struct {
	io.Writer
	closed bool
}

func (w *copyBinaryWriterCloser) Close() error { w.closed = true; return nil }
func copyBinaryJoin(t *testing.T, done <-chan copyTextOutcome) copyTextOutcome {
	t.Helper()
	select {
	case out := <-done:
		return out
	case <-time.After(5 * time.Second):
		t.Error("binary worker did not join within5s after release")
		return <-done
	}
}

func TestReferenceCopyBinaryLifecycle(t *testing.T) {
	for _, phase := range []string{"payload_cancel", "trailer_cancel", "payload_timeout", "trailer_timeout"} {
		t.Run(phase, func(t *testing.T) {
			copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
				// SQL server observation/cancellation functions are a PG-owned
				// surface. Native binary interruption uses real Operation units.
				if !b.PG {
					_, err := c.conn.ExecContext(c.ctx, "SELECT pg_backend_pid()")
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "functions are unsupported", Position: "8"})
					return
				}
				c.exec(t, "CREATE TABLE copy_binary_lifecycle(v int4)")
				c.exec(t, "INSERT INTO copy_binary_lifecycle VALUES(99)")
				db := p.Native
				if b.PG {
					db = p.CopyPostgres
				}
				peer, err := db.Conn(c.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				observer := copyTextConnection{conn: peer, ctx: c.ctx, pg: b.PG}
				var pid int
				if err := c.conn.QueryRowContext(c.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
					t.Fatal(err)
				}
				timeout := phase == "payload_timeout" || phase == "trailer_timeout"
				if timeout {
					c.exec(t, "SET statement_timeout TO '100ms'")
					c.selectRows(t, "SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{"100ms"}})
				}
				stream := copyBinaryStream(copyBinaryRow(copyBinary32(42)), copyBinaryRow(copyBinary32(43)))
				split := len(stream)
				if phase == "payload_cancel" || phase == "payload_timeout" {
					split -= 5
				}
				barrier := newCopyTextBarrier()
				reader := &copyBinaryBlockedReader{prefix: copyTextChunks{text: string(stream[:split]), widths: []int{1, 7, 3}}, rest: copyTextChunks{text: string(stream[split:]), widths: []int{1}}, barrier: barrier}
				streamCtx, stopStream := context.WithTimeout(p.Ctx, 10*time.Second)
				defer stopStream()
				owner := c
				owner.ctx = streamCtx
				query := "COPY copy_binary_lifecycle FROM STDIN WITH(FORMAT binary)"
				done := make(chan copyTextOutcome, 1)
				joined := false
				defer func() {
					barrier.unblock()
					if !joined {
						copyBinaryJoin(t, done)
					}
				}()
				go func() { result, err := owner.from(query, reader); done <- copyTextOutcome{result, err} }()
				select {
				case <-barrier.entered:
				case <-done:
					joined = true
					t.Fatal("COPY exited before valid binary admission barrier")
				case <-p.Ctx.Done():
					t.Fatal("binary admission barrier deadline")
				}
				observation, stopObservation := context.WithTimeout(p.Ctx, 5*time.Second)
				defer stopObservation()
				if timeout {
					// Observe this command's actual server query_start deadline, not a sleep.
					for {
						var due bool
						if err := observer.conn.QueryRowContext(observation, "SELECT COALESCE((SELECT query=$2 AND clock_timestamp() >= query_start + INTERVAL '100 milliseconds' FROM pg_stat_activity WHERE pid=$1),FALSE)", pid, query).Scan(&due); err != nil {
							t.Fatal("binary statement deadline", err)
						}
						if due {
							break
						}
					}
				} else {
					for {
						var active bool
						if err := observer.conn.QueryRowContext(observation, "SELECT COALESCE((SELECT state='active' AND query=$2 AND wait_event='ClientRead' FROM pg_stat_activity WHERE pid=$1),FALSE)", pid, query).Scan(&active); err != nil {
							t.Fatal("binary server ClientRead admission", err)
						}
						if active {
							break
						}
					}
					var acknowledged bool
					if err := observer.conn.QueryRowContext(observation, "SELECT pg_cancel_backend($1)", pid).Scan(&acknowledged); err != nil || !acknowledged {
						t.Fatal("binary cancel acknowledgement", acknowledged, err)
					}
				}
				// Complete field/trailer framing on release, so malformed input cannot
				// accidentally outrank the genuinely observed server interrupt.
				barrier.unblock()
				out := copyBinaryJoin(t, done)
				joined = true
				copyBinaryAdditionalOutcome(t, out.result, out.err, nil)
				if out.err == nil {
					t.Error("observed binary interrupt returned success")
				}
				observer.selectRows(t, "SELECT * FROM copy_binary_lifecycle", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
				if timeout {
					c.exec(t, "SET statement_timeout TO 0")
				}
			})
		})
	}
	t.Run("native_caller_ownership", func(t *testing.T) {
		copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
			if b.PG {
				return
			} // Ordinary native identities/replacement are native-owned.
			c.exec(t, "CREATE TABLE copy_binary_native_io(v int4)")
			c.exec(t, "INSERT INTO copy_binary_native_io VALUES(99)")
			query := "COPY copy_binary_native_io FROM STDIN WITH(FORMAT binary)"
			prefix := copyBinaryHeader(0, 0, nil)
			prefix = append(prefix, copyBinaryRow(copyBinary32(42))...)
			sentinel := errors.New("binary caller read failure")
			reader := &copyBinaryErrorReader{prefix: copyTextChunks{text: string(prefix), widths: []int{1, 7}}, err: sentinel}
			result, err := c.from(query, reader)
			if !errors.Is(err, sentinel) || result != (copyTextResult{}) || reader.closed {
				t.Fatal("binary reader identity/result/ownership", result, err, reader.closed)
			}
			c.selectRows(t, "SELECT * FROM copy_binary_native_io", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
			canceled, cancel := context.WithCancel(c.ctx)
			cancel()
			before := c
			before.ctx = canceled
			never := &copyTextNoRead{}
			result, err = before.from(query, never)
			if !errors.Is(err, context.Canceled) || result != (copyTextResult{}) || never.calls != 0 {
				t.Fatal("binary precancel ownership", result, err, never.calls)
			}
			for _, split := range []int{len(copyBinaryStream(copyBinaryRow(copyBinary32(42)))) - 5, len(copyBinaryStream(copyBinaryRow(copyBinary32(42))))} {
				stream := copyBinaryStream(copyBinaryRow(copyBinary32(42)))
				barrier := newCopyTextBarrier()
				reader := &copyBinaryBlockedReader{prefix: copyTextChunks{text: string(stream[:split]), widths: []int{1}}, rest: copyTextChunks{text: string(stream[split:]), widths: []int{1}}, barrier: barrier}
				ctx, stop := context.WithCancel(c.ctx)
				active := c
				active.ctx = ctx
				done := make(chan copyTextOutcome, 1)
				joined := false
				func() {
					defer func() {
						stop()
						barrier.unblock()
						if !joined {
							copyBinaryJoin(t, done)
						}
					}()
					go func() { result, err := active.from(query, reader); done <- copyTextOutcome{result, err} }()
					select {
					case <-barrier.entered:
					case out := <-done:
						joined = true
						t.Fatal("native binary failed before callback", out)
					case <-p.Ctx.Done():
						t.Fatal("native callback deadline")
					}
					stop()
					barrier.unblock()
					out := copyBinaryJoin(t, done)
					joined = true
					if !errors.Is(out.err, context.Canceled) || out.result != (copyTextResult{}) {
						t.Fatal("native admitted binary cancellation", out)
					}
				}()
				c.selectRows(t, "SELECT * FROM copy_binary_native_io", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
			}
			var panicValue any
			func() {
				defer func() { panicValue = recover() }()
				_, _ = c.from(query, &copyBinaryErrorReader{prefix: copyTextChunks{text: string(prefix), widths: []int{7}}, panicValue: "binary callback panic"})
			}()
			if panicValue != "binary callback panic" {
				t.Fatal("binary panic ownership", panicValue)
			}
			c.selectRows(t, "SELECT * FROM copy_binary_native_io", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
			outputQuery := "COPY copy_binary_native_io TO STDOUT WITH(FORMAT binary)"
			for _, short := range []bool{false, true} {
				writer := &copyTextPrefixErrorWriter{err: sentinel}
				var output io.Writer = writer
				want := error(sentinel)
				if short {
					output = &copyBinaryShortWriter{}
					want = io.ErrShortWrite
				}
				closer := &copyBinaryWriterCloser{Writer: output}
				output = closer
				result, err = c.to(outputQuery, output)
				if !errors.Is(err, want) || result != (copyTextResult{}) {
					t.Fatal("binary writer identity/result", result, err)
				}
				if !short && writer.Len() == 0 {
					t.Fatal("missing irreversible binary prefix")
				}
				if closer.closed {
					t.Fatal("binary COPY closed caller writer")
				}
				c.selectRows(t, "SELECT * FROM copy_binary_native_io", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
			}
			func() {
				barrier := newCopyTextBarrier()
				writer := &copyTextBlockedWriter{barrier: barrier}
				ctx, stop := context.WithCancel(c.ctx)
				active := c
				active.ctx = ctx
				done := make(chan copyTextOutcome, 1)
				joined := false
				defer func() {
					stop()
					barrier.unblock()
					if !joined {
						copyBinaryJoin(t, done)
					}
				}()
				go func() { result, err := active.to(outputQuery, writer); done <- copyTextOutcome{result, err} }()
				select {
				case <-barrier.entered:
				case out := <-done:
					joined = true
					t.Fatal("binary output failed before callback", out)
				case <-p.Ctx.Done():
					t.Fatal("output callback deadline")
				}
				stop()
				barrier.unblock()
				out := copyBinaryJoin(t, done)
				joined = true
				if !errors.Is(out.err, context.Canceled) || out.result != (copyTextResult{}) || writer.Len() == 0 {
					t.Fatal("binary blocked output cancellation/prefix", out, writer.Len())
				}
			}()
			c.selectRows(t, "SELECT * FROM copy_binary_native_io", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})
			barrier := newCopyTextBarrier()
			done := make(chan copyTextOutcome, 1)
			joined := false
			defer func() {
				barrier.unblock()
				if !joined {
					copyBinaryJoin(t, done)
				}
			}()
			go func() {
				result, err := c.from(query, &copyBinaryBlockedReader{prefix: copyTextChunks{text: string(prefix), widths: []int{7}}, rest: copyTextChunks{text: string(copyBinary16(-1)), widths: []int{1}}, barrier: barrier})
				done <- copyTextOutcome{result, err}
			}()
			select {
			case <-barrier.entered:
			case out := <-done:
				joined = true
				t.Fatal("replacement COPY failed before callback", out)
			case <-p.Ctx.Done():
				t.Fatal("replacement callback deadline")
			}
			if err := stuntgres.Reset(c.ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fresh, err := p.Native.Conn(c.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			replacement := copyTextConnection{conn: fresh, ctx: c.ctx}
			replacement.exec(t, "CREATE TABLE copy_binary_replacement(v int4)")
			replacement.exec(t, "INSERT INTO copy_binary_replacement VALUES(7)")
			barrier.unblock()
			out := copyBinaryJoin(t, done)
			joined = true
			if out.err == nil || out.result != (copyTextResult{}) {
				t.Fatal("replacement resurrected binary COPY", out)
			}
			replacement.selectRows(t, "SELECT * FROM copy_binary_replacement", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
		})
	})
	t.Run("postgres_writer_identity", func(t *testing.T) {
		copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
			// pgx disposes its connection after caller writer failure. Native keeps
			// its reusable connection; that ownership contract is tested above.
			if !b.PG {
				return
			}
			c.exec(t, "CREATE TABLE copy_binary_writer(v int4)")
			result, err := c.from("COPY copy_binary_writer FROM STDIN WITH(FORMAT binary)", bytes.NewReader(copyBinaryStream(copyBinaryRow(copyBinary32(42)))))
			copyTextSuccess(t, result, err, 1)
			sentinel := errors.New("binary pgx caller writer failure")
			writer := &copyTextPrefixErrorWriter{err: sentinel}
			result, err = c.to("COPY copy_binary_writer TO STDOUT WITH(FORMAT binary)", writer)
			if !errors.Is(err, sentinel) || result != (copyTextResult{}) || writer.Len() == 0 {
				t.Fatal("pgx binary writer identity/prefix", result, err, writer.Bytes())
			}
			closed := false
			rawErr := c.conn.Raw(func(raw any) error { closed = raw.(*stdlib.Conn).Conn().IsClosed(); return nil })
			if rawErr != nil || !closed {
				t.Fatal("pgx binary writer disposal", closed, rawErr)
			}
			fresh, err := p.CopyPostgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			recovered := copyTextConnection{conn: fresh, ctx: p.Ctx, pg: true}
			recovered.selectRows(t, "SELECT * FROM copy_binary_writer", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		})
	})
}

type copyBinaryShortWriter struct{}

func (*copyBinaryShortWriter) Write(p []byte) (int, error) { return max(0, len(p)-1), nil }
