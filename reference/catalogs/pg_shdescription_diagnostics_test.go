package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGShDescriptionVisibilityAndRoleErrors(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgsd_error_role", "pgsd_error_reader"})
	fixture.SchemaReferenceCleanup(t, "pgsd_errors")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(q string) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 0))
			}
			run("CREATE SCHEMA pgsd_errors")
			id := pgShDescriptionCreateRole(t, p, backend, "pgsd_error_role", "pgsd_errors")
			run("CREATE ROLE pgsd_error_reader")
			run("COMMENT ON ROLE pgsd_error_role IS 'unchanged'")
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(context.Background(), "RESET ROLE")
			query := "COMMENT ON ROLE pgsd_error_role IS 'held forbidden'"
			stmt, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET ROLE pgsd_error_reader", 0))
			pgShDescriptionCheck(t, p, backend, conn, id, new("unchanged"))
			for _, target := range []string{"pgsd_error_reader", "pgsd_error_role", "postgres"} {
				detail := "The current user must have the CREATEROLE attribute."
				if target == "postgres" {
					detail = "The current user must have the SUPERUSER attribute."
				}
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.RoleReferenceError("COMMENT ON ROLE "+target+" IS 'forbidden'", "42501", "permission denied", detail, prepared, "execute"))
					pgShDescriptionCheck(t, p, backend, conn, id, new("unchanged"))
				}
			}
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.RoleReferenceError(query, "42501", "permission denied", "The current user must have the CREATEROLE attribute.", false, "execute"))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("RESET ROLE", 0))
			for _, name := range []string{"pgsd_missing_role", "public"} {
				for _, prepared := range []bool{false, true} {
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.RoleReferenceError("COMMENT ON ROLE "+name+" IS NULL", "42704", "role \""+name+"\" does not exist", "", prepared, "execute"))
					pgShDescriptionCheck(t, p, backend, conn, id, new("unchanged"))
				}
			}
			run("DROP ROLE pgsd_error_role")
			run("DROP ROLE pgsd_error_reader")
		})
	}
}

func TestReferencePGShDescriptionDiagnosticsAndTransactions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgsd_tx_role"})
	fixture.SchemaReferenceCleanup(t, "pgsd_tx")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, tc := range []struct{ query, code, message, position string }{
				{"SELECT oid FROM pg_catalog.pg_shdescription", "42703", "column \"oid\" does not exist", "8"},
				{"SELECT objsubid FROM pg_catalog.pg_shdescription", "42703", "column \"objsubid\" does not exist", "8"},
				{"SELECT /*é*/ oid FROM pg_catalog.pg_shdescription", "42703", "column \"oid\" does not exist", "14"},
				{"SELECT 'bad'::oid FROM pg_catalog.pg_shdescription", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					if tc.query == "SELECT objsubid FROM pg_catalog.pg_shdescription" {
						s.Diagnostic.Hint = "Perhaps you meant to reference the column \"pg_shdescription.objoid\"."
					}
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_shdescription WHERE objoid=$1", fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type oid: \"bad\"", Context: "unnamed portal parameter $1 = '...'"}, "bad")
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
			query := "SELECT * FROM pg_catalog.pg_shdescription WHERE FALSE"
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := backend.DB.QueryContext(canceled, query)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			stmt, err := backend.DB.PrepareContext(canceled, query)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled prepare published statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			stmt, err = backend.DB.PrepareContext(p.Ctx, query)
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
			fixture.PgDescriptionRows(t, p, backend, pgShDescriptionSelect(query, [][]any{}))
			run := func(q string) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec(q, 0))
			}
			run("CREATE SCHEMA pgsd_tx")
			id := pgShDescriptionCreateRole(t, p, backend, "pgsd_tx_role", "pgsd_tx")
			run("COMMENT ON ROLE pgsd_tx_role IS 'original'")
			run("CREATE TABLE pgsd_tx.sentinel(a INT)")
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO pgsd_tx.sentinel VALUES(7)", 1))
			if !backend.PG {
				for _, tc := range []struct{ query, stage string }{
					{"INSERT INTO pg_catalog.pg_shdescription VALUES(1,1260,'forbidden')", "execute"},
					{"UPDATE pg_catalog.pg_shdescription SET description='forbidden'", "prepare"},
					{"DELETE FROM pg_catalog.pg_shdescription", "execute"},
				} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
						s.Prepared, s.ErrorStage = prepared, "execute"
						if prepared {
							s.ErrorStage = tc.stage
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
						pgShDescriptionCheck(t, p, backend, backend.DB, id, new("original"))
					}
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(query+" FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
			for _, prepared := range []bool{false, true} {
				tx, err := backend.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer tx.Rollback()
					pgShDescriptionCheck(t, p, backend, tx, id, new("original"))
					fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("INSERT INTO pgsd_tx.sentinel VALUES(8)", 1))
					s := fixture.Exec("COMMENT ON ROLE pgsd_tx_role IS 'transactional'", 0)
					s.Prepared = prepared
					if !backend.PG {
						s = fixture.RoleReferenceError(s.Query, "0A000", "statement is unsupported in an explicit transaction", "", prepared, "prepare")
					}
					fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s)
					if backend.PG {
						pgShDescriptionCheck(t, p, backend, tx, id, new("transactional"))
					} else {
						fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.RoleReferenceError("SELECT * FROM pgsd_tx.sentinel", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", false, "execute"))
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}()
				pgShDescriptionCheck(t, p, backend, backend.DB, id, new("original"))
			}
			if backend.PG {
				run("COMMENT ON ROLE pgsd_tx_role IS NULL")
				tx, err := backend.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec("COMMENT ON ROLE pgsd_tx_role IS 'added'", 0))
				pgShDescriptionCheck(t, p, backend, tx, id, new("added"))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				pgShDescriptionCheck(t, p, backend, backend.DB, id, nil)
				tx, err = backend.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Prepared(fixture.Exec("COMMENT ON ROLE pgsd_tx_role IS 'committed'", 0)))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				pgShDescriptionCheck(t, p, backend, backend.DB, id, new("committed"))
			}
			tx, err := backend.DB.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.RoleReferenceError("COMMENT ON ROLE pgsd_tx_role IS 'readonly'", "25006", "cannot execute COMMENT in a read-only transaction", "", false, "execute"))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if !backend.PG {
				tx, err = backend.DB.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.RoleReferenceError("COMMENT ON ROLE pgsd_tx_role IS 'readonly'", "0A000", "statement is unsupported in an explicit transaction", "", true, "prepare"))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
			}
			text := "original"
			if backend.PG {
				text = "committed"
			}
			pgShDescriptionCheck(t, p, backend, backend.DB, id, new(text))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT a FROM pgsd_tx.sentinel", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run("DROP ROLE pgsd_tx_role")
		})
	}
}
