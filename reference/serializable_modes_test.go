package reference_test

import "testing"

func TestReferenceSerializableModes(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, _ := referenceLockConnections(t, p.ctx, b.db)
		run := func(s referenceStep) { serializableReferenceRun(t, p, b, a, s) }
		for _, mode := range []string{"ISOLATION LEVEL SERIALIZABLE", "DEFERRABLE", "NOT DEFERRABLE", "ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE"} {
			run(referenceExec("BEGIN "+mode, 0))
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
			run(referenceExec("ROLLBACK", 0))
		}
		for _, route := range []string{"BEGIN ISOLATION LEVEL READ COMMITTED", "START TRANSACTION ISOLATION LEVEL SERIALIZABLE"} {
			run(referenceExec(route, 0))
			for _, mode := range []string{"SERIALIZABLE", "READ COMMITTED", "SERIALIZABLE", "SERIALIZABLE"} {
				run(referenceExec("SET TRANSACTION ISOLATION LEVEL "+mode, 0))
			}
			run(referenceExec("SET TRANSACTION READ ONLY, READ WRITE, DEFERRABLE, NOT DEFERRABLE, DEFERRABLE", 0))
			referenceIsolationShow(t, a, "transaction_isolation", "serializable")
			referenceIsolationShow(t, a, "transaction_read_only", "off")
			referenceIsolationShow(t, a, "transaction_deferrable", "on")
			run(referenceExec("ROLLBACK", 0))
		}
		tx, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		referenceState(t, tx.Rollback(), "")
	})
}

