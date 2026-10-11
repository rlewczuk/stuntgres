package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSavepointsNativeReplacement(t *testing.T) {
	for _, replacement := range []string{"Reset", "Restore"} {
		t.Run(replacement, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					f := fixture.NewControlsReferenceFixture(t, p, backend.PG)
					ctx := p.Ctx
					f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
					f.Exec(t, f.B, "INSERT INTO t VALUES(77)", false, 1)
					f.Exec(t, f.A, "BEGIN", false, 0)
					f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
					f.Exec(t, f.A, "SAVEPOINT s", false, 0)
					f.Rows(t, f.A, 1, 77)
					f.Rows(t, f.B, 77)
					rows, err := f.A.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
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
						cloneRows, err := clone.QueryContext(ctx, "SELECT v FROM t")
						fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: cloneRows, Err: err}, false, 77)
						if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("SAVEPOINT s;")); err == nil {
							t.Fatal("Restore accepted controls")
						}
						f.Exec(t, f.A, "ROLLBACK TO s", false, 0)
						if replacement == "Reset" {
							err = stuntgres.Reset(ctx, p.Native)
						} else {
							err = stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE t(v INT NOT NULL);INSERT INTO t VALUES(99);"))
						}
						if err != nil {
							t.Fatal(err)
						}
						_, err = f.A.ExecContext(ctx, "ROLLBACK TO s")
						if err == nil || err.Error() != "stuntgres: catalog replaced before publication" {
							t.Fatal(err)
						}
						if _, ok := err.(interface{ SQLState() string }); ok {
							t.Fatal("ordinary replacement error exposes SQLState", err)
						}
					}
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, backend.PG, 1, 77)
					f.Exec(t, f.A, "ROLLBACK", false, 0)
					if backend.PG {
						f.Rows(t, f.B, 77)
					} else if replacement == "Restore" {
						f.Rows(t, f.B, 99)
					}
				})
			}
		})
	}
}

func TestReferenceSavepointsBorrowerPersistence(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		ctx := f.Pair.Ctx
		f.DB.SetMaxOpenConns(2)
		f.Exec(t, f.A, "BEGIN", false, 0)
		f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
		f.Exec(t, f.A, "SAVEPOINT s", false, 0)
		stmt, err := f.A.PrepareContext(ctx, "ROLLBACK TO s")
		if err != nil {
			t.Fatal(err)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		var original any
		if err := f.A.Raw(func(c any) error { original = c; return nil }); err != nil {
			t.Fatal(err)
		}
		if err := f.A.Close(); err != nil {
			t.Fatal(err)
		}
		borrowed, err := f.DB.Conn(ctx)
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
		f.Exec(t, borrowed, "INSERT INTO t VALUES(2)", false, 1)
		f.Exec(t, borrowed, "ROLLBACK TO s", false, 0)
		f.Rows(t, borrowed, 1)
		f.Exec(t, borrowed, "COMMIT", false, 0)
		f.Rows(t, f.B, 1)
	})
}
