package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(prefix TEXT,a INT DEFAULT 1 CONSTRAINT c CHECK(a=1) NO INHERIT,b INT CONSTRAINT keep CHECK(b>0))", 0))
			run(fixture.Exec("INSERT INTO t VALUES('old',1,2),('null',NULL,3)", 2))
			if !b.PG {
				saved, err := b.DB.QueryContext(p.Ctx, "SELECT a FROM t ORDER BY a NULLS FIRST")
				if err != nil {
					t.Fatal(err)
				}
				defer saved.Close()
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(dump.String(), `CONSTRAINT "c" CHECK`) || !strings.Contains(dump.String(), "NO INHERIT") {
					t.Fatal(dump.String())
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				var again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
					t.Fatal(err, again.String())
				}
				for _, script := range []string{"CREATE TABLE replacement(a INT CHECK(a))", "CREATE TABLE replacement(a INT CONSTRAINT c CHECK(a=1));INSERT INTO replacement VALUES(2)"} {
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
						t.Fatal("invalid Restore succeeded")
					}
					var after bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || after.String() != dump.String() {
						t.Fatal("failed Restore changed target", err)
					}
				}
				for _, want := range []any{nil, int64(1)} {
					if !saved.Next() {
						t.Fatal(saved.Err())
					}
					var v any
					if err := saved.Scan(&v); err != nil || v != want {
						t.Fatal(v, err, want)
					}
				}
				if saved.Next() || saved.Err() != nil {
					t.Fatal("saved cursor", saved.Err())
				}
				if err := saved.Close(); err != nil {
					t.Fatal(err)
				}
			}
			run(fixture.Select("SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {int64(1)}}))
			run(fixture.ImplicitError("INSERT INTO t VALUES('bad',2,2)", fixture.CheckViolation("t", "c", "bad, 2, 2")))
			run(fixture.ImplicitError("UPDATE t SET a=2 WHERE a=1", fixture.CheckViolation("t", "c", "old, 2, 2")))
			// Mutate the same descriptor reconstructed and protected above.
			run(fixture.Exec("ALTER TABLE t RENAME TO u", 0))
			run(fixture.Exec("ALTER TABLE u RENAME a TO renamed", 0))
			run(fixture.Exec("ALTER TABLE u DROP prefix", 0))
			run(fixture.ImplicitError("INSERT INTO u VALUES(2,2)", fixture.CheckViolation("u", "c", "2, 2")))
			run(fixture.Exec("ALTER TABLE u DROP renamed", 0))
			run(fixture.ImplicitError("INSERT INTO u VALUES(0)", fixture.CheckViolation("u", "keep", "0")))
			run(fixture.Exec("ALTER TABLE u DROP CONSTRAINT keep", 0))
			run(fixture.Exec("INSERT INTO u VALUES(0)", 1))
			// Retain the reference-only one-column diagnostic/control vector.
			run(fixture.Exec("DROP TABLE u", 0))
			run(fixture.Exec("CREATE TABLE t(a INT DEFAULT 1 CONSTRAINT c CHECK(a=1) NO INHERIT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1),(NULL)", 2))
			run(fixture.ImplicitError("INSERT INTO t VALUES(2)", fixture.CheckViolation("t", "c", "2")))
			if !b.PG {
				dump := fixture.CheckDump(t, p)
				if !bytes.Contains(dump, []byte(`CONSTRAINT "c" CHECK`)) || !bytes.Contains(dump, []byte("NO INHERIT")) {
					t.Fatal(string(dump))
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.CheckRoundtrip(t, p, dump)
			}
			run(fixture.Select("SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {int64(1)}}))
			run(fixture.ImplicitError("UPDATE t SET a=2", fixture.CheckViolation("t", "c", "2")))
		})
	}
}

func TestReferenceCheckLikeAndConstraintCollisions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1))", 0))
			run(fixture.Exec("CREATE TABLE clone(LIKE t)", 0))
			run(fixture.Exec("INSERT INTO clone VALUES(2)", 1))
			run(fixture.ImplicitError("ALTER TABLE t ADD CONSTRAINT c NOT NULL a", fixture.Diagnostic{Code: "42710", Message: `constraint "c" for relation "t" already exists`}))
			run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT c", 0))
			run(fixture.Exec("INSERT INTO t VALUES(2)", 1))
		})
	}
}

func TestReferenceCheckExplicitTransactionAndSavepoint(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.ImplicitError("INSERT INTO t VALUES(2)", fixture.CheckViolation("t", "c", "2")))
			run(fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT recover", 0))
			run(fixture.Prepared(fixture.ImplicitError("INSERT INTO t VALUES($1)", fixture.CheckViolation("t", "c", "2"), 2)))
			run(fixture.Exec("ROLLBACK TO recover", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("COMMIT", 0))
			run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
		})
	}
}
