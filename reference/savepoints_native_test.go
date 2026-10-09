package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceSavepointsNativeReplacement(t *testing.T) {
	for _, replacement := range []string{"Reset", "Restore"} {
		t.Run(replacement, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					f := newControlsReferenceFixture(t, p, backend.pg)
					ctx := p.ctx
					f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
					f.exec(t, f.b, "INSERT INTO t VALUES(77)", false, 1)
					f.exec(t, f.a, "BEGIN", false, 0)
					f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
					f.exec(t, f.a, "SAVEPOINT s", false, 0)
					f.rows(t, f.a, 1, 77)
					f.rows(t, f.b, 77)
					rows, err := f.a.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
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
						cloneRows, err := clone.QueryContext(ctx, "SELECT v FROM t")
						referenceLimitLockValues(t, relationOutcome{rows: cloneRows, err: err}, false, 77)
						if err := stuntgres.Restore(ctx, p.native, strings.NewReader("SAVEPOINT s;")); err == nil {
							t.Fatal("Restore accepted controls")
						}
						f.exec(t, f.a, "ROLLBACK TO s", false, 0)
						if replacement == "Reset" {
							err = stuntgres.Reset(ctx, p.native)
						} else {
							err = stuntgres.Restore(ctx, p.native, strings.NewReader("CREATE TABLE t(v INT NOT NULL);INSERT INTO t VALUES(99);"))
						}
						if err != nil {
							t.Fatal(err)
						}
						_, err = f.a.ExecContext(ctx, "ROLLBACK TO s")
						if err == nil || err.Error() != "stuntgres: catalog replaced before publication" {
							t.Fatal(err)
						}
						if _, ok := err.(interface{ SQLState() string }); ok {
							t.Fatal("ordinary replacement error exposes SQLState", err)
						}
					}
					referenceLimitLockValues(t, relationOutcome{rows: rows}, backend.pg, 1, 77)
					f.exec(t, f.a, "ROLLBACK", false, 0)
					if backend.pg {
						f.rows(t, f.b, 77)
					} else if replacement == "Restore" {
						f.rows(t, f.b, 99)
					}
				})
			}
		})
	}
}

func TestReferenceSavepointsBorrowerPersistence(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		ctx := f.pair.ctx
		f.db.SetMaxOpenConns(2)
		f.exec(t, f.a, "BEGIN", false, 0)
		f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
		f.exec(t, f.a, "SAVEPOINT s", false, 0)
		stmt, err := f.a.PrepareContext(ctx, "ROLLBACK TO s")
		if err != nil {
			t.Fatal(err)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		var original any
		if err := f.a.Raw(func(c any) error { original = c; return nil }); err != nil {
			t.Fatal(err)
		}
		if err := f.a.Close(); err != nil {
			t.Fatal(err)
		}
		borrowed, err := f.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer borrowed.Close()
		defer borrowed.ExecContext(ctx, "ROLLBACK")
		if err := borrowed.Raw(func(c any) error {
			if c != original {
				t.Fatal("borrower changed physical handle")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		f.exec(t, borrowed, "INSERT INTO t VALUES(2)", false, 1)
		f.exec(t, borrowed, "ROLLBACK TO s", false, 0)
		f.rows(t, borrowed, 1)
		f.exec(t, borrowed, "COMMIT", false, 0)
		f.rows(t, f.b, 1)
	})
}
