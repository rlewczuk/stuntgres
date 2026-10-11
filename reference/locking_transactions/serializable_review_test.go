package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func serializableReviewReferenceScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func TestReferenceSerializableReviewOwnInsertDeleteRollback(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			serializableReviewReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
				for _, table := range []string{"p", "q"} {
					run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
				}
				run(a, fixture.Exec("CREATE TABLE snapshot_seed(v INT)", 0))
				run(a, fixture.Exec("INSERT INTO snapshot_seed VALUES(1)", 1))
				run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
				run(c, fixture.Select("SELECT v FROM snapshot_seed", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
				run(serializableReferenceHeld(t, p.Ctx, a, "SELECT v FROM p", held), fixture.TransactionRows(false, "SELECT v FROM p"))
				s := fixture.Exec("INSERT INTO q VALUES(1)", 1)
				s.Prepared = held
				run(a, s)
				run(a, fixture.Exec("SAVEPOINT s", 0))
				s = fixture.Exec("DELETE FROM q WHERE v=1", 1)
				s.Prepared = held
				run(a, s)
				run(a, fixture.Exec("ROLLBACK TO s", 0))
				run(a, fixture.Exec("COMMIT", 0))
				run(serializableReferenceHeld(t, p.Ctx, c, "SELECT v FROM q", held), fixture.TransactionRows(false, "SELECT v FROM q"))
				err := serializableReferenceWrite(t, p.Ctx, c, "INSERT INTO p VALUES(1)", held, 1)
				if err == nil {
					_, err = c.ExecContext(p.Ctx, "COMMIT")
					if err == nil {
						t.Fatal("both contradictory Serializable histories committed")
					}
					serializableReferenceFailure(t, err)
				}
				run(c, fixture.Exec("ROLLBACK", 0))
				run(a, fixture.TransactionRows(false, "SELECT v FROM p"))
				run(a, fixture.TransactionRows(false, "SELECT v FROM q", 1))
			})
		})
	}
}

func TestReferenceSerializableReviewGoDefaultDeferrable(t *testing.T) {
	serializableReviewReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { serializableReferenceRun(t, p, b, a, s) }
		run(fixture.Exec("SET default_transaction_isolation=serializable", 0))
		run(fixture.Exec("SET default_transaction_read_only=on", 0))
		for _, value := range []string{"on", "off"} {
			run(fixture.Exec("SET default_transaction_deferrable="+value, 0))
			for _, level := range []sql.IsolationLevel{sql.LevelDefault, sql.LevelSerializable} {
				tx, err := a.BeginTx(p.Ctx, &sql.TxOptions{Isolation: level, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for name, want := range map[string]string{"default_transaction_deferrable": value, "transaction_deferrable": value, "transaction_read_only": "on", "transaction_isolation": "serializable"} {
					serializableReferenceRun(t, p, b, tx, fixture.Select("SHOW "+name, []string{name}, []string{"TEXT"}, [][]any{{want}}))
				}
				fixture.State(t, tx.Rollback(), "")
			}
		}
	})
}

