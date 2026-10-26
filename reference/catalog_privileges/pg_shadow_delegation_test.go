package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pgShadowViewSecret(t *testing.T, p *fixture.Pair, conn pgShadowQuerier, query, want string, prepared bool, args ...any) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := conn.PrepareContext(p.Ctx, query); if e != nil { t.Fatal("delegated prepare failed") }; defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx, args...)
	} else { rows, err = conn.QueryContext(p.Ctx, query, args...) }
	if err != nil { t.Fatal("delegated query failed", err) }; defer rows.Close()
	if !rows.Next() { t.Fatal("delegated credential missing") }
	var got string
	if err := rows.Scan(&got); err != nil || got != want { t.Fatal("delegated credential mismatch") }
	if rows.Next() || rows.Err() != nil { t.Fatal("delegated credential cardinality") }
}

func TestReferencePGShadowDelegation(t *testing.T) {
	pgUserScenario(t, []string{"pgs_delegate", "pgs_delegate_target", "pgs_delegate_owner"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, name := range []string{"pgs_delegate", "pgs_delegate_target", "pgs_delegate_owner"} { pgUserCreate(t, p, b, name) }
		password := pgShadowMD5; pgShadowSetPassword(t, p, b, "pgs_delegate_target", &password)
		// The PostgreSQL fixture recreates public without default PUBLIC USAGE.
		// Both callers and delegated owners must resolve the stored view chain.
		if b.PG { pgUserExec(t, p, b, "GRANT USAGE ON SCHEMA public TO pgs_delegate,pgs_delegate_owner") }
		conn, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		q := "SELECT passwd FROM pg_shadow WHERE usename='pgs_delegate_target'"
		stmt, err := conn.PrepareContext(p.Ctx, q); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		run(fixture.Exec("SET ROLE pgs_delegate", 0))
		run(pgShadowDenied(q, false))
		run(fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "GRANT pg_read_all_data TO pgs_delegate")
		run(fixture.Exec("SET ROLE pgs_delegate", 0))
		for _, prepared := range []bool{false, true} { pgShadowPassword(t, p, conn, prepared, "pgs_delegate_target", &password) }
		rows, err := stmt.QueryContext(p.Ctx); if err != nil { t.Fatal(err) }
		if !rows.Next() { t.Fatal("held delegated row missing") }; var secret string
		if err := rows.Scan(&secret); err != nil || secret != password { t.Fatal("held delegated credential mismatch") }; if rows.Next() || rows.Err() != nil { t.Fatal("held delegated cardinality") }; rows.Close()
		run(fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "REVOKE pg_read_all_data FROM pgs_delegate")
		run(fixture.Exec("SET ROLE pgs_delegate", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgShadowDenied(q, false))
		for _, prepared := range []bool{false, true} { run(pgShadowDenied(q, prepared)) }
		run(fixture.Exec("RESET ROLE", 0))
		for _, sql := range []string{
			"CREATE VIEW pgs_definer AS SELECT passwd FROM pg_catalog.pg_shadow WHERE usename='pgs_delegate_target'",
			"CREATE VIEW pgs_nested AS SELECT passwd FROM pgs_definer",
			"CREATE VIEW pgs_invoker WITH (security_invoker=true) AS SELECT passwd FROM pg_catalog.pg_shadow WHERE usename='pgs_delegate_target'",
			"GRANT SELECT ON pgs_definer,pgs_nested,pgs_invoker TO pgs_delegate,pgs_delegate_owner",
		} { pgUserExec(t, p, b, sql) }
		run(fixture.Exec("SET ROLE pgs_delegate", 0))
		for _, prepared := range []bool{false, true} {
			pgShadowViewSecret(t, p, conn, "SELECT passwd FROM pgs_definer", password, prepared)
			pgShadowViewSecret(t, p, conn, "SELECT passwd FROM pgs_nested", password, prepared)
			run(pgShadowDenied("SELECT passwd FROM pgs_invoker", prepared))
			run(pgShadowDenied(q, prepared))
		}
		run(fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "ALTER VIEW pgs_definer OWNER TO pgs_delegate_owner")
		run(fixture.Exec("SET ROLE pgs_delegate", 0))
		for _, prepared := range []bool{false, true} { run(pgShadowDenied("SELECT passwd FROM pgs_nested", prepared)) }
		run(fixture.Exec("RESET ROLE", 0))
		pgUserExec(t, p, b, "GRANT pg_read_all_data TO pgs_delegate_owner")
		run(fixture.Exec("SET ROLE pgs_delegate", 0))
		for _, prepared := range []bool{false, true} { pgShadowViewSecret(t, p, conn, "SELECT passwd FROM pgs_nested", password, prepared); run(pgShadowDenied(q, prepared)) }
		run(fixture.Exec("RESET ROLE", 0))
		// Catalog SQL grants remain outside native SQL admission. The real oracle
		// alone supplies explicit relation/column grants, rolled back completely;
		// native component tests exercise SetObjectACL with those same authorities.
		if b.PG {
			tx, err := conn.BeginTx(p.Ctx, nil); if err != nil { t.Fatal(err) }; defer tx.Rollback()
			if _, err := tx.ExecContext(p.Ctx, "GRANT SELECT(passwd) ON pg_catalog.pg_shadow TO pgs_delegate"); err != nil { t.Fatal(err) }
			if _, err := tx.ExecContext(p.Ctx, "SET LOCAL ROLE pgs_delegate"); err != nil { t.Fatal(err) }
			// A passwd-only grant cannot authorize a usename predicate.
			for _, prepared := range []bool{false, true} { pgShadowViewSecret(t, p, tx, "SELECT passwd FROM pg_shadow WHERE passwd=$1", password, prepared, password) }
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT 1 AS n FROM pg_shadow WHERE FALSE", []string{"n"}, []string{"INT4"}, [][]any{}))
			if _, err := tx.ExecContext(p.Ctx, "SAVEPOINT denied_column"); err != nil { t.Fatal(err) }
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, pgShadowDenied("SELECT usename FROM pg_shadow WHERE FALSE", false))
			if _, err := tx.ExecContext(p.Ctx, "ROLLBACK TO denied_column"); err != nil { t.Fatal(err) }
			if _, err := tx.ExecContext(p.Ctx, "RESET ROLE; GRANT SELECT ON pg_catalog.pg_shadow TO pgs_delegate; SET LOCAL ROLE pgs_delegate"); err != nil { t.Fatal(err) }
			pgShadowPassword(t, p, tx, true, "pgs_delegate_target", &password)
			if err := tx.Rollback(); err != nil { t.Fatal(err) }
		}
		if !b.PG {
			for _, q := range []string{"GRANT SELECT ON pg_catalog.pg_shadow TO pgs_delegate", "GRANT SELECT(passwd) ON pg_catalog.pg_shadow TO pgs_delegate"} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "statement is not supported"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
				}
			}
		}
		if b.PG { pgUserExec(t, p, b, "REVOKE USAGE ON SCHEMA public FROM pgs_delegate,pgs_delegate_owner") }
		for _, sql := range []string{"DROP VIEW pgs_nested", "DROP VIEW pgs_definer", "DROP VIEW pgs_invoker", "REVOKE pg_read_all_data FROM pgs_delegate_owner", "DROP ROLE pgs_delegate", "DROP ROLE pgs_delegate_owner", "DROP ROLE pgs_delegate_target"} { pgUserExec(t, p, b, sql) }
	})
}
