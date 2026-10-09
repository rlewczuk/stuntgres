package reference_test

import (
	"database/sql"
	"testing"
)

func serializableReviewReferenceScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend)) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { body(t, p, backend) })
	}
}

func TestReferenceSerializableReviewOwnInsertDeleteRollback(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			serializableReviewReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c := referenceLockConnections(t, p.ctx, b.db)
				run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
				for _, table := range []string{"p", "q"} {
					run(a, referenceExec("CREATE TABLE "+table+"(v INT)", 0))
				}
				run(a, referenceExec("CREATE TABLE snapshot_seed(v INT)", 0))
				run(a, referenceExec("INSERT INTO snapshot_seed VALUES(1)", 1))
				run(c, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
				run(c, referenceSelect("SELECT v FROM snapshot_seed", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
				run(serializableReferenceHeld(t, p.ctx, a, "SELECT v FROM p", held), referenceTransactionRows(false, "SELECT v FROM p"))
				s := referenceExec("INSERT INTO q VALUES(1)", 1)
				s.prepared = held
				run(a, s)
				run(a, referenceExec("SAVEPOINT s", 0))
				s = referenceExec("DELETE FROM q WHERE v=1", 1)
				s.prepared = held
				run(a, s)
				run(a, referenceExec("ROLLBACK TO s", 0))
				run(a, referenceExec("COMMIT", 0))
				run(serializableReferenceHeld(t, p.ctx, c, "SELECT v FROM q", held), referenceTransactionRows(false, "SELECT v FROM q"))
				err := serializableReferenceWrite(t, p.ctx, c, "INSERT INTO p VALUES(1)", held, 1)
				if err == nil {
					_, err = c.ExecContext(p.ctx, "COMMIT")
					if err == nil {
						t.Fatal("both contradictory Serializable histories committed")
					}
					serializableReferenceFailure(t, err)
				}
				run(c, referenceExec("ROLLBACK", 0))
				run(a, referenceTransactionRows(false, "SELECT v FROM p"))
				run(a, referenceTransactionRows(false, "SELECT v FROM q", 1))
			})
		})
	}
}

func TestReferenceSerializableReviewGoDefaultDeferrable(t *testing.T) {
	serializableReviewReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, _ := referenceLockConnections(t, p.ctx, b.db)
		run := func(s referenceStep) { serializableReferenceRun(t, p, b, a, s) }
		run(referenceExec("SET default_transaction_isolation=serializable", 0))
		run(referenceExec("SET default_transaction_read_only=on", 0))
		for _, value := range []string{"on", "off"} {
			run(referenceExec("SET default_transaction_deferrable="+value, 0))
			for _, level := range []sql.IsolationLevel{sql.LevelDefault, sql.LevelSerializable} {
				tx, err := a.BeginTx(p.ctx, &sql.TxOptions{Isolation: level, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for name, want := range map[string]string{"default_transaction_deferrable": value, "transaction_deferrable": value, "transaction_read_only": "on", "transaction_isolation": "serializable"} {
					serializableReferenceRun(t, p, b, tx, referenceSelect("SHOW "+name, []string{name}, []string{"TEXT"}, [][]any{{want}}))
				}
				referenceState(t, tx.Rollback(), "")
			}
		}
	})
}