func TestReferenceSerializableReviewGoDefaultSafeWait(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		t.Run(map[bool]string{false: "query", true: "prepare"}[prepare], func(t *testing.T) {
			serializableReviewReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c, r := serializableReferenceWriters(t, p, b)
				run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
				run(r, fixture.Exec("SET default_transaction_deferrable=on", 0))
				run(r, fixture.Exec("SET statement_timeout='200ms'", 0))
				run(r, fixture.Exec("SET lock_timeout='1ms'", 0))
				tx, err := r.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				for _, name := range []string{"default_transaction_deferrable", "transaction_deferrable"} {
					run(tx, fixture.Select("SHOW "+name, []string{name}, []string{"TEXT"}, [][]any{{"on"}}))
				}
				want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
				if prepare {
					stmt, err := tx.PrepareContext(p.Ctx, "SELECT v FROM x")
					if stmt != nil {
						stmt.Close()
						t.Fatal("unsafe wait returned a prepared handle")
					}
					fixture.AssertDiagnostic(t, err, &want)
				} else {
					step := fixture.ErrorDiagnostic("SELECT v FROM x", want)
					step.SelectRows = true
					run(tx, step)
				}
				step := fixture.ErrorDiagnostic("SELECT v FROM y", fixture.TransactionAborted())
				step.SelectRows = true
				run(tx, step)
				fixture.State(t, tx.Rollback(), "")
				run(r, fixture.Exec("SET statement_timeout=0", 0))
				run(r, fixture.Exec("SET lock_timeout=0", 0))
				// Inherited OFF and explicit SQL NOT DEFERRABLE do not wait on
				// this same still-unresolved writer. Roll back their provisional
				// reads so the control does not add a surviving anomaly witness.
				run(r, fixture.Exec("SET default_transaction_deferrable=off", 0))
				control, err := r.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer control.Rollback()
				run(control, fixture.Select("SHOW transaction_deferrable", []string{"transaction_deferrable"}, []string{"TEXT"}, [][]any{{"off"}}))
				run(serializableReferenceHeld(t, p.Ctx, control, "SELECT v FROM x", prepare), fixture.TransactionRows(false, "SELECT v FROM x", 0))
				fixture.State(t, control.Rollback(), "")
				run(r, fixture.Exec("SET default_transaction_deferrable=on", 0))
				run(r, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY NOT DEFERRABLE", 0))
				run(r, fixture.Select("SHOW default_transaction_deferrable", []string{"default_transaction_deferrable"}, []string{"TEXT"}, [][]any{{"on"}}))
				run(r, fixture.Select("SHOW transaction_deferrable", []string{"transaction_deferrable"}, []string{"TEXT"}, [][]any{{"off"}}))
				run(serializableReferenceHeld(t, p.Ctx, r, "SELECT v FROM x", prepare), fixture.TransactionRows(false, "SELECT v FROM x", 0))
				run(r, fixture.Exec("ROLLBACK", 0))
				run(c, fixture.Exec("UPDATE x SET v=-11", 1))
				run(c, fixture.Exec("COMMIT", 0))
				tx, err = r.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				run(serializableReferenceHeld(t, p.Ctx, tx, "SELECT v FROM x", prepare), fixture.TransactionRows(false, "SELECT v FROM x", -11))
				run(tx, fixture.TransactionRows(false, "SELECT v FROM y", 20))
				fixture.State(t, tx.Commit(), "")
				run(a, fixture.TransactionRows(false, "SELECT v FROM x", -11))
			})
		})
	}
}

