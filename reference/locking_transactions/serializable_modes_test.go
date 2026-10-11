package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSerializableModes(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { serializableReferenceRun(t, p, b, a, s) }
		for _, mode := range []string{"ISOLATION LEVEL SERIALIZABLE", "DEFERRABLE", "NOT DEFERRABLE", "ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE"} {
			run(fixture.Exec("BEGIN "+mode, 0))
			isolation, ro, def := "read committed", "off", "off"
			if mode == "ISOLATION LEVEL SERIALIZABLE" || mode == "ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE" {
				isolation = "serializable"
			}
			if mode == "DEFERRABLE" || mode == "ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE" {
				def = "on"
			}
			if mode == "ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE" {
				ro = "on"
			}
			referenceIsolationShow(t, a, "transaction_isolation", isolation)
			referenceIsolationShow(t, a, "transaction_read_only", ro)
			referenceIsolationShow(t, a, "transaction_deferrable", def)
			run(fixture.Exec("ROLLBACK", 0))
		}
		for _, route := range []string{"BEGIN ISOLATION LEVEL READ COMMITTED", "START TRANSACTION ISOLATION LEVEL SERIALIZABLE"} {
			run(fixture.Exec(route, 0))
			for _, mode := range []string{"SERIALIZABLE", "READ COMMITTED", "SERIALIZABLE", "SERIALIZABLE"} {
				run(fixture.Exec("SET TRANSACTION ISOLATION LEVEL "+mode, 0))
			}
			run(fixture.Exec("SET TRANSACTION READ ONLY, READ WRITE, DEFERRABLE, NOT DEFERRABLE, DEFERRABLE", 0))
			referenceIsolationShow(t, a, "transaction_isolation", "serializable")
			referenceIsolationShow(t, a, "transaction_read_only", "off")
			referenceIsolationShow(t, a, "transaction_deferrable", "on")
			run(fixture.Exec("ROLLBACK", 0))
		}
		tx, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		fixture.State(t, tx.Rollback(), "")
	})
}

