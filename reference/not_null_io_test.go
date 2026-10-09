package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"sync"
	"testing"
	"time"
)

type referenceNotNullShortWriter struct{}

func (referenceNotNullShortWriter) Write([]byte) (int, error) { return 0, nil }

type referenceNotNullGatedWriter struct {
	started, release chan struct{}
	first            sync.Once
	output           bytes.Buffer
}

func (w *referenceNotNullGatedWriter) Write(p []byte) (int, error) {
	w.first.Do(func() { close(w.started); <-w.release })
	return w.output.Write(p)
}

func TestReferenceNotNullControlsExternalIOHasNoTargetLock(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(7)", 1))
	if err := stuntgres.Dump(p.ctx, p.native, referenceNotNullShortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	writer := &referenceNotNullGatedWriter{started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	ctx, cancel := context.WithTimeout(p.ctx, 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- stuntgres.Dump(ctx, p.native, writer) }()
	joined := false
	defer func() {
		release.Do(func() { close(writer.release) })
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("dump cleanup did not join")
			}
		}
	}()
	select {
	case <-writer.started:
	case <-ctx.Done():
		t.Fatal("dump never reached external I/O", ctx.Err())
	}
	execDone := make(chan error, 1)
	go func() {
		result, err := p.memory.ExecContext(ctx, "ALTER TABLE t DROP CONSTRAINT nn")
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count != 0 {
				err = errors.New("ALTER affected count is not zero")
			}
		}
		execDone <- err
	}()
	execJoined := false
	defer func() {
		if !execJoined {
			cancel()
			release.Do(func() { close(writer.release) })
			select {
			case <-execDone:
			case <-time.After(3 * time.Second):
				t.Error("ALTER cleanup did not join")
			}
		}
	}()
	select {
	case err := <-execDone:
		execJoined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Dump held target lock over external I/O", ctx.Err())
	}
	release.Do(func() { close(writer.release) })
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("dump did not finish", ctx.Err())
	}
	if !strings.Contains(writer.output.String(), `CONSTRAINT "nn" NOT NULL`) {
		t.Fatal("capture lost old constraint identity", writer.output.String())
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("INSERT INTO t VALUES(NULL)", 1))
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(writer.output.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE t"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, writer.output.String()); err != nil {
			t.Fatal("captured stream replay", err)
		}
	}
	p.step(t, referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	p.step(t, referenceImplicitError("INSERT INTO t VALUES(NULL)", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"}))
	p.step(t, referencePrepared(referenceExec("ALTER TABLE t DROP CONSTRAINT nn", 0)))
}
