package reference_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/jackc/pgx/v5/stdlib"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
	"time"
)

type copyTextOutcome struct {
	result copyTextResult
	err    error
}
type copyTextBarrier struct {
	entered, release chan struct{}
	once             sync.Once
}

func newCopyTextBarrier() *copyTextBarrier {
	return &copyTextBarrier{entered: make(chan struct{}), release: make(chan struct{})}
}
func (b *copyTextBarrier) wait()    { close(b.entered); <-b.release }
func (b *copyTextBarrier) unblock() { b.once.Do(func() { close(b.release) }) }

type copyTextBlockedReader struct {
	barrier *copyTextBarrier
	first   bool
}

func (r *copyTextBlockedReader) Read(p []byte) (int, error) {
	if !r.first {
		r.first = true
		return copy(p, "1\tone\n"), nil
	}
	r.barrier.wait()
	return 0, io.EOF
}

type copyTextBlockedWriter struct {
	barrier *copyTextBarrier
	first   bool
	bytes.Buffer
}

func (w *copyTextBlockedWriter) Write(p []byte) (int, error) {
	if !w.first {
		w.first = true
		w.barrier.wait()
	}
	return w.Buffer.Write(p)
}

type copyTextFailReader struct {
	first bool
	err   error
}

func (r *copyTextFailReader) Read(p []byte) (int, error) {
	if !r.first {
		r.first = true
		return copy(p, "1\tone\n"), nil
	}
	return 0, r.err
}

type copyTextFailWriter struct {
	err   error
	short bool
	calls int
}

func (w *copyTextFailWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		return len(p), nil
	}
	if w.short {
		return 0, nil
	}
	return 0, w.err
}

type copyTextPrefixErrorWriter struct {
	err error
	bytes.Buffer
}

func (w *copyTextPrefixErrorWriter) Write(p []byte) (int, error) {
	n := min(3, len(p))
	_, _ = w.Buffer.Write(p[:n])
	return n, w.err
}

type copyTextPanicReader struct{ first bool }

func (r *copyTextPanicReader) Read(p []byte) (int, error) {
	if !r.first {
		r.first = true
		return copy(p, "1\tone\n"), nil
	}
	panic("COPY reader panic")
}

