package reference_test

import (
	"context"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePGAuthIDDelegation(t *testing.T) {
	pgUserScenario(t, []string{"pga_delegate", "pga_delegate_target", "pga_delegate_owner"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, name := range []string{"pga_delegate", "pga_delegate_target", "pga_delegate_owner"} {
			pgUserCreate(t, p, b, name)
		}
		if b.PG {
			// Catalog ACLs outlive public-schema reset. Return body handles
			// before removing only this scenario's catalog dependencies.
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cleanup, err := b.DB.Conn(ctx)
				if err != nil {
					t.Error("pg_authid cleanup connection", err)
					return
				}
				defer cleanup.Close()
				for _, query := range []string{
					"RESET SESSION AUTHORIZATION",
					"DROP OWNED BY pga_delegate,pga_delegate_target,pga_delegate_owner CASCADE",
				} {
					if _, err := cleanup.ExecContext(ctx, query); err != nil {
						t.Error("pg_authid cleanup", err)
					}
				}
			})
		}
		password := pgShadowMD5
		pgShadowSetPassword(t, p, b, "pga_delegate_target", &password)
		// The PostgreSQL fixture recreates public without default PUBLIC USAGE.
		// Both callers and delegated owners must resolve the stored view chain.
		if b.PG {
			pgUserExec(t, p, b, "GRANT USAGE ON SCHEMA public TO pga_delegate,pga_delegate_owner")
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		q := "SELECT rolpassword FROM pg_authid WHERE rolname='pga_delegate_target'"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		run(fixture.Exec("SET ROLE pga_delegate", 0))
		run(pgAuthIDDenied(q, false))
		run(fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "GRANT pg_read_all_data TO pga_delegate")
		run(fixture.Exec("SET ROLE pga_delegate", 0))
		for _, prepared := range []bool{false, true} {
			pgAuthIDPassword(t, p, conn, prepared, "pga_delegate_target", &password)
		}
		rows, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("held delegated row missing")
		}
		var secret string
		if err := rows.Scan(&secret); err != nil || secret != password {
			t.Fatal("held delegated credential mismatch")
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("held delegated cardinality")
		}
		rows.Close()
		run(fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "GRANT pg_read_all_data TO pga_delegate WITH INHERIT FALSE")
		run(fixture.Exec("SET ROLE pga_delegate", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgAuthIDDenied(q, false))
		run(fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "GRANT pg_read_all_data TO pga_delegate WITH INHERIT TRUE")
		run(fixture.Exec("SET ROLE pga_delegate", 0))
		pgAuthIDPassword(t, p, held, false, "pga_delegate_target", &password)
		run(fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "REVOKE pg_read_all_data FROM pga_delegate")
		run(fixture.Exec("SET ROLE pga_delegate", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgAuthIDDenied(q, false))
		for _, prepared := range []bool{false, true} {
			run(pgAuthIDDenied(q, prepared))
		}
		run(fixture.Exec("RESET ROLE", 0))
		for _, sql := range []string{
			"CREATE VIEW pga_definer AS SELECT rolpassword FROM pg_catalog.pg_authid WHERE rolname='pga_delegate_target'",
			"CREATE VIEW pga_nested AS SELECT rolpassword FROM pga_definer",
			"CREATE VIEW pga_invoker WITH (security_invoker=true) AS SELECT rolpassword FROM pg_catalog.pg_authid WHERE rolname='pga_delegate_target'",
			"GRANT SELECT ON pga_definer,pga_nested,pga_invoker TO pga_delegate,pga_delegate_owner",
		} {
			pgUserExec(t, p, b, sql)
		}
		run(fixture.Exec("SET ROLE pga_delegate", 0))
		for _, prepared := range []bool{false, true} {
			pgShadowViewSecret(t, p, conn, "SELECT rolpassword FROM pga_definer", password, prepared)
			pgShadowViewSecret(t, p, conn, "SELECT rolpassword FROM pga_nested", password, prepared)
			run(pgAuthIDDenied("SELECT rolpassword FROM pga_invoker", prepared))
			run(pgAuthIDDenied(q, prepared))
		}
		run(fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "ALTER VIEW pga_definer OWNER TO pga_delegate_owner")
		run(fixture.Exec("SET ROLE pga_delegate", 0))
		for _, prepared := range []bool{false, true} {
			run(pgAuthIDDenied("SELECT rolpassword FROM pga_nested", prepared))
		}
		run(fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "GRANT pg_read_all_data TO pga_delegate_owner")
		run(fixture.Exec("SET ROLE pga_delegate", 0))
		for _, prepared := range []bool{false, true} {
			pgShadowViewSecret(t, p, conn, "SELECT rolpassword FROM pga_nested", password, prepared)
			run(pgAuthIDDenied(q, prepared))
		}
		run(fixture.Exec("RESET ROLE", 0))
		// Preserve the real transaction REVOKE/schema rollback witness.
		// Shared autocommit GRANT consumers below exercise both backends.
		if b.PG {
			tx, err := conn.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(p.Ctx, "GRANT SELECT(rolname) ON pg_catalog.pg_authid TO pga_delegate"); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(p.Ctx, "SET LOCAL ROLE pga_delegate"); err != nil {
				t.Fatal(err)
			}
			for _, prepared := range []bool{false, true} {
				step := fixture.Select("SELECT rolname FROM pg_authid WHERE rolname='pga_delegate_target'", []string{"rolname"}, []string{"NAME"}, [][]any{{"pga_delegate_target"}})
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, step)
			}
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT 1 AS n FROM pg_authid WHERE FALSE", []string{"n"}, []string{"INT4"}, [][]any{}))
			for _, query := range []string{"SELECT rolpassword FROM pg_authid WHERE FALSE", "SELECT rolname FROM pg_authid WHERE rolpassword IS NOT NULL", "SELECT rolname FROM pg_authid ORDER BY rolpassword"} {
				if _, err := tx.ExecContext(p.Ctx, "SAVEPOINT denied_column"); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, pgAuthIDDenied(query, false))
				if _, err := tx.ExecContext(p.Ctx, "ROLLBACK TO denied_column"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tx.ExecContext(p.Ctx, "RESET ROLE; GRANT SELECT ON pg_catalog.pg_authid TO pga_delegate; SET LOCAL ROLE pga_delegate"); err != nil {
				t.Fatal(err)
			}
			pgAuthIDPassword(t, p, tx, true, "pga_delegate_target", &password)
			credential, err := tx.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("delegated retained prepare failed")
			}
			defer credential.Close()
			pgAuthIDPassword(t, p, fixture.HeldStatement{Executor: tx, Statement: credential, SQL: q}, false, "pga_delegate_target", &password)
			if _, err := tx.ExecContext(p.Ctx, "RESET ROLE; REVOKE SELECT ON pg_catalog.pg_authid FROM pga_delegate; REVOKE SELECT(rolname) ON pg_catalog.pg_authid FROM pga_delegate; SET LOCAL ROLE pga_delegate; SAVEPOINT revoked"); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: tx, Statement: credential, SQL: q}, b.Recorder, pgAuthIDDenied(q, false))
			if _, err := tx.ExecContext(p.Ctx, "ROLLBACK TO revoked; RESET ROLE; GRANT SELECT ON pg_catalog.pg_authid TO pga_delegate; REVOKE USAGE ON SCHEMA pg_catalog FROM PUBLIC; SET LOCAL ROLE pga_delegate; SAVEPOINT schema_denied"); err != nil {
				t.Fatal(err)
			}
			s := fixture.ImplicitError("SELECT rolname FROM pg_catalog.pg_authid", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pg_catalog", Position: "21"})
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
			if _, err := tx.ExecContext(p.Ctx, "ROLLBACK TO schema_denied; RESET ROLE; GRANT USAGE ON SCHEMA pg_catalog TO pga_delegate; SET LOCAL ROLE pga_delegate"); err != nil {
				t.Fatal(err)
			}
			pgAuthIDPassword(t, p, tx, false, "pga_delegate_target", &password)
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
		nameQuery := "SELECT rolname FROM pg_authid WHERE rolname='pga_delegate_target'"
		nameStmt, err := conn.PrepareContext(p.Ctx, nameQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer nameStmt.Close()
		heldName := fixture.HeldStatement{Executor: conn, Statement: nameStmt, SQL: nameQuery}
		nameRows := fixture.Select(nameQuery, []string{"rolname"}, []string{"NAME"}, [][]any{{"pga_delegate_target"}})
		passwordRows := fixture.Select(q, []string{"rolpassword"}, []string{"TEXT"}, [][]any{{password}})
		for _, role := range []string{"pga_delegate", "pga_delegate_target"} {
			run(fixture.Exec("SET ROLE "+role, 0))
			fixture.RunStep(t, p.Ctx, heldName, b.Recorder, pgAuthIDDenied(nameQuery, false))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgAuthIDDenied(q, false))
			run(fixture.Exec("RESET ROLE", 0))
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("GRANT SELECT(rolname) ON pg_catalog.pg_authid TO pga_delegate WITH GRANT OPTION", 0)
			s.Prepared = prepared
			run(s)
			run(fixture.Exec("SET ROLE pga_delegate", 0))
			s = nameRows
			s.Prepared = prepared
			run(s)
			fixture.RunStep(t, p.Ctx, heldName, b.Recorder, nameRows)
			s = fixture.Select("SELECT 1 AS n FROM pg_authid WHERE FALSE", []string{"n"}, []string{"INT4"}, nil)
			s.Prepared = prepared
			run(s)
			for _, query := range []string{q, "SELECT rolpassword FROM pg_authid WHERE FALSE", "SELECT rolname FROM pg_authid WHERE rolpassword IS NOT NULL", "SELECT rolname FROM pg_authid ORDER BY rolpassword"} {
				run(pgAuthIDDenied(query, prepared))
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgAuthIDDenied(q, false))
			s = fixture.Exec("GRANT SELECT(rolname) ON pg_catalog.pg_authid TO pga_delegate_target", 0)
			s.Prepared = prepared
			run(s)
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET ROLE pga_delegate_target", 0))
			s = nameRows
			s.Prepared = prepared
			run(s)
			fixture.RunStep(t, p.Ctx, heldName, b.Recorder, nameRows)
			run(pgAuthIDDenied(q, prepared))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgAuthIDDenied(q, false))
			run(fixture.Exec("RESET ROLE", 0))
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("GRANT SELECT ON pg_catalog.pg_authid TO pga_delegate WITH GRANT OPTION", 0)
			s.Prepared = prepared
			run(s)
			run(fixture.Exec("SET ROLE pga_delegate", 0))
			s = passwordRows
			s.Prepared = prepared
			run(s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, passwordRows)
			s = fixture.Exec("GRANT SELECT ON pg_catalog.pg_authid TO pga_delegate_target", 0)
			s.Prepared = prepared
			run(s)
			// Source permissions are independent in both directions: reading
			// pg_authid does not grant access to the protected pg_shadow view.
			run(pgShadowDenied("SELECT passwd FROM pg_catalog.pg_shadow WHERE FALSE", prepared))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET ROLE pga_delegate_target", 0))
			s = passwordRows
			s.Prepared = prepared
			run(s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, passwordRows)
			run(pgShadowDenied("SELECT passwd FROM pg_catalog.pg_shadow WHERE FALSE", prepared))
			run(fixture.Exec("RESET ROLE", 0))
		}
		if b.PG {
			pgUserExec(t, p, b, "REVOKE USAGE ON SCHEMA public FROM pga_delegate,pga_delegate_owner")
		}
		for _, sql := range []string{"DROP VIEW pga_nested", "DROP VIEW pga_definer", "DROP VIEW pga_invoker", "REVOKE pg_read_all_data FROM pga_delegate_owner"} {
			pgUserExec(t, p, b, sql)
		}
	})
}
