package core_test

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestCopyCSVLifecycle(t *testing.T) {
	t.Run("OwnerReservation", func(t *testing.T) { copyOwnerReservation(t, copyfmt.CSV) })
	t.Run("OperationExpiry", func(t *testing.T) { copyTimeoutTest(t, true) })
	t.Run("FinalCancellationVeto", func(t *testing.T) { copyFinalCancellationVeto(t, true) })
}

func copyOwnerReservation(t *testing.T, format copyfmt.Format) {
	for _, entry := range []string{"statement", "begin", "commit", "rollback"} {
		t.Run(entry, func(t *testing.T) {
			e, sess, op, scope := copyUnitStart(t, true)
			reader := &copyUnitReader{entered: make(chan struct{}), release: make(chan struct{})}
			syntax := copyUnitSyntax()
			if format == copyfmt.CSV {
				syntax.Options = []parser.CopyOption{{Name: "format", Value: "csv", HasValue: true, Argument: parser.CopyArgumentScalar}}
				reader.first = true
				reader.prefix = strings.NewReader("\"1\n")
				reader.suffix = strings.NewReader("\"\n")
			}
			if format == copyfmt.Binary {
				syntax = copyBinaryUnitSyntax()
				stream := copyBinaryUnitStream(t)
				split := len(stream) - 5
				reader.first = true
				reader.prefix = strings.NewReader(string(stream[:split]))
				reader.suffix = strings.NewReader(string(stream[split:]))
			}
			copyDone := make(chan error, 1)
			go func() {
				_, err := scope.Copy(syntax, reader, nil)
				err = scope.Finish(err)
				op.Done()
				copyDone <- err
			}()
			released := false
			defer func() {
				if !released {
					close(reader.release)
					<-copyDone
				}
			}()
			select {
			case <-reader.entered:
			case err := <-copyDone:
				released = true
				close(reader.release)
				t.Fatal("COPY failed before reader barrier", err)
			}
			before := scope.TxRef
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			waiter, err := sess.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer waiter.Done()
			waiting := &copyWaitContext{Context: waiter.Ctx, entered: make(chan struct{})}
			waiter.Ctx = waiting
			started := make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				close(started)
				var err error
				switch entry {
				case "statement":
					var next *engine.StatementScope
					next, err = e.StartStatement(waiter)
					if next != nil {
						err = next.Finish(errors.New("unexpected reservation entry"))
					}
				case "begin":
					_, err = e.Begin(waiter, session.TxOptions{})
				case "commit":
					_, err = e.Commit(waiter, before)
				case "rollback":
					_, err = e.Rollback(waiter, before)
				}
				finished <- err
			}()
			<-started
			select {
			case <-waiting.entered:
			case err := <-finished:
				t.Fatal("owner entry did not wait for COPY", entry, err)
			}
			cancel()
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("reserved waiter cancellation", entry, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("reservation waiter could not cancel")
			}
			sess.Transactions.Mu.Lock()
			ref, command := sess.Transactions.Ref, sess.Transactions.Command
			sess.Transactions.Mu.Unlock()
			if ref != before || command != scope.CommandID {
				t.Fatal("waiter mutated reserved owner")
			}
			close(reader.release)
			released = true
			if err := <-copyDone; err != nil {
				t.Fatal(err)
			}
			scope.Owner.Admission.Lock()
			reserved := scope.Owner.CopyLease != nil
			scope.Owner.Admission.Unlock()
			if reserved {
				t.Fatal("reservation leaked")
			}
			nextOp := enginetest.TransactionOperation(t, sess)
			next, err := e.StartStatement(nextOp)
			if err != nil {
				nextOp.Done()
				t.Fatal(err)
			}
			if err = next.Finish(nil); err != nil {
				t.Fatal(err)
			}
			nextOp.Done()
			if err = scope.Finish(nil); err != nil {
				t.Fatal("idempotent finish", err)
			}
		})
	}
}

