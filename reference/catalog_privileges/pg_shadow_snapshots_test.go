package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pgShadowRetained(t *testing.T, rows *sql.Rows, name, password, expiry string, createdb bool) {
	t.Helper(); defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 4 || meta[0].Name() != "usename" || meta[0].DatabaseTypeName() != "NAME" || meta[1].DatabaseTypeName() != "TEXT" || meta[2].DatabaseTypeName() != "TEXT" || meta[3].DatabaseTypeName() != "BOOL" { t.Fatal("retained shadow metadata") }
	if !rows.Next() { t.Fatal("retained shadow row missing") }
	var n, secret, until string; var flag bool
	if err := rows.Scan(&n, &secret, &until, &flag); err != nil || n != name || secret != password || until != expiry || flag != createdb { t.Fatal("retained shadow data changed") }
	if rows.Next() || rows.Err() != nil { t.Fatal("retained shadow cardinality") }
}

func TestReferencePGShadowSnapshots(t *testing.T) {
	pgUserScenario(t, []string{"pgs_snapshot", "pgs_snapshot_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgUserCreate(t, p, b, "pgs_snapshot")
		pgUserCreate(t, p, b, "pgs_snapshot_reader")
		password := pgShadowMD5; replacement := pgShadowSCRAM(t)
		pgShadowSetPassword(t, p, b, "pgs_snapshot", &password)
		pgUserExec(t, p, b, "ALTER ROLE pgs_snapshot VALID UNTIL '2030-01-02 03:04:05+00'")
		conn, err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer conn.Close()
		q := "SELECT usename,passwd,valuntil::text AS expiry,usecreatedb FROM pg_shadow WHERE usesysid=$1"
		stmt, err := conn.PrepareContext(p.Ctx, q); if err != nil { t.Fatal(err) }; defer stmt.Close()
		old, err := stmt.QueryContext(p.Ctx, id); if err != nil { t.Fatal(err) }; defer old.Close()
		// pq streams; consume before another command on its physical connection.
		if b.PG { pgShadowRetained(t, old, "pgs_snapshot", password, "2030-01-02 03:04:05+00", false) }
		pgShadowSetPassword(t, p, b, "pgs_snapshot", &replacement)
		pgUserExec(t, p, b, "ALTER ROLE pgs_snapshot CREATEDB VALID UNTIL '-infinity'")
		if !b.PG {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET ROLE pgs_snapshot_reader", 0))
			denied := pgShadowDenied(q, false); denied.Args = []any{id}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, b.Recorder, denied)
			pgShadowRetained(t, old, "pgs_snapshot", password, "2030-01-02 03:04:05+00", false)
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
		}
		for _, prepared := range []bool{false, true} { pgShadowPassword(t, p, conn, prepared, "pgs_snapshot", &replacement) }
		old, err = stmt.QueryContext(p.Ctx, id); if err != nil { t.Fatal(err) }; defer old.Close()
		if b.PG { pgShadowRetained(t, old, "pgs_snapshot", replacement, "-infinity", true) }
		// Reusing the same name must not reattach a held identity or resurrect its
		// credentials. No role rename support is needed for this name/ID witness.
		pgUserExec(t, p, b, "DROP ROLE pgs_snapshot")
		newID := pgUserCreate(t, p, b, "pgs_snapshot")
		if newID == id { t.Fatal("drop/recreate reused role identity") }
		if !b.PG { pgShadowRetained(t, old, "pgs_snapshot", replacement, "-infinity", true) }
		pgUserBoth(t, p, b, pgShadowSelect("SELECT * FROM pg_shadow WHERE usesysid=$1", [][]any{}, id))
		pgUserBoth(t, p, b, pgShadowSelect("SELECT * FROM pg_shadow WHERE usesysid=$1", [][]any{pgShadowRow("pgs_snapshot", newID, nil, nil)}, newID))
		for _, name := range []string{"pgs_snapshot", "pgs_snapshot_reader"} { pgUserExec(t, p, b, "DROP ROLE "+name) }
	})
}
