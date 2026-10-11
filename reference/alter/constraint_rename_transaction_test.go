package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConstraintRenameTransactions(t *testing.T) {
	for _, k := range constraintRenameKinds {
		t.Run(k.kind, func(t *testing.T) {
			constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t("+k.declaration+")", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
				before := constraintRenameCapture(t, p, c, "old", k.indexed)
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				q := "ALTER TABLE t RENAME CONSTRAINT old TO new"
				stmt, err := tx.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
				private := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
				constraintRenameNames(private, "new", k)
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				if constraintRenameCapture(t, p, c, "old", k.indexed) != before {
					t.Fatal("rollback changed group")
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='new'", []string{"relname"}, []string{"NAME"}, nil))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec(q, 0))
				run(fixture.Exec("SAVEPOINT s", 0))
				run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT new TO later", 0))
				constraintRenameNames(run, "later", k)
				run(fixture.Exec("ROLLBACK TO s", 0))
				constraintRenameNames(run, "new", k)
				run(fixture.Exec("SAVEPOINT collision", 0))
				code, message := "42710", `constraint "new" for relation "t" already exists`
				if k.indexed {
					code, message = "42P07", `relation "new" already exists`
				}
				run(fixture.ErrorDiagnostic("ALTER TABLE t RENAME CONSTRAINT new TO new", constraintRenameError(code, message)))
				run(fixture.Exec("ROLLBACK TO collision", 0))
				constraintRenameNames(run, "new", k)
				run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT new TO old", 0))
				run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT old TO new", 0))
				run(fixture.Exec("COMMIT", 0))
				if constraintRenameCapture(t, p, c, "new", k.indexed) != before {
					t.Fatal("commit replaced identity")
				}
				for _, duplicate := range []bool{false, true} {
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT new TO later", 0))
					constraintRenameNames(run, "later", k)
					if duplicate {
						q, d := constraintRenameViolation(k, "later")
						run(fixture.ErrorDiagnostic(q, d))
					} else {
						run(fixture.ErrorDiagnostic("ALTER TABLE t RENAME CONSTRAINT missing TO unused", constraintRenameMissing("missing")))
					}
					run(fixture.ErrorDiagnostic("SELECT k,payload FROM t", fixture.TransactionAborted()))
					run(fixture.Exec("ROLLBACK", 0))
					if constraintRenameCapture(t, p, c, "new", k.indexed) != before {
						t.Fatal("failed later statement published rename")
					}
					run(fixture.Select("SELECT k,payload FROM t", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
				}
			})
		})
	}
}

func TestReferenceConstraintRenameDeferredExclusion(t *testing.T) {
	constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT,CONSTRAINT old EXCLUDE(k WITH =) DEFERRABLE)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		before := constraintRenameCapture(t, p, c, "old", true)
		for _, commit := range []bool{false, true} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET CONSTRAINTS old DEFERRED", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT old TO new", 0))
			if commit {
				run(fixture.ErrorDiagnostic("COMMIT", fixture.ExclusionReferenceConflict("t", "new", "k", "1", "1")))
			} else {
				run(fixture.ErrorDiagnostic("SET CONSTRAINTS new IMMEDIATE", fixture.ExclusionReferenceConflict("t", "new", "k", "1", "1")))
				run(fixture.Exec("ROLLBACK", 0))
			}
			if constraintRenameCapture(t, p, c, "old", true) != before {
				t.Fatal("deferred failure published rename")
			}
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET CONSTRAINTS old DEFERRED", 0))
		run(fixture.Exec("SAVEPOINT s", 0))
		run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT old TO new", 0))
		run(fixture.ErrorDiagnostic("SET CONSTRAINTS old IMMEDIATE", constraintRenameError("42704", `constraint "old" does not exist`)))
		run(fixture.Exec("ROLLBACK TO s", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(fixture.Exec("ROLLBACK TO s", 0))
		run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT old TO new", 0))
		run(fixture.Exec("INSERT INTO t VALUES(2)", 1))
		run(fixture.Exec("COMMIT", 0))
		if constraintRenameCapture(t, p, c, "new", true) != before {
			t.Fatal("deferred successful rename lost identity")
		}
		run(fixture.Select("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	})
}
