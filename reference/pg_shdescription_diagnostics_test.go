package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestReferencePGShDescriptionVisibilityAndRoleErrors(t *testing.T) {
	roleReferenceCleanup(t, []string{"pgsd_error_role", "pgsd_error_reader"})
	schemaReferenceCleanup(t, "pgsd_errors")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(q string) {
				t.Helper()
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 0))
			}
			run("CREATE SCHEMA pgsd_errors")
			id := pgShDescriptionCreateRole(t, p, backend, "pgsd_error_role", "pgsd_errors")
			run("CREATE ROLE pgsd_error_reader")
			run("COMMENT ON ROLE pgsd_error_role IS 'unchanged'")
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET ROLE")
			query := "COMMENT ON ROLE pgsd_error_role IS 'held forbidden'"
			stmt, err := conn.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: query}
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("SET ROLE pgsd_error_reader", 0))
			pgShDescriptionCheck(t, p, backend, conn, id, new("unchanged"))
			for _, target := range []string{"pgsd_error_reader", "pgsd_error_role", "postgres"} {
				detail := "The current user must have the CREATEROLE attribute."
				if target == "postgres" {
					detail = "The current user must have the SUPERUSER attribute."
				}
				for _, prepared := range []bool{false, true} {
					referenceRunStep(t, p.ctx, conn, backend.recorder, roleReferenceError("COMMENT ON ROLE "+target+" IS 'forbidden'", "42501", "permission denied", detail, prepared, "execute"))
					pgShDescriptionCheck(t, p, backend, conn, id, new("unchanged"))
				}
			}
			referenceRunStep(t, p.ctx, held, backend.recorder, roleReferenceError(query, "42501", "permission denied", "The current user must have the CREATEROLE attribute.", false, "execute"))
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("RESET ROLE", 0))
			for _, name := range []string{"pgsd_missing_role", "public"} {
				for _, prepared := range []bool{false, true} {
					referenceRunStep(t, p.ctx, conn, backend.recorder, roleReferenceError("COMMENT ON ROLE "+name+" IS NULL", "42704", "role \""+name+"\" does not exist", "", prepared, "execute"))
					pgShDescriptionCheck(t, p, backend, conn, id, new("unchanged"))
				}
			}
			run("DROP ROLE pgsd_error_role")
			run("DROP ROLE pgsd_error_reader")
		})
	}
}

func TestReferencePGShDescriptionDiagnosticsAndTransactions(t *testing.T) {
	roleReferenceCleanup(t, []string{"pgsd_tx_role"})
	schemaReferenceCleanup(t, "pgsd_tx")
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, tc := range []struct{ query, code, message, position string }{
				{"SELECT oid FROM pg_catalog.pg_shdescription", "42703", "column \"oid\" does not exist", "8"},
				{"SELECT objsubid FROM pg_catalog.pg_shdescription", "42703", "column \"objsubid\" does not exist", "8"},
				{"SELECT /*é*/ oid FROM pg_catalog.pg_shdescription", "42703", "column \"oid\" does not exist", "14"},
				{"SELECT 'bad'::oid FROM pg_catalog.pg_shdescription", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
			} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					if tc.query == "SELECT objsubid FROM pg_catalog.pg_shdescription" {
						s.diagnostic.Hint = "Perhaps you meant to reference the column \"pg_shdescription.objoid\"."
					}
					s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("SELECT * FROM pg_catalog.pg_shdescription WHERE objoid=$1", referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type oid: \"bad\"", Context: "unnamed portal parameter $1 = '...'"}, "bad")
				s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			query := "SELECT * FROM pg_catalog.pg_shdescription WHERE FALSE"
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := backend.db.QueryContext(canceled, query)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			stmt, err := backend.db.PrepareContext(canceled, query)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled prepare published statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			stmt, err = backend.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err = stmt.QueryContext(canceled)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled held query published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			pgDescriptionRows(t, p, backend, pgShDescriptionSelect(query, [][]any{}))
			run := func(q string) {
				t.Helper()
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec(q, 0))
			}
			run("CREATE SCHEMA pgsd_tx")
			id := pgShDescriptionCreateRole(t, p, backend, "pgsd_tx_role", "pgsd_tx")
			run("COMMENT ON ROLE pgsd_tx_role IS 'original'")
			run("CREATE TABLE pgsd_tx.sentinel(a INT)")
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO pgsd_tx.sentinel VALUES(7)", 1))
			if !backend.pg {
				for _, tc := range []struct{ query, stage string }{
					{"INSERT INTO pg_catalog.pg_shdescription VALUES(1,1260,'forbidden')", "execute"},
					{"UPDATE pg_catalog.pg_shdescription SET description='forbidden'", "prepare"},
					{"DELETE FROM pg_catalog.pg_shdescription", "execute"},
				} {
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError(tc.query, referenceDiagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
						s.prepared, s.errorStage = prepared, "execute"
						if prepared {
							s.errorStage = tc.stage
						}
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
						pgShDescriptionCheck(t, p, backend, backend.db, id, new("original"))
					}
				}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(query+" FOR UPDATE", referenceDiagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
					s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
			for _, prepared := range []bool{false, true} {
				tx, err := backend.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer tx.Rollback()
					pgShDescriptionCheck(t, p, backend, tx, id, new("original"))
					referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("INSERT INTO pgsd_tx.sentinel VALUES(8)", 1))
					s := referenceExec("COMMENT ON ROLE pgsd_tx_role IS 'transactional'", 0)
					s.prepared = prepared
					if !backend.pg {
						s = roleReferenceError(s.query, "0A000", "statement is unsupported in an explicit transaction", "", prepared, "prepare")
					}
					referenceRunStep(t, p.ctx, tx, backend.recorder, s)
					if backend.pg {
						pgShDescriptionCheck(t, p, backend, tx, id, new("transactional"))
					} else {
						referenceRunStep(t, p.ctx, tx, backend.recorder, roleReferenceError("SELECT * FROM pgsd_tx.sentinel", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", false, "execute"))
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}()
				pgShDescriptionCheck(t, p, backend, backend.db, id, new("original"))
			}
			if backend.pg {
				run("COMMENT ON ROLE pgsd_tx_role IS NULL")
				tx, err := backend.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec("COMMENT ON ROLE pgsd_tx_role IS 'added'", 0))
				pgShDescriptionCheck(t, p, backend, tx, id, new("added"))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				pgShDescriptionCheck(t, p, backend, backend.db, id, nil)
				tx, err = backend.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceRunStep(t, p.ctx, tx, backend.recorder, referencePrepared(referenceExec("COMMENT ON ROLE pgsd_tx_role IS 'committed'", 0)))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				pgShDescriptionCheck(t, p, backend, backend.db, id, new("committed"))
			}
			tx, err := backend.db.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, backend.recorder, roleReferenceError("COMMENT ON ROLE pgsd_tx_role IS 'readonly'", "25006", "cannot execute COMMENT in a read-only transaction", "", false, "execute"))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if !backend.pg {
				tx, err = backend.db.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				referenceRunStep(t, p.ctx, tx, backend.recorder, roleReferenceError("COMMENT ON ROLE pgsd_tx_role IS 'readonly'", "0A000", "statement is unsupported in an explicit transaction", "", true, "prepare"))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			text := "original"
			if backend.pg {
				text = "committed"
			}
			pgShDescriptionCheck(t, p, backend, backend.db, id, new(text))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT a FROM pgsd_tx.sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run("DROP ROLE pgsd_tx_role")
		})
	}
}