func TestReferenceSerializableCharacteristics(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c := referenceLockConnections(t, p.ctx, b.db)
		run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
		run(a, referenceExec("CREATE TABLE t(v INT)", 0))
		run(a, referenceExec("INSERT INTO t VALUES(1)", 1))
		run(a, referenceExec("SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0))
		for name, want := range map[string]string{"default_transaction_isolation": "serializable", "default_transaction_read_only": "on", "default_transaction_deferrable": "on"} {
			referenceIsolationShow(t, a, name, want)
		}
		for _, prepared := range []bool{false, true} {
			reader := referenceExecutor(a)
			if prepared {
				statement, err := a.PrepareContext(p.ctx, "SHOW ALL")
				if err != nil {
					t.Fatal(err)
				}
				defer statement.Close()
				reader = referenceHeldStatement{referenceExecutor: a, statement: statement, query: "SHOW ALL"}
			}
			rows, err := reader.QueryContext(p.ctx, "SHOW ALL")
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
		run(a, referenceExec("BEGIN", 0))
		for name, want := range map[string]string{"transaction_isolation": "serializable", "transaction_read_only": "on", "transaction_deferrable": "on"} {
			referenceIsolationShow(t, a, name, want)
		}
		run(a, referenceExec("ROLLBACK", 0))
		run(a, referenceExec("SET default_transaction_read_only=off", 0))
		run(a, referenceExec("SET default_transaction_deferrable=off", 0))
		for _, route := range []string{"SET TRANSACTION DEFERRABLE", "SET transaction_deferrable=on", "SET LOCAL transaction_deferrable=on"} {
			run(a, referenceExec("BEGIN", 0))
			run(a, referenceExec(route, 0))
			referenceIsolationShow(t, a, "transaction_deferrable", "on")
			run(a, referenceExec("ROLLBACK", 0))
			referenceIsolationShow(t, a, "transaction_deferrable", "off")
		}
		run(a, referenceExec("BEGIN", 0))
		s := referenceErrorDiagnostic("SELECT set_config('transaction_deferrable','on',true)", controlsReferenceDiagnostic("25001", "SET TRANSACTION [NOT] DEFERRABLE must be called before any query"))
		s.selectRows = true
		run(a, s)
		run(a, referenceExec("ROLLBACK", 0))
		referenceIsolationShow(t, a, "transaction_deferrable", "off")
		for _, mode := range []string{"DEFERRABLE", "NOT DEFERRABLE"} {
			run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
			run(a, referenceTransactionRows(false, "SELECT v FROM t", 1))
			want := controlsReferenceDiagnostic("25001", "SET TRANSACTION [NOT] DEFERRABLE must be called before any query")
			run(a, referenceErrorDiagnostic("SET TRANSACTION "+mode, want))
			run(a, referenceExec("ROLLBACK", 0))
			run(a, referenceExec("BEGIN", 0))
			run(a, referenceExec("SAVEPOINT s", 0))
			want = controlsReferenceDiagnostic("25001", "SET TRANSACTION [NOT] DEFERRABLE cannot be called within a subtransaction")
			run(a, referenceErrorDiagnostic("SET TRANSACTION "+mode, want))
			run(a, referenceExec("ROLLBACK TO s", 0))
			run(a, referenceExec("ROLLBACK", 0))
		}
		run(a, referenceErrorDiagnostic("RESET transaction_deferrable", controlsReferenceDiagnostic("0A000", "parameter \"transaction_deferrable\" cannot be reset")))
		run(a, referenceExec("SET default_transaction_deferrable=on", 0))
		run(a, referenceExec("RESET default_transaction_deferrable", 0))
		referenceIsolationShow(t, a, "default_transaction_deferrable", "off")
		run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE DEFERRABLE", 0))
		run(a, referenceExec("SAVEPOINT s", 0))
		run(a, referenceExec("SET LOCAL default_transaction_deferrable=on", 0))
		run(a, referenceExec("ROLLBACK TO s", 0))
		referenceIsolationShow(t, a, "default_transaction_deferrable", "off")
		referenceIsolationShow(t, a, "transaction_deferrable", "on")
		run(a, referenceExec("RESET ALL", 0))
		referenceIsolationShow(t, a, "transaction_deferrable", "on")
		run(a, referenceExec("ROLLBACK", 0))
		// Rejected post-snapshot changes do not silently detach the SSI owner.
		run(c, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, referenceTransactionRows(false, "SELECT v FROM t", 1))
		run(c, referenceExec("SET TRANSACTION READ ONLY", 0))
		run(c, referenceErrorDiagnostic("SET TRANSACTION READ WRITE", controlsReferenceDiagnostic("25001", "transaction read-write mode must be set before any query")))
		run(c, referenceExec("ROLLBACK", 0))
	})
}

func TestReferenceSerializableVisibility(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c := referenceLockConnections(t, p.ctx, b.db)
		run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
		for _, table := range []string{"t", "u", "e"} {
			run(a, referenceExec("CREATE TABLE "+table+"(v INT)", 0))
		}
		run(a, referenceExec("INSERT INTO t VALUES(1)", 1))
		run(a, referenceExec("INSERT INTO u VALUES(10)", 1))
		run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, referenceExec("INSERT INTO t VALUES(2)", 1))
		run(a, referenceTransactionRows(false, "SELECT v FROM t ORDER BY v", 1, 2))
		run(c, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, referenceExec("INSERT INTO t VALUES(3)", 1))
		run(c, referenceExec("UPDATE u SET v=20", 1))
		run(c, referenceExec("INSERT INTO e VALUES(30)", 1))
		run(c, referenceExec("COMMIT", 0))
		run(a, referenceTransactionRows(false, "SELECT v FROM t ORDER BY v", 1, 2))
		run(a, referenceTransactionRows(false, "SELECT v FROM u", 10))
		run(a, referenceTransactionRows(false, "SELECT v FROM e"))
		run(a, referenceExec("INSERT INTO t VALUES(4)", 1))
		run(a, referenceTransactionRows(false, "SELECT v FROM t ORDER BY v", 1, 2, 4))
		run(a, referenceExec("COMMIT", 0))
		run(c, referenceTransactionRows(false, "SELECT v FROM t ORDER BY v", 1, 2, 3, 4))
	})
}

func TestReferenceSerializableDeferrableInactiveModes(t *testing.T) {
	for _, mode := range []string{"READ COMMITTED READ ONLY", "REPEATABLE READ READ ONLY", "SERIALIZABLE READ WRITE", "SERIALIZABLE READ ONLY"} {
		t.Run(mode, func(t *testing.T) {
			serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				_, c, r := serializableReferenceWriters(t, p, b)
				run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
				run(r, referenceExec("SET statement_timeout='200ms'", 0))
				run(r, referenceExec("BEGIN ISOLATION LEVEL "+mode+" DEFERRABLE", 0))
				if mode == "SERIALIZABLE READ ONLY" {
					run(r, referenceExec("SET TRANSACTION NOT DEFERRABLE", 0))
				}
				run(r, referenceTransactionRows(false, "SELECT v FROM x", 0))
				run(r, referenceExec("ROLLBACK", 0))
				run(c, referenceExec("ROLLBACK", 0))
			})
		})
	}
}