func TestReferenceCopyTextAdmissionAndLocks(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_locks(id int,v text)")
		c.exec(t, "INSERT INTO copy_locks VALUES(99,'sentinel')")
		c.exec(t, "CREATE ROLE copy_stream_actor")
		if b.PG {
			c.exec(t, "GRANT USAGE ON SCHEMA public TO copy_stream_actor")
		}
		c.exec(t, "GRANT INSERT(id),SELECT(id) ON copy_locks TO copy_stream_actor")
		c.exec(t, "SET ROLE copy_stream_actor")
		never := &copyTextNoRead{}
		denied, deniedErr := c.from("COPY copy_locks(v) FROM STDIN", never)
		copyTextDiagnostic(t, denied, deniedErr, fixture.Diagnostic{Code: "42501", Message: "permission denied for table copy_locks"})
		if !b.PG && never.calls != 0 {
			t.Fatal("denied FROM read caller")
		}
		var rejected bytes.Buffer
		denied, deniedErr = c.to("COPY copy_locks TO STDOUT", &rejected)
		copyTextDiagnostic(t, denied, deniedErr, fixture.Diagnostic{Code: "42501", Message: "permission denied for table copy_locks"})
		if rejected.Len() != 0 {
			t.Fatal("denied TO wrote caller")
		}
		accepted, acceptedErr := c.from("COPY copy_locks(id) FROM STDIN", strings.NewReader("8\n"))
		copyTextSuccess(t, accepted, acceptedErr, 1)
		c.output(t, "COPY (SELECT id FROM copy_locks ORDER BY id) TO STDOUT", "8\n99\n", 2)
		c.exec(t, "RESET ROLE")
		c.exec(t, "DELETE FROM copy_locks WHERE id=8")
		peerDB := p.Native
		if b.PG {
			peerDB = p.CopyPostgres
		}
		peer, err := peerDB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		observer := copyTextConnection{conn: peer, ctx: p.Ctx, pg: b.PG}
		observer.exec(t, "BEGIN")
		observer.exec(t, "LOCK TABLE copy_locks IN ACCESS EXCLUSIVE MODE")
		c.exec(t, "SET lock_timeout TO '30ms'")
		for _, from := range []bool{true, false} {
			var result copyTextResult
			var err error
			if from {
				result, err = c.from("COPY copy_locks FROM STDIN", strings.NewReader("1\tone\n"))
			} else {
				result, err = c.to("COPY copy_locks TO STDOUT", &bytes.Buffer{})
			}
			copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"})
		}
		c.exec(t, "SET lock_timeout TO 0")
		c.exec(t, "SET statement_timeout TO '30ms'")
		for _, from := range []bool{true, false} {
			var result copyTextResult
			var err error
			if from {
				result, err = c.from("COPY copy_locks FROM STDIN", strings.NewReader("1\tone\n"))
			} else {
				result, err = c.to("COPY copy_locks TO STDOUT", &bytes.Buffer{})
			}
			copyTextDiagnostic(t, result, err, fixture.Diagnostic{Code: "57014", Message: "canceling statement due to statement timeout"})
		}
		c.exec(t, "RESET statement_timeout")
		observer.exec(t, "ROLLBACK")
		c.exec(t, "SET lock_timeout TO 0")
		barrier := newCopyTextBarrier()
		done := make(chan copyTextOutcome, 1)
		joined := false
		defer func() {
			barrier.unblock()
			if !joined {
				<-done
			}
		}()
		go func() {
			result, err := c.from("COPY copy_locks FROM STDIN", &copyTextBlockedReader{barrier: barrier})
			done <- copyTextOutcome{result, err}
		}()
		select {
		case <-barrier.entered:
		case <-p.Ctx.Done():
			t.Fatal("COPY did not reach external reader", p.Ctx.Err())
		}
		observer.selectRows(t, "SELECT * FROM copy_locks ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
		barrier.unblock()
		out := <-done
		joined = true
		copyTextSuccess(t, out.result, out.err, 1)
		observer.selectRows(t, "SELECT * FROM copy_locks ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(99), "sentinel"}})
		writerBarrier := newCopyTextBarrier()
		writer := &copyTextBlockedWriter{barrier: writerBarrier}
		writeDone := make(chan copyTextOutcome, 1)
		writeJoined := false
		defer func() {
			writerBarrier.unblock()
			if !writeJoined {
				<-writeDone
			}
		}()
		go func() {
			result, err := c.to("COPY (SELECT * FROM copy_locks ORDER BY id) TO STDOUT", writer)
			writeDone <- copyTextOutcome{result, err}
		}()
		select {
		case <-writerBarrier.entered:
		case <-p.Ctx.Done():
			t.Fatal("COPY did not reach external writer")
		}
		observer.selectRows(t, "SELECT * FROM copy_locks ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(99), "sentinel"}})
		writerBarrier.unblock()
		out = <-writeDone
		writeJoined = true
		copyTextSuccess(t, out.result, out.err, 2)
		if writer.String() != "1\tone\n99\tsentinel\n" {
			t.Fatalf("writer bytes=%q", writer.String())
		}
	})
}

func TestReferenceCopyTextCancellation(t *testing.T) {
	copyTextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c copyTextConnection) {
		c.exec(t, "CREATE TABLE copy_cancel(id int,v text)")
		c.exec(t, "INSERT INTO copy_cancel VALUES(99,'sentinel')")
		canceled, cancel := context.WithCancel(c.ctx)
		cancel()
		before := c
		before.ctx = canceled
		never := &copyTextNoRead{}
		result, err := before.from("COPY copy_cancel FROM STDIN", never)
		if !errors.Is(err, context.Canceled) || result != (copyTextResult{}) || never.calls != 0 {
			t.Fatal("precanceled COPY admitted input", result, err, never.calls)
		}
		peerDB := p.Native
		if b.PG {
			peerDB = p.CopyPostgres
		}
		peer, err := peerDB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		observer := copyTextConnection{conn: peer, ctx: p.Ctx, pg: b.PG}
		var activePID int
		if b.PG {
			if err := c.conn.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&activePID); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithCancel(c.ctx)
		defer cancel()
		active := c
		active.ctx = ctx
		barrier := newCopyTextBarrier()
		done := make(chan copyTextOutcome, 1)
		joined := false
		defer func() {
			cancel()
			barrier.unblock()
			if !joined {
				<-done
			}
		}()
		go func() {
			result, err := active.from("COPY copy_cancel FROM STDIN", &copyTextBlockedReader{barrier: barrier})
			done <- copyTextOutcome{result, err}
		}()
		select {
		case <-barrier.entered:
		case <-p.Ctx.Done():
			t.Fatal("COPY cancellation admission barrier")
		}
		if b.PG {
			observation, stop := context.WithTimeout(p.Ctx, 5*time.Second)
			defer stop()
			// Observe actual server COPY input admission, not only the client
			// reader. Keep CopyDone blocked until server cancellation completes.
			for {
				var reading bool
				if err := observer.conn.QueryRowContext(observation, "SELECT COALESCE((SELECT state='active' AND query=$2 AND wait_event='ClientRead' FROM pg_stat_activity WHERE pid=$1),FALSE)", activePID, "COPY copy_cancel FROM STDIN").Scan(&reading); err != nil {
					t.Fatal("server COPY admission", err)
				}
				if reading {
					break
				}
			}
			var signaled bool
			if err := observer.conn.QueryRowContext(observation, "SELECT pg_cancel_backend($1)", activePID).Scan(&signaled); err != nil || !signaled {
				t.Fatal("server COPY cancellation signal", signaled, err)
			}
			// The target signal is acknowledged. COPY error recovery may still
			// drain client protocol before idle, so release and join input next.
		} else {
			cancel()
		}
		barrier.unblock()
		out := <-done
		joined = true
		if out.err == nil || out.result != (copyTextResult{}) {
			t.Fatal("admitted cancellation returned success", out)
		}
		if !b.PG && !errors.Is(out.err, context.Canceled) {
			t.Fatal("native admitted cancellation lost ordinary identity", out.err)
		}
		if b.PG {
			copyTextDiagnostic(t, out.result, out.err, fixture.Diagnostic{Code: "57014", Message: "canceling statement due to user request", Context: "COPY copy_cancel, line 2"})
		}
		observer.selectRows(t, "SELECT * FROM copy_cancel ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
		writeFailure := errors.New("caller COPY writer failure")
		prefix := &copyTextPrefixErrorWriter{err: writeFailure}
		result, err = observer.to("COPY (SELECT * FROM copy_cancel ORDER BY id) TO STDOUT", prefix)
		if !errors.Is(err, writeFailure) || result != (copyTextResult{}) || prefix.Len() == 0 {
			t.Fatal("stream writer identity/irreversible prefix", result, err, prefix.String())
		}
		// pgconn genuinely disposes this writer-failed connection; native can reuse it.
		if !b.PG {
			observer.selectRows(t, "SELECT * FROM copy_cancel ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
		}
		if b.PG {
			closed := false
			rawErr := peer.Raw(func(raw any) error { closed = raw.(*stdlib.Conn).Conn().IsClosed(); return nil })
			if rawErr != nil || !closed {
				t.Fatal("real pgconn writer-error disposal", closed, rawErr)
			}
		}
		fresh, borrowErr := peerDB.Conn(p.Ctx)
		if borrowErr != nil {
			t.Fatal(borrowErr)
		}
		defer fresh.Close()
		recovered := copyTextConnection{conn: fresh, ctx: p.Ctx, pg: b.PG}
		recovered.selectRows(t, "SELECT * FROM copy_cancel ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
		if b.PG {
			// PostgreSQL exposes server-clock transaction deadlines; native expiry is
			// proved with the private Operation signal, not an invented public hook.
			timeoutConn, acquireErr := peerDB.Conn(p.Ctx)
			if acquireErr != nil {
				t.Fatal(acquireErr)
			}
			defer timeoutConn.Close()
			timeoutOwner := copyTextConnection{conn: timeoutConn, ctx: p.Ctx, pg: true}
			var pid int
			if err := timeoutConn.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			timeoutOwner.exec(t, "SET transaction_timeout TO '100ms'")
			timeoutOwner.selectRows(t, "SHOW transaction_timeout", []string{"transaction_timeout"}, []string{"TEXT"}, [][]any{{"100ms"}})
			timeoutOwner.exec(t, "BEGIN")
			expiry := newCopyTextBarrier()
			timeoutDone := make(chan copyTextOutcome, 1)
			timeoutJoined := false
			defer func() {
				expiry.unblock()
				if !timeoutJoined {
					<-timeoutDone
				}
			}()
			go func() {
				result, err := timeoutOwner.from("COPY copy_cancel FROM STDIN", &copyTextBlockedReader{barrier: expiry})
				timeoutDone <- copyTextOutcome{result, err}
			}()
			select {
			case <-expiry.entered:
			case outcome := <-timeoutDone:
				timeoutJoined = true
				t.Fatalf("transaction timeout before stream barrier: %+v", outcome)
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
			observation, stopObservation := context.WithTimeout(p.Ctx, 5*time.Second)
			defer stopObservation()
			for {
				var due bool
				// Expiry can be pending while COPY drains client input. Observe the
				// server transaction's own deadline, not a client sleep or teardown.
				if err := recovered.conn.QueryRowContext(observation, "SELECT NOT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1) OR COALESCE((SELECT clock_timestamp() >= xact_start + INTERVAL '100 milliseconds' FROM pg_stat_activity WHERE pid=$1),FALSE)", pid).Scan(&due); err != nil {
					t.Fatal("server transaction deadline observation", err)
				}
				if due {
					break
				}
			}
			expiry.unblock()
			expired := <-timeoutDone
			timeoutJoined = true
			copyTextDiagnostic(t, expired.result, expired.err, fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "25P04", Message: "terminating connection due to transaction timeout", Context: "COPY copy_cancel, line 2"})
			recovered.selectRows(t, "SELECT * FROM copy_cancel ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(99), "sentinel"}})
		}
		// pgconn cancellation may dispose the original connection; native must reuse.
		if !b.PG {
			ordinary := errors.New("caller COPY reader failure")
			result, err = c.from("COPY copy_cancel FROM STDIN", &copyTextFailReader{err: ordinary})
			if !errors.Is(err, ordinary) || result != (copyTextResult{}) {
				t.Fatal("reader identity", result, err)
			}
			c.exec(t, "INSERT INTO copy_cancel VALUES(2,'two')")
			for _, short := range []bool{false, true} {
				writer := &copyTextFailWriter{err: ordinary, short: short}
				result, err = c.to("COPY (SELECT * FROM copy_cancel ORDER BY id) TO STDOUT", writer)
				want := ordinary
				if short {
					want = io.ErrShortWrite
				}
				if !errors.Is(err, want) || result != (copyTextResult{}) {
					t.Fatal("writer identity/result", result, err)
				}
				c.selectRows(t, "SELECT * FROM copy_cancel ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "two"}, {int64(99), "sentinel"}})
			}
			var panicValue any
			func() {
				defer func() { panicValue = recover() }()
				_, _ = c.from("COPY copy_cancel FROM STDIN", &copyTextPanicReader{})
			}()
			if panicValue != "COPY reader panic" {
				t.Fatal("COPY callback panic not rethrown", panicValue)
			}
			c.selectRows(t, "SELECT * FROM copy_cancel ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "two"}, {int64(99), "sentinel"}})
			replace := newCopyTextBarrier()
			replacementDone := make(chan copyTextOutcome, 1)
			replacementJoined := false
			defer func() {
				replace.unblock()
				if !replacementJoined {
					<-replacementDone
				}
			}()
			go func() {
				result, err := c.from("COPY copy_cancel FROM STDIN", &copyTextBlockedReader{barrier: replace})
				replacementDone <- copyTextOutcome{result, err}
			}()
			<-replace.entered
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			observer.exec(t, "CREATE TABLE replacement(v int)")
			observer.exec(t, "INSERT INTO replacement VALUES(7)")
			replace.unblock()
			out = <-replacementDone
			replacementJoined = true
			if out.err == nil || out.result != (copyTextResult{}) {
				t.Fatal("replacement resurrected old COPY", out)
			}
			observer.selectRows(t, "SELECT * FROM replacement", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
		}
	})
}