func TestReferenceSerializableReviewGoDefaultSafeWait(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		t.Run(map[bool]string{false: "query", true: "prepare"}[prepare], func(t *testing.T) {
			serializableReviewReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c, r := serializableReferenceWriters(t, p, b)
				run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
				run(r, referenceExec("SET default_transaction_deferrable=on", 0))
				run(r, referenceExec("SET statement_timeout='200ms'", 0))
				run(r, referenceExec("SET lock_timeout='1ms'", 0))
				tx, err := r.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for _, name := range []string{"default_transaction_deferrable", "transaction_deferrable"} {
					run(tx, referenceSelect("SHOW "+name, []string{name}, []string{"TEXT"}, [][]any{{"on"}}))
				}
				want := controlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
				if prepare {
					stmt, err := tx.PrepareContext(p.ctx, "SELECT v FROM x")
					if stmt != nil {
						stmt.Close()
						t.Fatal("unsafe wait returned a prepared handle")
					}
					referenceAssertDiagnostic(t, err, &want)
				} else {
					step := referenceErrorDiagnostic("SELECT v FROM x", want)
					step.selectRows = true
					run(tx, step)
				}
				step := referenceErrorDiagnostic("SELECT v FROM y", referenceTransactionAborted())
				step.selectRows = true
				run(tx, step)
				referenceState(t, tx.Rollback(), "")
				run(r, referenceExec("SET statement_timeout=0", 0))
				run(r, referenceExec("SET lock_timeout=0", 0))
				// Inherited OFF and explicit SQL NOT DEFERRABLE do not wait on
				// this same still-unresolved writer. Roll back their provisional
				// reads so the control does not add a surviving anomaly witness.
				run(r, referenceExec("SET default_transaction_deferrable=off", 0))
				control, err := r.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer control.Rollback()
				run(control, referenceSelect("SHOW transaction_deferrable", []string{"transaction_deferrable"}, []string{"TEXT"}, [][]any{{"off"}}))
				run(serializableReferenceHeld(t, p.ctx, control, "SELECT v FROM x", prepare), referenceTransactionRows(false, "SELECT v FROM x", 0))
				referenceState(t, control.Rollback(), "")
				run(r, referenceExec("SET default_transaction_deferrable=on", 0))
				run(r, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY NOT DEFERRABLE", 0))
				run(r, referenceSelect("SHOW default_transaction_deferrable", []string{"default_transaction_deferrable"}, []string{"TEXT"}, [][]any{{"on"}}))
				run(r, referenceSelect("SHOW transaction_deferrable", []string{"transaction_deferrable"}, []string{"TEXT"}, [][]any{{"off"}}))
				run(serializableReferenceHeld(t, p.ctx, r, "SELECT v FROM x", prepare), referenceTransactionRows(false, "SELECT v FROM x", 0))
				run(r, referenceExec("ROLLBACK", 0))
				run(c, referenceExec("UPDATE x SET v=-11", 1))
				run(c, referenceExec("COMMIT", 0))
				tx, err = r.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				run(serializableReferenceHeld(t, p.ctx, tx, "SELECT v FROM x", prepare), referenceTransactionRows(false, "SELECT v FROM x", -11))
				run(tx, referenceTransactionRows(false, "SELECT v FROM y", 20))
				referenceState(t, tx.Commit(), "")
				run(a, referenceTransactionRows(false, "SELECT v FROM x", -11))
			})
		})
	}
}