func copyTimeoutTest(t *testing.T, csv bool) {
	for _, setting := range []string{"statement_timeout", "transaction_timeout"} {
		t.Run(setting, func(t *testing.T) {
			e := engine.New()
			session := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, session, "CREATE TABLE t(v INT)")
			enginetest.SessionSQL(t, session, "SET "+setting+" TO '100ms'")
			if setting == "transaction_timeout" {
				enginetest.SessionSQL(t, session, "BEGIN")
			}
			op, _, err := session.AcquireCommand(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				op.Done()
				t.Fatal(err)
			}
			reader := &copyUnitReader{entered: make(chan struct{}), release: make(chan struct{})}
			syntax := copyUnitSyntax()
			if csv {
				syntax.Options = []parser.CopyOption{{Name: "format", Value: "csv", HasValue: true, Argument: parser.CopyArgumentScalar}}
				reader.first = true
				reader.prefix = strings.NewReader("1\n\"2\n")
			}
			done := make(chan error, 1)
			go func() {
				_, err := scope.Copy(syntax, reader, nil)
				err = op.CommandError(scope.Finish(err))
				op.Done()
				done <- err
			}()
			released := false
			defer func() {
				if !released {
					close(reader.release)
					<-done
				}
			}()
			select {
			case <-reader.entered:
			case err := <-done:
				released = true
				close(reader.release)
				t.Fatal("COPY timed out before callback barrier", err)
			}
			select {
			case <-op.Context().Done():
			case <-time.After(5 * time.Second):
				t.Fatal("timeout could not cancel blocked COPY")
			}
			close(reader.release)
			released = true
			err = <-done
			var record *sqlstate.Error
			if !errors.As(err, &record) {
				t.Fatal("timeout lost SQL diagnostic", err)
			}
			code, severity, message, errorContext := "57014", "ERROR", "canceling statement due to statement timeout", "COPY t, line 2"
			if setting == "transaction_timeout" {
				code, severity, message, errorContext = "25P04", "FATAL", "terminating connection due to transaction timeout", "COPY t, line 2"
			}
			if csv {
				errorContext = "COPY t, line 3"
			}
			want := sqlstate.Error{Code: code, Severity: severity, SeverityUnlocalized: severity, Message: message, Context: errorContext}
			if *record != want {
				t.Fatalf("timeout diagnostic: %#v", record)
			}
			if scope.Owner.CopyLease != nil || scope.Stage.Rows != nil {
				t.Fatal("timeout leaked COPY reservation/stage")
			}
			if setting == "transaction_timeout" {
				if err = session.WaitEnd(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			peer := enginetest.StartSession(t, e, nil)
			result := enginetest.SessionSQL(t, peer, "SELECT v FROM t")
			if result.Count != 0 {
				t.Fatal("timeout published COPY prefix", result.Count)
			}
			if result.Rows != nil {
				result.Rows.Close()
			}
		})
	}
}

func copyFinalCancellationVeto(t *testing.T, csv bool) {
	e, _, op, scope := copyUnitStart(t, false)
	syntax := copyUnitSyntax()
	if csv {
		syntax.Options = []parser.CopyOption{{Name: "format", Value: "csv", HasValue: true, Argument: parser.CopyArgumentScalar}}
	}
	result, err := scope.Copy(syntax, strings.NewReader("1\n"), nil)
	if err != nil || result != 1 {
		t.Fatal(result, err)
	}
	op.Cancel(context.Canceled)
	if err = scope.Finish(nil); err == nil {
		t.Fatal("canceled final publication succeeded")
	}
	op.Done()
	peer := enginetest.StartSession(t, e, nil)
	observer := enginetest.TransactionOperation(t, peer)
	defer observer.Done()
	rows, readErr := e.Execute(observer, enginetest.Statement(t, "SELECT * FROM t"), 0, nil)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if rows.Rows != nil {
		defer rows.Rows.Close()
	}
	if rows.Count != 0 {
		t.Fatal("canceled COPY published rows", rows.Count)
	}
	if scope.Stage.Rows != nil || scope.Owner.CopyLease != nil {
		t.Fatal("final cancellation leaked COPY stage")
	}
}

func copyUnitStart(t *testing.T, explicit bool) (*engine.Engine, *session.Session, *session.Operation, *engine.StatementScope) {
	t.Helper()
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "CREATE TABLE t(v INT PRIMARY KEY,i bigint GENERATED BY DEFAULT AS IDENTITY)")
	if explicit {
		enginetest.SessionSQL(t, session, "BEGIN")
	}
	op := enginetest.TransactionOperation(t, session)
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	return e, session, op, scope
}

type copyUnitReader struct {
	entered, release chan struct{}
	once             sync.Once
	first            bool
	panicValue       any
	prefix, suffix   io.Reader
}

func (r *copyUnitReader) Read(p []byte) (int, error) {
	if r.prefix != nil {
		n, err := r.prefix.Read(p)
		if err != io.EOF {
			return n, err
		}
		r.prefix = nil
	}
	if !r.first {
		r.first = true
		return copy(p, "1\n"), nil
	}
	r.once.Do(func() { close(r.entered) })
	<-r.release
	if r.panicValue != nil {
		panic(r.panicValue)
	}
	if r.suffix != nil {
		return r.suffix.Read(p)
	}
	return 0, io.EOF
}

func copyUnitSyntax() *parser.Copy {
	return &parser.Copy{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "t"}}}, Columns: []parser.Identifier{{Text: "v"}}, From: true, Endpoint: parser.CopyStdin}
}

func copyBinaryUnitSyntax() *parser.Copy {
	s := copyUnitSyntax()
	s.Options = []parser.CopyOption{{Name: "format", Value: "binary", HasValue: true, Argument: parser.CopyArgumentScalar}}
	return s
}

func copyBinaryUnitStream(t *testing.T) []byte {
	t.Helper()
	p, err := hex.DecodeString("5047434f50590aff0d0a0000000000000000000001000000040000000100010000000400000002ffff")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type copyWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *copyWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}
