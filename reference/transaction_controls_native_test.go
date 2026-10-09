package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
)

type transactionControlFailingReader struct{ err error }

func (r transactionControlFailingReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceTransactionControlsNativeRoundTrip(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			f := newControlsReferenceFixture(t, p, backend.pg)
			ctx := p.ctx
			f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			f.exec(t, f.b, "INSERT INTO t VALUES(77)", false, 1)
			f.exec(t, f.a, "BEGIN", false, 0)
			f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
			f.rows(t, f.a, 1, 77)
			f.rows(t, f.b, 77)
			if !backend.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/clone")
				if err != nil {
					t.Fatal(err)
				}
				clone := sql.OpenDB(connector)
				t.Cleanup(func() { clone.Close() })
				if err := stuntgres.Restore(ctx, clone, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				rows, err := clone.QueryContext(ctx, "SELECT v FROM t")
				referenceLimitLockValues(t, relationOutcome{rows: rows, err: err}, false, 77)
				for _, script := range []string{"BEGIN;CREATE TABLE x(v INT);COMMIT;", "ROLLBACK;", "CREATE TABLE x(v INT);INSERT INTO x VALUES('bad');"} {
					if err := stuntgres.Restore(ctx, p.native, strings.NewReader(script)); err == nil {
						t.Fatal("restore accepted invalid script", script)
					}
					f.rows(t, f.b, 77)
				}
				readErr := errors.New("transaction controls reader failure")
				if err := stuntgres.Restore(ctx, p.native, transactionControlFailingReader{readErr}); !errors.Is(err, readErr) {
					t.Fatal(err)
				}
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if err := stuntgres.Restore(canceled, p.native, strings.NewReader("CREATE TABLE x(v INT)")); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				f.rows(t, f.a, 1, 77)
			}
			f.exec(t, f.a, "COMMIT", false, 0)
			f.rows(t, f.b, 1, 77)
			f.exec(t, f.a, "BEGIN", false, 0)
			f.exec(t, f.a, "INSERT INTO t VALUES(3)", false, 1)
			if !backend.pg {
				if err := stuntgres.Restore(ctx, p.native, strings.NewReader("CREATE TABLE t(v INT NOT NULL);INSERT INTO t VALUES(99);")); err != nil {
					t.Fatal(err)
				}
			}
			f.exec(t, f.a, "ROLLBACK AND CHAIN", true, 0)
			f.exec(t, f.a, "INSERT INTO t VALUES(4)", true, 1)
			f.exec(t, f.a, "END", false, 0)
			if backend.pg {
				f.rows(t, f.b, 1, 4, 77)
			} else {
				f.rows(t, f.b, 4, 99)
			}
		})
	}
}
