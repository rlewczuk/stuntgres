package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type transactionControlFailingReader struct{ err error }

func (r transactionControlFailingReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceTransactionControlsNativeRoundTrip(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			f := fixture.NewControlsReferenceFixture(t, p, backend.PG)
			ctx := p.Ctx
			f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
			f.Exec(t, f.B, "INSERT INTO t VALUES(77)", false, 1)
			f.Exec(t, f.A, "BEGIN", false, 0)
			f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
			f.Rows(t, f.A, 1, 77)
			f.Rows(t, f.B, 77)
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
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
				fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, false, 77)
				for _, script := range []string{"BEGIN;CREATE TABLE x(v INT);COMMIT;", "ROLLBACK;", "CREATE TABLE x(v INT);INSERT INTO x VALUES('bad');"} {
					if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(script)); err == nil {
						t.Fatal("restore accepted invalid script", script)
					}
					f.Rows(t, f.B, 77)
				}
				readErr := errors.New("transaction controls reader failure")
				if err := stuntgres.Restore(ctx, p.Native, transactionControlFailingReader{readErr}); !errors.Is(err, readErr) {
					t.Fatal(err)
				}
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if err := stuntgres.Restore(canceled, p.Native, strings.NewReader("CREATE TABLE x(v INT)")); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				f.Rows(t, f.A, 1, 77)
			}
			f.Exec(t, f.A, "COMMIT", false, 0)
			f.Rows(t, f.B, 1, 77)
			f.Exec(t, f.A, "BEGIN", false, 0)
			f.Exec(t, f.A, "INSERT INTO t VALUES(3)", false, 1)
			if !backend.PG {
				if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE t(v INT NOT NULL);INSERT INTO t VALUES(99);")); err != nil {
					t.Fatal(err)
				}
			}
			f.Exec(t, f.A, "ROLLBACK AND CHAIN", true, 0)
			f.Exec(t, f.A, "INSERT INTO t VALUES(4)", true, 1)
			f.Exec(t, f.A, "END", false, 0)
			if backend.PG {
				f.Rows(t, f.B, 1, 4, 77)
			} else {
				f.Rows(t, f.B, 4, 99)
			}
		})
	}
}