func TestReferenceSerializableReviewCatalogSequenceIndexAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			serializableReviewReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c := referenceLockConnections(t, p.ctx, b.db)
				run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
				for _, table := range []string{"p", "q"} {
					run(a, referenceExec("CREATE TABLE "+table+"(v INT)", 0))
					run(a, referenceExec("INSERT INTO "+table+" VALUES(0)", 1))
				}
				run(a, referenceExec("CREATE TABLE indexed(v INT UNIQUE)", 0))
				run(a, referenceExec("INSERT INTO indexed VALUES(0)", 1))
				run(a, referenceExec("CREATE SEQUENCE existing_s", 0))
				run(a, referenceSelect("SELECT nextval('existing_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
				run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
				run(c, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
				run(a, referenceTransactionRows(false, "SELECT v FROM p", 0))
				run(c, referenceTransactionRows(false, "SELECT v FROM q", 0))
				for _, s := range []referenceStep{referenceExec("INSERT INTO indexed VALUES(7),(8)", 2), referenceExec("ALTER TABLE indexed ADD COLUMN marker INT DEFAULT 7", 0), referenceExec("CREATE SEQUENCE private_s START WITH 100", 0), referenceExec("ALTER SEQUENCE existing_s RESTART WITH 100", 0)} {
					s.prepared = prepared
					run(c, s)
				}
				run(c, referenceSelect("SELECT v,marker FROM indexed ORDER BY v", []string{"v", "marker"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(7)}, {int64(7), int64(7)}, {int64(8), int64(7)}}))
				run(a, referenceExec("INSERT INTO q VALUES(1)", 1))
				run(c, referenceExec("INSERT INTO p VALUES(1)", 1))
				run(a, referenceExec("COMMIT", 0))
				want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
				_, err := c.ExecContext(p.ctx, "COMMIT")
				referenceAssertDiagnostic(t, err, &want)
				run(a, referenceTransactionRows(false, "SELECT v FROM indexed", 0))
				missing := controlsReferenceDiagnostic("42703", `column "marker" does not exist`)
				missing.Position = "8"
				step := referenceErrorDiagnostic("SELECT marker FROM indexed", missing)
				step.selectRows = true
				run(a, step)
				missing = controlsReferenceDiagnostic("42P01", `relation "private_s" does not exist`)
				missing.Position = "15"
				step = referenceErrorDiagnostic("SELECT * FROM private_s", missing)
				step.selectRows = true
				run(a, step)
				run(a, referenceSelect("SELECT relname FROM pg_class WHERE relname='private_s'", []string{"relname"}, []string{"NAME"}, nil))
				run(a, referenceSelect("SELECT nextval('existing_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
				run(a, referenceExec("INSERT INTO indexed VALUES(7),(8)", 2))
				run(a, referenceTransactionRows(false, "SELECT v FROM indexed ORDER BY v", 0, 7, 8))
				duplicate := referenceErrorDiagnostic("INSERT INTO indexed VALUES(0)", uniqueReferenceDuplicate("indexed", "indexed_v_key", "Key (v)=(0) already exists."))
				duplicate.prepared = prepared
				run(a, duplicate)
				run(a, referenceTransactionRows(false, "SELECT v FROM indexed ORDER BY v", 0, 7, 8))
				run(a, referenceTransactionRows(false, "SELECT v FROM p", 0))
				run(a, referenceTransactionRows(false, "SELECT v FROM q ORDER BY v", 0, 1))
			})
		})
	}
}

func TestReferenceSerializableReviewSequenceProviderExemptions(t *testing.T) {
	serializableReviewReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c := referenceLockConnections(t, p.ctx, b.db)
		run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
		run(a, referenceExec("CREATE SEQUENCE exempt_s", 0))
		for _, table := range []string{"a_rows", "b_rows"} {
			run(a, referenceExec("CREATE TABLE "+table+"(v INT)", 0))
		}
		run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		for _, x := range []referenceExecutor{a, c} {
			run(x, referenceSelect("SELECT last_value,is_called FROM exempt_s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
			run(serializableReferenceHeld(t, p.ctx, x, "SELECT relname FROM pg_class WHERE relname='exempt_s'", true), referenceSelect("SELECT relname FROM pg_class WHERE relname='exempt_s'", []string{"relname"}, []string{"NAME"}, [][]any{{"exempt_s"}}))
		}
		run(a, referenceSelect("SELECT nextval('exempt_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(c, referenceSelect("SELECT nextval('exempt_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(a, referenceExec("INSERT INTO a_rows VALUES(10)", 1))
		run(c, referencePrepared(referenceExec("INSERT INTO b_rows VALUES(20)", 1)))
		run(a, referenceExec("COMMIT", 0))
		run(c, referenceExec("COMMIT", 0))
		run(a, referenceTransactionRows(false, "SELECT v FROM a_rows", 10))
		run(a, referenceTransactionRows(false, "SELECT v FROM b_rows", 20))
		run(a, referenceSelect("SELECT last_value,is_called FROM exempt_s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
		run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(a, referenceSelect("SELECT nextval('exempt_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(a, referenceExec("ROLLBACK", 0))
		run(c, referenceSelect("SELECT nextval('exempt_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}))
	})
}