func TestReferenceSerializableReviewCatalogSequenceIndexAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			serializableReviewReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
				for _, table := range []string{"p", "q"} {
					run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
					run(a, fixture.Exec("INSERT INTO "+table+" VALUES(0)", 1))
				}
				run(a, fixture.Exec("CREATE TABLE indexed(v INT UNIQUE)", 0))
				run(a, fixture.Exec("INSERT INTO indexed VALUES(0)", 1))
				run(a, fixture.Exec("CREATE SEQUENCE existing_s", 0))
				run(a, fixture.Select("SELECT nextval('existing_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
				run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
				run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
				run(a, fixture.TransactionRows(false, "SELECT v FROM p", 0))
				run(c, fixture.TransactionRows(false, "SELECT v FROM q", 0))
				for _, s := range []fixture.Step{fixture.Exec("INSERT INTO indexed VALUES(7),(8)", 2), fixture.Exec("ALTER TABLE indexed ADD COLUMN marker INT DEFAULT 7", 0), fixture.Exec("CREATE SEQUENCE private_s START WITH 100", 0), fixture.Exec("ALTER SEQUENCE existing_s RESTART WITH 100", 0)} {
					s.Prepared = prepared
					run(c, s)
				}
				run(c, fixture.Select("SELECT v,marker FROM indexed ORDER BY v", []string{"v", "marker"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(7)}, {int64(7), int64(7)}, {int64(8), int64(7)}}))
				run(a, fixture.Exec("INSERT INTO q VALUES(1)", 1))
				run(c, fixture.Exec("INSERT INTO p VALUES(1)", 1))
				run(a, fixture.Exec("COMMIT", 0))
				want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
				_, err := c.ExecContext(p.Ctx, "COMMIT")
				fixture.AssertDiagnostic(t, err, &want)
				run(a, fixture.TransactionRows(false, "SELECT v FROM indexed", 0))
				missing := fixture.ControlsReferenceDiagnostic("42703", `column "marker" does not exist`)
				missing.Position = "8"
				step := fixture.ErrorDiagnostic("SELECT marker FROM indexed", missing)
				step.SelectRows = true
				run(a, step)
				missing = fixture.ControlsReferenceDiagnostic("42P01", `relation "private_s" does not exist`)
				missing.Position = "15"
				step = fixture.ErrorDiagnostic("SELECT * FROM private_s", missing)
				step.SelectRows = true
				run(a, step)
				run(a, fixture.Select("SELECT relname FROM pg_class WHERE relname='private_s'", []string{"relname"}, []string{"NAME"}, nil))
				run(a, fixture.Select("SELECT nextval('existing_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
				run(a, fixture.Exec("INSERT INTO indexed VALUES(7),(8)", 2))
				run(a, fixture.TransactionRows(false, "SELECT v FROM indexed ORDER BY v", 0, 7, 8))
				duplicate := fixture.ErrorDiagnostic("INSERT INTO indexed VALUES(0)", fixture.UniqueReferenceDuplicate("indexed", "indexed_v_key", "Key (v)=(0) already exists."))
				duplicate.Prepared = prepared
				run(a, duplicate)
				run(a, fixture.TransactionRows(false, "SELECT v FROM indexed ORDER BY v", 0, 7, 8))
				run(a, fixture.TransactionRows(false, "SELECT v FROM p", 0))
				run(a, fixture.TransactionRows(false, "SELECT v FROM q ORDER BY v", 0, 1))
			})
		})
	}
}

func TestReferenceSerializableReviewSequenceProviderExemptions(t *testing.T) {
	serializableReviewReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
		run(a, fixture.Exec("CREATE SEQUENCE exempt_s", 0))
		for _, table := range []string{"a_rows", "b_rows"} {
			run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
		}
		run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		for _, x := range []fixture.Executor{a, c} {
			run(x, fixture.Select("SELECT last_value,is_called FROM exempt_s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
			run(serializableReferenceHeld(t, p.Ctx, x, "SELECT relname FROM pg_class WHERE relname='exempt_s'", true), fixture.Select("SELECT relname FROM pg_class WHERE relname='exempt_s'", []string{"relname"}, []string{"NAME"}, [][]any{{"exempt_s"}}))
		}
		run(a, fixture.Select("SELECT nextval('exempt_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(c, fixture.Select("SELECT nextval('exempt_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(a, fixture.Exec("INSERT INTO a_rows VALUES(10)", 1))
		run(c, fixture.Prepared(fixture.Exec("INSERT INTO b_rows VALUES(20)", 1)))
		run(a, fixture.Exec("COMMIT", 0))
		run(c, fixture.Exec("COMMIT", 0))
		run(a, fixture.TransactionRows(false, "SELECT v FROM a_rows", 10))
		run(a, fixture.TransactionRows(false, "SELECT v FROM b_rows", 20))
		run(a, fixture.Select("SELECT last_value,is_called FROM exempt_s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
		run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(a, fixture.Select("SELECT nextval('exempt_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
		run(a, fixture.Exec("ROLLBACK", 0))
		run(c, fixture.Select("SELECT nextval('exempt_s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}))
	})
}
