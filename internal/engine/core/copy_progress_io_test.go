package core_test

import (
	"errors"
	"io"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

type progressUnitIO struct {
	entered, release chan struct{}
	once             sync.Once
	err              error
	read             bool
}

func (g *progressUnitIO) gate() { g.once.Do(func() { close(g.entered); <-g.release }) }
func (g *progressUnitIO) Read(p []byte) (int, error) {
	g.gate()
	if g.read {
		return 0, io.EOF
	}
	g.read = true
	return copy(p, "1\n"), g.err
}
func (g *progressUnitIO) Write(p []byte) (int, error) { g.gate(); return min(2, len(p)), g.err }

func TestCopyProgressPartialIO(t *testing.T) {
	for _, phase := range []string{"read_eof", "read_error", "write_error", "write_short"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			worker := sessionStart(t, e, nil)
			sessionSQL(t, worker, "CREATE TABLE t(v INT)")
			sessionSQL(t, worker, "INSERT INTO t VALUES(123)")
			op := transactionOperation(t, worker)
			scope, err := e.StartStatement(op)
			if err != nil {
				op.Done()
				t.Fatal(err)
			}
			failure := errors.New("partial COPY I/O failure")
			g := &progressUnitIO{entered: make(chan struct{}), release: make(chan struct{}), err: failure}
			if phase == "read_eof" {
				g.err = io.EOF
			}
			if phase == "write_short" {
				g.err = nil
			}
			q := "COPY t FROM STDIN"
			if phase == "write_error" || phase == "write_short" {
				q = "COPY t TO STDOUT"
			}
			syntax := enginetest.Statement(t, q).(*parser.Copy)
			done := make(chan error, 1)
			joined := false
			var release sync.Once
			unblock := func() { release.Do(func() { close(g.release) }) }
			defer func() {
				if !joined {
					unblock()
					<-done
				}
			}()
			go func() { _, err := scope.Copy(syntax, g, g); err = scope.Finish(err); op.Done(); done <- err }()
			select {
			case <-g.entered:
			case err := <-done:
				joined = true
				t.Fatal("COPY ended before real I/O", err)
			}
			probe := core.ScopeCopyProgressProbe(scope)
			if f := probe(); f.BytesProcessed != 0 || f.TuplesProcessed != 0 {
				t.Fatalf("blocked I/O counted prematurely: %+v", f)
			}
			unblock()
			err = <-done
			joined = true
			f := probe()
			if f.BytesProcessed != 2 {
				t.Fatalf("partial n+error/EOF bytes lost: %+v err=%v", f, err)
			}
			if phase == "read_eof" {
				if err != nil || f.TuplesProcessed != 1 {
					t.Fatalf("n+EOF row completion: %+v err=%v", f, err)
				}
			} else {
				want := failure
				if phase == "write_short" {
					want = io.ErrShortWrite
				}
				if !errors.Is(err, want) {
					t.Fatalf("I/O identity got=%v want=%v", err, want)
				}
				if phase != "read_error" && f.TuplesProcessed != 0 {
					t.Fatalf("partial row write counted as tuple: %+v", f)
				}
			}
		})
	}
}
