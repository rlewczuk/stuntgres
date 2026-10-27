package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pgAuthIDRetained(t *testing.T, rows *sql.Rows, name, password, expiry string, createdb bool) {
	t.Helper()
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 4 || meta[0].Name() != "rolname" || meta[0].DatabaseTypeName() != "NAME" || meta[1].DatabaseTypeName() != "TEXT" || meta[2].DatabaseTypeName() != "TEXT" || meta[3].DatabaseTypeName() != "BOOL" {
		t.Fatal("retained shadow metadata")
	}
	if !rows.Next() {
		t.Fatal("retained shadow row missing")
	}
	var n, secret, until string
	var flag bool
	if err := rows.Scan(&n, &secret, &until, &flag); err != nil || n != name || secret != password || until != expiry || flag != createdb {
		t.Fatal("retained shadow data changed")
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained shadow cardinality")
	}
}

func TestReferencePGAuthIDLifecycle(t *testing.T) {
	pgUserScenario(t, []string{"pga_snapshot", "pga_snapshot_renamed", "pga_snapshot_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgUserCreate(t, p, b, "pga_snapshot")
		pgUserCreate(t, p, b, "pga_snapshot_reader")
		password := pgShadowMD5
		replacement := pgShadowSCRAM(t)
		pgShadowSetPassword(t, p, b, "pga_snapshot", &password)
		pgUserExec(t, p, b, "ALTER ROLE pga_snapshot VALID UNTIL '2030-01-02 03:04:05+00'")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q := "SELECT rolname,rolpassword,rolvaliduntil::text AS expiry,rolcreatedb FROM pg_authid WHERE oid=$1"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		old, err := stmt.QueryContext(p.Ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		// pq streams; consume before another command on its physical connection.
		if b.PG {
			pgAuthIDRetained(t, old, "pga_snapshot", password, "2030-01-02 03:04:05+00", false)
		}
		pgShadowSetPassword(t, p, b, "pga_snapshot", &replacement)
		pgUserExec(t, p, b, "ALTER ROLE pga_snapshot CREATEDB VALID UNTIL '-infinity'")
		pgUserExec(t, p, b, "ALTER ROLE pga_snapshot RENAME TO pga_snapshot_renamed")
		capturedName := "pga_snapshot_renamed"
		if !b.PG {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET ROLE pga_snapshot_reader", 0))
			denied := pgAuthIDDenied(q, false)
			denied.Args = []any{id}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, denied)
			pgAuthIDRetained(t, old, "pga_snapshot", password, "2030-01-02 03:04:05+00", false)
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
		}
		rows, err := stmt.QueryContext(p.Ctx, id)
		if err != nil {
			t.Fatal("renamed retained execution failed")
		}
		pgAuthIDRetained(t, rows, capturedName, replacement, "-infinity", true)
		pgUserExec(t, p, b, "ALTER ROLE pga_snapshot_renamed RENAME TO pga_snapshot")
		for _, prepared := range []bool{false, true} {
			pgAuthIDPassword(t, p, conn, prepared, "pga_snapshot", &replacement)
		}
		old, err = stmt.QueryContext(p.Ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if b.PG {
			pgAuthIDRetained(t, old, "pga_snapshot", replacement, "-infinity", true)
		}
		// Reusing the name must not reattach the old retained identity.
		pgUserExec(t, p, b, "DROP ROLE pga_snapshot")
		newID := pgUserCreate(t, p, b, "pga_snapshot")
		if newID == id {
			t.Fatal("drop/recreate reused role identity")
		}
		if !b.PG {
			pgAuthIDRetained(t, old, "pga_snapshot", replacement, "-infinity", true)
		}
		pgUserBoth(t, p, b, pgAuthIDSelect("SELECT * FROM pg_authid WHERE oid=$1", [][]any{}, id))
		pgUserBoth(t, p, b, pgAuthIDSelect("SELECT * FROM pg_authid WHERE oid=$1", [][]any{pgAuthIDRow("pga_snapshot", newID, true)}, newID))
		for _, name := range []string{"pga_snapshot", "pga_snapshot_reader"} {
			pgUserExec(t, p, b, "DROP ROLE "+name)
		}
	})
	pgAuthIDTransactions(t)
}

func pgAuthIDTransactions(t *testing.T) {
	pgUserScenario(t, []string{"pga_transaction"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		q := "SELECT rolname,rolconnlimit FROM pg_authid WHERE rolname='pga_transaction'"
		observer, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		want := func(rows [][]any) fixture.Step {
			return fixture.Select(q, []string{"rolname", "rolconnlimit"}, []string{"NAME", "INT4"}, rows)
		}
		for _, commit := range []bool{false, true} {
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer tx.Rollback()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
				run(fixture.Exec("CREATE ROLE pga_transaction", 0))
				stmt, err := tx.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: q}
				assert := func(limit int64) {
					s := want([][]any{{"pga_transaction", limit}})
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				assert(-1)
				run(fixture.Exec("SAVEPOINT role_fields", 0))
				run(fixture.Exec("ALTER ROLE pga_transaction CONNECTION LIMIT 7", 0))
				assert(7)
				run(fixture.Exec("ROLLBACK TO role_fields", 0))
				assert(-1)
				run(fixture.Exec("SAVEPOINT failed_role", 0))
				s := fixture.ImplicitError("CREATE ROLE pga_transaction", fixture.Diagnostic{Code: "42710", Message: `role "pga_transaction" already exists`})
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Exec("ROLLBACK TO failed_role", 0))
				assert(-1)
				run(fixture.Exec("SAVEPOINT dropped_role", 0))
				run(fixture.Exec("DROP ROLE pga_transaction", 0))
				run(want([][]any{}))
				run(fixture.Exec("ROLLBACK TO dropped_role", 0))
				assert(-1)
				// A separate retained observer sees only committed role publication.
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: observer, SQL: q}, b.Recorder, want([][]any{}))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
			}()
			rows := [][]any{}
			if commit {
				rows = [][]any{{"pga_transaction", int64(-1)}}
			}
			pgAuthIDRoutes(t, p, b, want(rows))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: observer, SQL: q}, b.Recorder, want(rows))
		}
		pgUserExec(t, p, b, "DROP ROLE pga_transaction")
	})
}
