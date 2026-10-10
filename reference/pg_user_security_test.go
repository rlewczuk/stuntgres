package reference_test

import (
	"database/sql"
	"testing"
	"time"
)

func pgUserRetainedExpiry(t *testing.T, rows *sql.Rows, want string) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || len(names) != 1 || names[0] != "expiry" {
		t.Fatal("retained expiry columns", names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "TEXT" {
		t.Fatal("retained expiry metadata", meta, err)
	}
	if !rows.Next() {
		t.Fatal("missing retained expiry", rows.Err())
	}
	var text any
	if err := rows.Scan(&text); err != nil || text != want {
		t.Fatal("retained expiry", text, want, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained expiry cardinality", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferencePGUserSecurity(t *testing.T) {
	names := []string{"pgu_target", "pgu_reader", "pgu_group", "pgu_privileged", "pgu_user_default", "pgu_role_default"}
	pgUserScenario(t, names, func(t *testing.T, p *referencePair, b referenceBackend) {
		target := pgUserCreate(t, p, b, "pgu_target")
		pgUserCreate(t, p, b, "pgu_reader")
		group := pgShDescriptionCreateRole(t, p, b, "pgu_group", "public")
		privileged := pgUserCreate(t, p, b, "pgu_privileged")
		for _, q := range []string{"CREATE ROLE pgu_role_default", "CREATE USER pgu_user_default", "ALTER ROLE pgu_group CREATEDB SUPERUSER REPLICATION BYPASSRLS", "GRANT pgu_group TO pgu_target", "ALTER ROLE pgu_privileged CREATEDB SUPERUSER REPLICATION BYPASSRLS", "ALTER ROLE pgu_target CREATEROLE NOINHERIT CONNECTION LIMIT 0 VALID UNTIL '-infinity'"} {
			pgUserExec(t, p, b, q)
		}
		pgUserBoth(t, p, b, referenceSelect("SELECT usename FROM pg_user WHERE usename='pgu_role_default' OR usename='pgu_user_default' ORDER BY usename", []string{"usename"}, []string{"NAME"}, [][]any{{"pgu_user_default"}}))
		pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{}, group))
		row := pgUserRow("pgu_privileged", privileged, nil)
		for i := 2; i <= 5; i++ {
			row[i] = true
		}
		pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{row}, privileged))
		// Credentials never enter the recorder, query dump, or error diagnostics.
		// The statement text intentionally stays out of failure output.
		for _, passwordSQL := range []string{"NULL", "'pgu-first-private-password'", "'pgu-replacement-private-password'"} {
			if _, err := b.db.ExecContext(p.ctx, "ALTER ROLE pgu_target PASSWORD "+passwordSQL); err != nil {
				t.Fatal("password fixture setup failed")
			}
			for _, ordinary := range []bool{false, true} {
				conn, err := b.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer conn.Close()
					if ordinary {
						if _, err := conn.ExecContext(p.ctx, "SET ROLE pgu_reader"); err != nil {
							t.Fatal(err)
						}
						defer func() {
							if _, err := conn.ExecContext(p.ctx, "RESET ROLE"); err != nil {
								t.Error(err)
							}
						}()
					}
					for _, prepared := range []bool{false, true} {
						s := referenceSelect("SELECT usename,usesysid,usecreatedb,usesuper,userepl,usebypassrls,passwd,valuntil::text AS expiry,useconfig FROM pg_catalog.pg_user WHERE usesysid=$1", []string{"usename", "usesysid", "usecreatedb", "usesuper", "userepl", "usebypassrls", "passwd", "expiry", "useconfig"}, []string{"NAME", "OID", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "_TEXT"}, [][]any{{"pgu_target", target, false, false, false, false, "********", "-infinity", nil}}, target)
						s.prepared = prepared
						pgDescriptionExecutorRows(t, p, b, conn, s)
					}
				}()
			}
		}
		pgUserExec(t, p, b, "ALTER ROLE pgu_target NOLOGIN")
		pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{}, target))
		pgUserExec(t, p, b, "ALTER ROLE pgu_target LOGIN")
		pgUserExec(t, p, b, "ALTER ROLE pgu_group LOGIN")
		pgUserBoth(t, p, b, referenceSelect("SELECT usename FROM pg_user WHERE usesysid=$1", []string{"usename"}, []string{"NAME"}, [][]any{{"pgu_group"}}, group))
		for _, name := range names {
			pgUserExec(t, p, b, "DROP ROLE "+name)
		}
	})
}

func TestReferencePGUserTimeValues(t *testing.T) {
	pgUserScenario(t, []string{"pgu_time"}, func(t *testing.T, p *referencePair, b referenceBackend) {
		id := pgUserCreate(t, p, b, "pgu_time")
		pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{pgUserRow("pgu_time", id, nil)}, id))
		pgUserExec(t, p, b, "ALTER ROLE pgu_time VALID UNTIL '2030-01-02 03:04:05+00'")
		instant := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{pgUserRow("pgu_time", id, instant)}, id))
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		query := "SELECT valuntil::text AS expiry FROM pg_user WHERE usesysid=$1"
		stmt, err := conn.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: query}
		for _, tc := range []struct{ zone, text string }{{"UTC", "2030-01-02 03:04:05+00"}, {"America/New_York", "2030-01-01 22:04:05-05"}} {
			referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
			s := referenceSelect(query, []string{"expiry"}, []string{"TEXT"}, [][]any{{tc.text}}, id)
			referenceRunStep(t, p.ctx, held, b.recorder, s)
		}
		old, err := stmt.QueryContext(p.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		// pq cannot issue another command while this connection has unread rows.
		// Native rows are detached and additionally witness a same-session change.
		if b.pg {
			pgUserRetainedExpiry(t, old, "2030-01-01 22:04:05-05")
		}
		referenceRunStep(t, p.ctx, conn, b.recorder, referenceExec("SET TIME ZONE 'UTC'", 0))
		if !b.pg {
			pgUserRetainedExpiry(t, old, "2030-01-01 22:04:05-05")
		}
		if _, err := conn.ExecContext(p.ctx, "RESET TIME ZONE"); err != nil {
			t.Fatal(err)
		}
		for _, expiry := range []string{"infinity", "-infinity", "2000-01-01 00:00:00+00"} {
			pgUserExec(t, p, b, "ALTER ROLE pgu_time VALID UNTIL '"+expiry+"'")
			pgUserBoth(t, p, b, referenceSelect(query, []string{"expiry"}, []string{"TEXT"}, [][]any{{expiry}}, id))
			if expiry == "infinity" || expiry == "-infinity" {
				pgUserBoth(t, p, b, pgUserSelect("SELECT * FROM pg_user WHERE usesysid=$1", [][]any{pgUserRow("pgu_time", id, expiry)}, id))
			}
		}
		pgUserExec(t, p, b, "DROP ROLE pgu_time")
	})
}