func TestReferenceSerializableCharacteristics(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
		run(a, fixture.Exec("CREATE TABLE t(v INT)", 0))
		run(a, fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(a, fixture.Exec("SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0))
		for name, want := range map[string]string{"default_transaction_isolation": "serializable", "default_transaction_read_only": "on", "default_transaction_deferrable": "on"} {
			referenceIsolationShow(t, a, name, want)
		}
		for _, prepared := range []bool{false, true} {
			reader := fixture.Executor(a)
			if prepared {
				statement, err := a.PrepareContext(p.Ctx, "SHOW ALL")
				if err != nil {
					t.Fatal(err)
				}
				defer statement.Close()
				reader = fixture.HeldStatement{Executor: a, Statement: statement, SQL: "SHOW ALL"}
			}
			rows, err := reader.QueryContext(p.Ctx, "SHOW ALL")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || len(columns) != 3 || columns[0] != "name" || columns[1] != "setting" || columns[2] != "description" {
				t.Fatal(columns, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 3 {
				t.Fatal(types, err)
			}
			for _, column := range types {
				if column.DatabaseTypeName() != "TEXT" {
					t.Fatal(column)
				}
			}
			expected := map[string]string{
				"default_transaction_deferrable": "Sets the default deferrable status of new transactions.",
				"transaction_deferrable":         "Whether to defer a read-only serializable transaction until it can be executed with no possible serialization failures.",
			}
			seen := map[string]bool{}
			for rows.Next() {
				var name, value, description string
				if err := rows.Scan(&name, &value, &description); err != nil {
					t.Fatal(err)
				}
				if want, selected := expected[name]; selected {
					if seen[name] || value != "on" || description != want {
						t.Fatal(name, value, description, want)
					}
					seen[name] = true
				}
			}
			if err := rows.Err(); err != nil || len(seen) != len(expected) {
				t.Fatal("deferrable SHOW ALL completeness", seen, err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
		run(a, fixture.Exec("BEGIN", 0))
		for name, want := range map[string]string{"transaction_isolation": "serializable", "transaction_read_only": "on", "transaction_deferrable": "on"} {
			referenceIsolationShow(t, a, name, want)
		}
		run(a, fixture.Exec("ROLLBACK", 0))
		run(a, fixture.Exec("SET default_transaction_read_only=off", 0))
		run(a, fixture.Exec("SET default_transaction_deferrable=off", 0))
		for _, route := range []string{"SET TRANSACTION DEFERRABLE", "SET transaction_deferrable=on", "SET LOCAL transaction_deferrable=on"} {
			run(a, fixture.Exec("BEGIN", 0))
			run(a, fixture.Exec(route, 0))
			referenceIsolationShow(t, a, "transaction_deferrable", "on")
			run(a, fixture.Exec("ROLLBACK", 0))
			referenceIsolationShow(t, a, "transaction_deferrable", "off")
		}
		run(a, fixture.Exec("BEGIN", 0))
		s := fixture.ErrorDiagnostic("SELECT set_config('transaction_deferrable','on',true)", fixture.ControlsReferenceDiagnostic("25001", "SET TRANSACTION [NOT] DEFERRABLE must be called before any query"))
		s.SelectRows = true
		run(a, s)
		run(a, fixture.Exec("ROLLBACK", 0))
		referenceIsolationShow(t, a, "transaction_deferrable", "off")
		for _, mode := range []string{"DEFERRABLE", "NOT DEFERRABLE"} {
			run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
			run(a, fixture.TransactionRows(false, "SELECT v FROM t", 1))
			want := fixture.ControlsReferenceDiagnostic("25001", "SET TRANSACTION [NOT] DEFERRABLE must be called before any query")
			run(a, fixture.ErrorDiagnostic("SET TRANSACTION "+mode, want))
			run(a, fixture.Exec("ROLLBACK", 0))
			run(a, fixture.Exec("BEGIN", 0))
			run(a, fixture.Exec("SAVEPOINT s", 0))
			want = fixture.ControlsReferenceDiagnostic("25001", "SET TRANSACTION [NOT] DEFERRABLE cannot be called within a subtransaction")
			run(a, fixture.ErrorDiagnostic("SET TRANSACTION "+mode, want))
			run(a, fixture.Exec("ROLLBACK TO s", 0))
			run(a, fixture.Exec("ROLLBACK", 0))
		}
		run(a, fixture.ErrorDiagnostic("RESET transaction_deferrable", fixture.ControlsReferenceDiagnostic("0A000", "parameter \"transaction_deferrable\" cannot be reset")))
		run(a, fixture.Exec("SET default_transaction_deferrable=on", 0))
		run(a, fixture.Exec("RESET default_transaction_deferrable", 0))
		referenceIsolationShow(t, a, "default_transaction_deferrable", "off")
		run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE DEFERRABLE", 0))
		run(a, fixture.Exec("SAVEPOINT s", 0))
		run(a, fixture.Exec("SET LOCAL default_transaction_deferrable=on", 0))
		run(a, fixture.Exec("ROLLBACK TO s", 0))
		referenceIsolationShow(t, a, "default_transaction_deferrable", "off")
		referenceIsolationShow(t, a, "transaction_deferrable", "on")
		run(a, fixture.Exec("RESET ALL", 0))
		referenceIsolationShow(t, a, "transaction_deferrable", "on")
		run(a, fixture.Exec("ROLLBACK", 0))
		// Rejected post-snapshot changes do not silently detach the SSI owner.
		run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, fixture.TransactionRows(false, "SELECT v FROM t", 1))
		run(c, fixture.Exec("SET TRANSACTION READ ONLY", 0))
		run(c, fixture.ErrorDiagnostic("SET TRANSACTION READ WRITE", fixture.ControlsReferenceDiagnostic("25001", "transaction read-write mode must be set before any query")))
		run(c, fixture.Exec("ROLLBACK", 0))
	})
}

func TestReferenceSerializableVisibility(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
		for _, table := range []string{"t", "u", "e"} {
			run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
		}
		run(a, fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(a, fixture.Exec("INSERT INTO u VALUES(10)", 1))
		run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, fixture.Exec("INSERT INTO t VALUES(2)", 1))
		run(a, fixture.TransactionRows(false, "SELECT v FROM t ORDER BY v", 1, 2))
		run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, fixture.Exec("INSERT INTO t VALUES(3)", 1))
		run(c, fixture.Exec("UPDATE u SET v=20", 1))
		run(c, fixture.Exec("INSERT INTO e VALUES(30)", 1))
		run(c, fixture.Exec("COMMIT", 0))
		run(a, fixture.TransactionRows(false, "SELECT v FROM t ORDER BY v", 1, 2))
		run(a, fixture.TransactionRows(false, "SELECT v FROM u", 10))
		run(a, fixture.TransactionRows(false, "SELECT v FROM e"))
		run(a, fixture.Exec("INSERT INTO t VALUES(4)", 1))
		run(a, fixture.TransactionRows(false, "SELECT v FROM t ORDER BY v", 1, 2, 4))
		run(a, fixture.Exec("COMMIT", 0))
		run(c, fixture.TransactionRows(false, "SELECT v FROM t ORDER BY v", 1, 2, 3, 4))
	})
}

func TestReferenceSerializableDeferrableInactiveModes(t *testing.T) {
	for _, mode := range []string{"READ COMMITTED READ ONLY", "REPEATABLE READ READ ONLY", "SERIALIZABLE READ WRITE", "SERIALIZABLE READ ONLY"} {
		t.Run(mode, func(t *testing.T) {
			serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				_, c, r := serializableReferenceWriters(t, p, b)
				run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
				run(r, fixture.Exec("SET statement_timeout='200ms'", 0))
				run(r, fixture.Exec("BEGIN ISOLATION LEVEL "+mode+" DEFERRABLE", 0))
				if mode == "SERIALIZABLE READ ONLY" {
					run(r, fixture.Exec("SET TRANSACTION NOT DEFERRABLE", 0))
				}
				run(r, fixture.TransactionRows(false, "SELECT v FROM x", 0))
				run(r, fixture.Exec("ROLLBACK", 0))
				run(c, fixture.Exec("ROLLBACK", 0))
			})
		})
	}
}
