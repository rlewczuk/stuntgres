package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCheckControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(prefix TEXT,a INT DEFAULT 1 CONSTRAINT c CHECK(a=1) NO INHERIT,b INT CONSTRAINT keep CHECK(b>0))", 0))
			run(referenceExec("INSERT INTO t VALUES('old',1,2),('null',NULL,3)", 2))
			if !b.pg {
				saved, err := b.db.QueryContext(p.ctx, "SELECT a FROM t ORDER BY a NULLS FIRST")
				if err != nil {
					t.Fatal(err)
				}
				defer saved.Close()
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(dump.String(), `CONSTRAINT "c" CHECK`) || !strings.Contains(dump.String(), "NO INHERIT") {
					t.Fatal(dump.String())
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				var again bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || again.String() != dump.String() {
					t.Fatal(err, again.String())
				}
				for _, script := range []string{"CREATE TABLE replacement(a INT CHECK(a))", "CREATE TABLE replacement(a INT CONSTRAINT c CHECK(a=1));INSERT INTO replacement VALUES(2)"} {
					if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
						t.Fatal("invalid Restore succeeded")
					}
					var after bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &after); err != nil || after.String() != dump.String() {
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
			run(referenceSelect("SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {int64(1)}}))
			run(referenceImplicitError("INSERT INTO t VALUES('bad',2,2)", referenceCheckViolation("t", "c", "bad, 2, 2")))
			run(referenceImplicitError("UPDATE t SET a=2 WHERE a=1", referenceCheckViolation("t", "c", "old, 2, 2")))
			// Mutate the same descriptor reconstructed and protected above.
			run(referenceExec("ALTER TABLE t RENAME TO u", 0))
			run(referenceExec("ALTER TABLE u RENAME a TO renamed", 0))
			run(referenceExec("ALTER TABLE u DROP prefix", 0))
			run(referenceImplicitError("INSERT INTO u VALUES(2,2)", referenceCheckViolation("u", "c", "2, 2")))
			run(referenceExec("ALTER TABLE u DROP renamed", 0))
			run(referenceImplicitError("INSERT INTO u VALUES(0)", referenceCheckViolation("u", "keep", "0")))
			run(referenceExec("ALTER TABLE u DROP CONSTRAINT keep", 0))
			run(referenceExec("INSERT INTO u VALUES(0)", 1))
			// Retain the reference-only one-column diagnostic/control vector.
			run(referenceExec("DROP TABLE u", 0))
			run(referenceExec("CREATE TABLE t(a INT DEFAULT 1 CONSTRAINT c CHECK(a=1) NO INHERIT)", 0))
			run(referenceExec("INSERT INTO t VALUES(1),(NULL)", 2))
			run(referenceImplicitError("INSERT INTO t VALUES(2)", referenceCheckViolation("t", "c", "2")))
			if !b.pg {
				dump := referenceCheckDump(t, p)
				if !bytes.Contains(dump, []byte(`CONSTRAINT "c" CHECK`)) || !bytes.Contains(dump, []byte("NO INHERIT")) {
					t.Fatal(string(dump))
				}
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
				referenceCheckRoundtrip(t, p, dump)
			}
			run(referenceSelect("SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {int64(1)}}))
			run(referenceImplicitError("UPDATE t SET a=2", referenceCheckViolation("t", "c", "2")))
		})
	}
}

func TestReferenceCheckLikeAndConstraintCollisions(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1))", 0))
			run(referenceExec("CREATE TABLE clone(LIKE t)", 0))
			run(referenceExec("INSERT INTO clone VALUES(2)", 1))
			run(referenceImplicitError("ALTER TABLE t ADD CONSTRAINT c NOT NULL a", referenceDiagnostic{Code: "42710", Message: `constraint "c" for relation "t" already exists`}))
			run(referenceExec("ALTER TABLE t DROP CONSTRAINT c", 0))
			run(referenceExec("INSERT INTO t VALUES(2)", 1))
		})
	}
}

func TestReferenceCheckExplicitTransactionAndSavepoint(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.ctx, "ROLLBACK")
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT CONSTRAINT c CHECK(a=1))", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			run(referenceExec("BEGIN", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			run(referenceImplicitError("INSERT INTO t VALUES(2)", referenceCheckViolation("t", "c", "2")))
			run(referenceImplicitError("SELECT * FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			run(referenceExec("ROLLBACK", 0))
			run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(referenceExec("BEGIN", 0))
			run(referenceExec("SAVEPOINT recover", 0))
			run(referencePrepared(referenceImplicitError("INSERT INTO t VALUES($1)", referenceCheckViolation("t", "c", "2"), 2)))
			run(referenceExec("ROLLBACK TO recover", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			run(referenceExec("COMMIT", 0))
			run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
		})
	}
}
