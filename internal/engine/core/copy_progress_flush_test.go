package core_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestCopyProgressFailedFlush(t *testing.T) {
	e := engine.New()
	worker := sessionStart(t, e, nil)
	sessionSQL(t, worker, "CREATE TABLE t(v INT PRIMARY KEY)")
	observer := sessionStart(t, e, nil)
	observerOp := transactionOperation(t, observer)
	defer observerOp.Done()
	op := transactionOperation(t, worker)
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	var prefix strings.Builder
	for i := 1; i < 1000; i++ {
		fmt.Fprintf(&prefix, "%d\n", i)
	}
	reader := &copyUnitReader{first: true, prefix: strings.NewReader(prefix.String()), suffix: strings.NewReader("1\n"), entered: make(chan struct{}), release: make(chan struct{})}
	type outcome struct {
		count int64
		err   error
	}
	done := make(chan outcome, 1)
	joined := false
	defer func() {
		if !joined {
			close(reader.release)
			<-done
		}
	}()
	go func() {
		count, err := scope.Copy(copyUnitSyntax(), reader, nil)
		err = scope.Finish(err)
		op.Done()
		done <- outcome{count, err}
	}()
	select {
	case <-reader.entered:
	case out := <-done:
		joined = true
		t.Fatalf("COPY ended before failed-flush gate: %+v", out)
	}
	probe := core.ScopeCopyProgressProbe(scope)
	if f := probe(); f.TuplesProcessed != 0 {
		t.Fatal("pending buffer counted", f)
	}
	close(reader.release)
	out := <-done
	joined = true
	var record *sqlstate.Error
	if out.count != 0 || !errors.As(out.err, &record) || record.Code != "23505" {
		t.Fatalf("constraint failure: %+v", out)
	}
	if f := probe(); f.TuplesProcessed != 0 || f.TuplesSkipped != 0 {
		t.Fatal("failed flush reported completion/soft skip", f)
	}
	if facts := copyProgressFacts(t, e, observerOp); len(facts) != 0 {
		t.Fatal("failed command retained", facts)
	}
}

func TestCopyProgressPreconversionBytes(t *testing.T) {
	e := engine.New()
	worker := sessionStart(t, e, nil)
	sessionSQL(t, worker, "CREATE TABLE t(v TEXT)")
	observer := sessionStart(t, e, nil)
	observerOp := transactionOperation(t, observer)
	defer observerOp.Done()
	op := transactionOperation(t, worker)
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	reader := &copyUnitReader{first: true, prefix: strings.NewReader("\xe9\n"), entered: make(chan struct{}), release: make(chan struct{})}
	syntax := enginetest.Statement(t, "COPY t FROM STDIN WITH(ENCODING 'LATIN1')").(*parser.Copy)
	done := make(chan error, 1)
	joined := false
	defer func() {
		if !joined {
			close(reader.release)
			<-done
		}
	}()
	go func() { _, err := scope.Copy(syntax, reader, nil); err = scope.Finish(err); op.Done(); done <- err }()
	select {
	case <-reader.entered:
	case err := <-done:
		joined = true
		t.Fatal("COPY ended before encoding gate", err)
	}
	facts := copyProgressFacts(t, e, observerOp)
	if len(facts) != 1 || facts[0].BytesProcessed != 2 || facts[0].TuplesProcessed != 1 {
		t.Fatalf("raw LATIN1 bytes must not count expanded UTF8: %+v", facts)
	}
	close(reader.release)
	err = <-done
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	if facts := copyProgressFacts(t, e, observerOp); len(facts) != 0 {
		t.Fatal("encoded COPY retained", facts)
	}
}
