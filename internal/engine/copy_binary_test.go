package engine

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func copyBinaryUnitStream(t *testing.T) []byte {
	t.Helper()
	p, err := hex.DecodeString("5047434f50590aff0d0a0000000000000000000001000000040000000100010000000400000002ffff")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func copyBinaryUnitSyntax() *parser.Copy {
	s := copyUnitSyntax()
	s.Options = []parser.CopyOption{{Name: "format", Value: "binary", HasValue: true, Argument: parser.CopyArgumentScalar}}
	return s
}
func TestCopyBinaryLifecycle(t *testing.T) {
	t.Run("OwnerReservation", func(t *testing.T) { copyOwnerReservation(t, copyFormatBinary) })
	t.Run("Replacement", func(t *testing.T) { copyReplacement(t, copyFormatBinary) })
	for _, setting := range []string{"statement_timeout", "transaction_timeout", "caller_cancel"} {
		for _, phase := range []string{"payload", "trailer"} {
			t.Run(setting+"/"+phase, func(t *testing.T) {
				e := New()
				session := sessionStart(t, e, nil)
				sessionSQL(t, session, "CREATE TABLE t(v INT)")
				if setting != "caller_cancel" {
					sessionSQL(t, session, "SET "+setting+" TO '100ms'")
				}
				if setting == "transaction_timeout" {
					sessionSQL(t, session, "BEGIN")
				}
				ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				op, _, err := session.AcquireCommand(ctx)
				if err != nil {
					t.Fatal(err)
				}
				scope, err := e.StartStatement(op)
				if err != nil {
					op.Done()
					t.Fatal(err)
				}
				stream := copyBinaryUnitStream(t)
				split := len(stream)
				if phase == "payload" {
					split -= 5
				}
				reader := &copyUnitReader{entered: make(chan struct{}), release: make(chan struct{}), first: true, prefix: bytes.NewReader(stream[:split]), suffix: bytes.NewReader(stream[split:])}
				done := make(chan error, 1)
				joined := false
				defer func() {
					if !joined {
						close(reader.release)
						<-done
					}
				}()
				go func() {
					_, err := scope.Copy(copyBinaryUnitSyntax(), reader, nil)
					err = op.CommandError(scope.Finish(err))
					op.Done()
					done <- err
				}()
				select {
				case <-reader.entered:
				case err := <-done:
					joined = true
					close(reader.release)
					t.Fatal("binary COPY failed before barrier", err)
				case <-ctx.Done():
					t.Fatal("binary COPY barrier deadline")
				}
				if setting == "caller_cancel" {
					op.Cancel(context.Canceled)
				}
				select {
				case <-op.Context().Done():
				case <-ctx.Done():
					t.Fatal("operation deadline did not interrupt binary callback")
				}
				close(reader.release)
				err = <-done
				joined = true
				if setting == "caller_cancel" {
					if !errors.Is(err, context.Canceled) {
						t.Fatal("caller identity", err)
					}
				} else {
					var record *sqlstate.Error
					if !errors.As(err, &record) {
						t.Fatal("SQL deadline identity", err)
					}
					code, severity, message := "57014", "ERROR", "canceling statement due to statement timeout"
					if setting == "transaction_timeout" {
						code, severity, message = "25P04", "FATAL", "terminating connection due to transaction timeout"
					}
					location := "COPY t, line 3"
					if phase == "payload" {
						location = "COPY t, line 2, column v"
					}
					want := sqlstate.Error{Code: code, Severity: severity, SeverityUnlocalized: severity, Message: message, Context: location}
					if *record != want {
						t.Fatalf("binary timeout=%#v want=%#v", record, want)
					}
				}
				if scope.owner.CopyLease != nil || scope.stage.rows != nil {
					t.Fatal("binary interruption leaked reservation/stage")
				}
				if setting == "transaction_timeout" {
					if err := session.WaitEnd(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
				peer := sessionStart(t, e, nil)
				result := sessionSQL(t, peer, "SELECT v FROM t")
				if result.Rows != nil {
					defer result.Rows.Close()
				}
				if result.Count != 0 {
					t.Fatal("binary interruption published rows", result.Count)
				}
			})
		}
	}
}
func TestCopyBinaryFinalCancellationVeto(t *testing.T) {
	e, _, op, scope := copyUnitStart(t, false)
	count, err := scope.Copy(copyBinaryUnitSyntax(), bytes.NewReader(copyBinaryUnitStream(t)), nil)
	if err != nil || count != 2 {
		scope.Finish(err)
		op.Done()
		t.Fatal(count, err)
	}
	op.Cancel(context.Canceled)
	err = scope.Finish(nil)
	op.Done()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("final cancellation identity", err)
	}
	peer := sessionStart(t, e, nil)
	result := sessionSQL(t, peer, "SELECT v FROM t")
	if result.Rows != nil {
		defer result.Rows.Close()
	}
	if result.Count != 0 || scope.stage.rows != nil || scope.owner.CopyLease != nil {
		t.Fatal("final binary cancellation published or leaked", result.Count)
	}
}
func TestCopyBinaryOptions(t *testing.T) {
	for _, q := range []string{"COPY t FROM STDIN WITH(FORMAT binary)", "COPY t TO STDOUT WITH(FORMAT binary,HEADER false)"} {
		s := statement(t, q).(*parser.Copy)
		o, err := parseCopyOptions(s)
		if err != nil || o.format != copyFormatBinary {
			t.Fatal(o, err)
		}
	}
	for _, tc := range []struct{ q, code, message string }{{"COPY t FROM STDIN WITH(FORMAT binary,HEADER true)", "0A000", "cannot specify HEADER in BINARY mode"}, {"COPY t FROM STDIN WITH(FORMAT binary,NULL '')", "42601", "cannot specify NULL in BINARY mode"}, {"COPY t TO STDOUT WITH(FORMAT binary,QUOTE 'x')", "0A000", "COPY QUOTE requires CSV mode"}} {
		_, err := parseCopyOptions(statement(t, tc.q).(*parser.Copy))
		var d *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &d) || *d != want {
			t.Fatal(tc.q, err)
		}
	}
}
func TestCopyBinaryFraming(t *testing.T) {
	for _, tc := range []struct {
		name       string
		payload    func(*testing.T) []byte
		count      int64
		diagnostic *sqlstate.Error
	}{
		{name: "full", payload: copyBinaryUnitStream, count: 2},
		{name: "partial_next_count", payload: func(t *testing.T) []byte { p := copyBinaryUnitStream(t); return append(p[:len(p)-2], 0) }, count: 2},
		{name: "signature", payload: func(*testing.T) []byte { return []byte("not binary") }, diagnostic: &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P04", Message: "COPY file signature not recognized"}},
		{name: "trailer_tail", payload: func(t *testing.T) []byte { return append(copyBinaryUnitStream(t), 0) }, diagnostic: &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P04", Message: "received copy data after EOF marker", Context: "COPY t, line 3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, op, scope := copyUnitStart(t, false)
			count, err := scope.Copy(copyBinaryUnitSyntax(), bytes.NewReader(tc.payload(t)), nil)
			err = scope.Finish(err)
			op.Done()
			if tc.diagnostic != nil {
				var d *sqlstate.Error
				if !errors.As(err, &d) || *d != *tc.diagnostic || count != 0 {
					t.Fatal(count, err)
				}
			} else if err != nil || count != tc.count {
				t.Fatal(count, err)
			}
			peer := sessionStart(t, e, nil)
			result := sessionSQL(t, peer, "SELECT v FROM t")
			if result.Rows != nil {
				defer result.Rows.Close()
			}
			if int64(result.Count) != tc.count {
				t.Fatal("whole table count", result.Count, tc.count)
			}
		})
	}
}
func TestCopyBinaryOutput(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	sessionSQL(t, session, "CREATE TABLE t(v INT)")
	sessionSQL(t, session, "INSERT INTO t VALUES(1),(2)")
	op := transactionOperation(t, session)
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	syntax := copyBinaryUnitSyntax()
	syntax.From = false
	syntax.Endpoint = parser.CopyStdout
	var output bytes.Buffer
	count, err := scope.Copy(syntax, nil, &output)
	err = scope.Finish(err)
	op.Done()
	if err != nil || count != 2 || !bytes.Equal(output.Bytes(), copyBinaryUnitStream(t)) {
		t.Fatalf("binary export %x count%d err%v", output.Bytes(), count, err)
	}
}
