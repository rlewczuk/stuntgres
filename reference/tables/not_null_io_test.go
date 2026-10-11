package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
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
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
	if err := stuntgres.Dump(p.Ctx, p.Native, referenceNotNullShortWriter{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	writer := &referenceNotNullGatedWriter{started: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	ctx, cancel := context.WithTimeout(p.Ctx, 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- stuntgres.Dump(ctx, p.Native, writer) }()
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
		result, err := p.Memory.ExecContext(ctx, "ALTER TABLE t DROP CONSTRAINT nn")
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
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("INSERT INTO t VALUES(NULL)", 1))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(writer.output.Bytes())); err != nil {
		t.Fatal(err)
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE t"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, writer.output.String()); err != nil {
			t.Fatal("captured stream replay", err)
		}
	}
	p.Step(t, fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
	p.Step(t, fixture.ImplicitError("INSERT INTO t VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"}))
	p.Step(t, fixture.Prepared(fixture.Exec("ALTER TABLE t DROP CONSTRAINT nn", 0)))
}
