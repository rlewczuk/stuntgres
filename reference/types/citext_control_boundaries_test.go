package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type citextFailWriter struct{ err error }

func (w citextFailWriter) Write([]byte) (int, error) { return 0, w.err }

type citextFailReader struct{ err error }

func (r citextFailReader) Read([]byte) (int, error) { return 0, r.err }

func citextControlBoundaries(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, dump []byte) {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	var output bytes.Buffer
	if err := stuntgres.Dump(ctx, p.Native, &output); !errors.Is(err, context.Canceled) || output.Len() != 0 {
		t.Fatalf("cancelled Dump %v bytes=%d", err, output.Len())
	}
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Restore %v", err)
	}
	citextControlChecks(t, p, b, c)
	failure := errors.New("citext control I/O boundary")
	if err := stuntgres.Dump(p.Ctx, p.Native, citextFailWriter{failure}); !errors.Is(err, failure) {
		t.Fatalf("Dump writer failure %v", err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, citextFailReader{failure}); !errors.Is(err, failure) {
		t.Fatalf("Restore reader failure %v", err)
	}
	citextControlChecks(t, p, b, c)
}
