package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgUserRetainedFlag(t *testing.T, rows *sql.Rows, want bool) {
	t.Helper()
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("retained row missing", rows.Err())
	}
	var flag bool
	if err := rows.Scan(&flag); err != nil || flag != want {
		t.Fatal("retained flag", flag, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained cardinality", rows.Err())
	}
}

func TestReferencePGUserSnapshots(t *testing.T) {
	pgUserScenario(t, []string{"pgu_snapshot"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		id := pgUserCreate(t, p, b, "pgu_snapshot")
		query := "SELECT * FROM pg_user WHERE usesysid=$1"
		byID, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer byID.Close()
		nameQuery := "SELECT * FROM pg_user WHERE usename=$1"
		byName, err := b.DB.PrepareContext(p.Ctx, nameQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer byName.Close()
		heldID := fixture.HeldStatement{Executor: b.DB, Statement: byID, SQL: query}
		heldName := fixture.HeldStatement{Executor: b.DB, Statement: byName, SQL: nameQuery}
		check := func(executor fixture.Executor, q string, row [][]any, arg any) {
			fixture.PgDescriptionExecutorRows(t, p, b, executor, pgUserSelect(q, row, arg))
		}
		old, err := b.DB.QueryContext(p.Ctx, "SELECT usecreatedb FROM pg_user WHERE usesysid=$1", id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil || len(metadata) != 1 {
			t.Fatal(metadata, err)
		}
		pgUserExec(t, p, b, "ALTER ROLE pgu_snapshot CREATEDB")
		row := pgUserRow("pgu_snapshot", id, nil)
		row[2] = true
		check(heldID, query, [][]any{row}, id)
		check(heldName, nameQuery, [][]any{row}, "pgu_snapshot")
		pgUserRetainedFlag(t, old, false)
		if metadata[0].Name() != "usecreatedb" || metadata[0].DatabaseTypeName() != "BOOL" {
			t.Fatal("retained metadata changed")
		}
		pgUserExec(t, p, b, "CREATE VIEW pgu_snapshot_view AS SELECT usename,usesysid,passwd FROM pg_catalog.pg_user WHERE usename='pgu_snapshot'")
		for _, q := range []string{"SELECT usename,usesysid,passwd FROM pgu_snapshot_view", "SELECT usename,usesysid,passwd FROM (SELECT * FROM pg_user) d WHERE usename='pgu_snapshot'", "SELECT a.usename,a.usesysid,a.passwd FROM pg_user a JOIN pg_user b ON a.usesysid=b.usesysid WHERE a.usename='pgu_snapshot'"} {
			pgUserBoth(t, p, b, fixture.Select(q, []string{"usename", "usesysid", "passwd"}, []string{"NAME", "OID", "TEXT"}, [][]any{{"pgu_snapshot", id, "********"}}))
		}
		pgUserExec(t, p, b, "ALTER ROLE pgu_snapshot VALID UNTIL 'infinity'")
		pgUserBoth(t, p, b, fixture.Select("SELECT valuntil::text AS expiry FROM pg_user WHERE usesysid=$1", []string{"expiry"}, []string{"TEXT"}, [][]any{{"infinity"}}, id))
		pgUserExec(t, p, b, "ALTER ROLE pgu_snapshot NOLOGIN")
		check(heldID, query, [][]any{}, id)
		pgUserExec(t, p, b, "ALTER ROLE pgu_snapshot LOGIN")
		beforeDrop, err := b.DB.QueryContext(p.Ctx, "SELECT usecreatedb FROM pg_user WHERE usesysid=$1", id)
		if err != nil {
			t.Fatal(err)
		}
		defer beforeDrop.Close()
		pgUserExec(t, p, b, "DROP ROLE pgu_snapshot")
		pgUserRetainedFlag(t, beforeDrop, true)
		check(heldID, query, [][]any{}, id)
		replacement := pgUserCreate(t, p, b, "pgu_snapshot")
		if replacement == id {
			t.Fatal("dropped identity reused")
		}
		check(heldID, query, [][]any{}, id)
		check(heldName, nameQuery, [][]any{pgUserRow("pgu_snapshot", replacement, nil)}, "pgu_snapshot")
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if rows, err := b.DB.QueryContext(canceled, query, replacement); !errors.Is(err, context.Canceled) || rows != nil {
			if rows != nil {
				rows.Close()
			}
			t.Fatal("canceled query", err)
		}
		if stmt, err := b.DB.PrepareContext(canceled, query); !errors.Is(err, context.Canceled) || stmt != nil {
			if stmt != nil {
				stmt.Close()
			}
			t.Fatal("canceled prepare", err)
		}
		if rows, err := byID.QueryContext(canceled, replacement); !errors.Is(err, context.Canceled) || rows != nil {
			if rows != nil {
				rows.Close()
			}
			t.Fatal("canceled held query", err)
		}
		check(heldName, nameQuery, [][]any{pgUserRow("pgu_snapshot", replacement, nil)}, "pgu_snapshot")
		// Native driver lifetime has no PostgreSQL driver-engine analogue.
		if !b.PG {
			pool := sql.OpenDB(p.NativeConnector)
			var before string
			if err := pool.QueryRowContext(p.Ctx, "SELECT passwd FROM pgu_snapshot_view").Scan(&before); err != nil || before != "********" {
				t.Fatal("original pool", err)
			}
			if err := pool.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := sql.OpenDB(p.NativeConnector)
			defer reopened.Close()
			var mask string
			if err := reopened.QueryRowContext(p.Ctx, "SELECT passwd FROM pg_user WHERE usesysid=$1", replacement).Scan(&mask); err != nil || mask != "********" {
				t.Fatal("same engine reopen", err)
			}
			if err := reopened.QueryRowContext(p.Ctx, "SELECT passwd FROM pgu_snapshot_view").Scan(&mask); err != nil || mask != "********" {
				t.Fatal("stored view after reopen", err)
			}
			connector, err := stuntgres.NewDriver().OpenConnector("reference/" + t.Name())
			if err != nil {
				t.Fatal(err)
			}
			isolated := sql.OpenDB(connector)
			defer isolated.Close()
			var count int64
			if err := isolated.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_user WHERE usename='pgu_snapshot'").Scan(&count); err != nil || count != 0 {
				t.Fatal("driver isolation", count, err)
			}
		}
		pgUserExec(t, p, b, "DROP VIEW pgu_snapshot_view")
		pgUserExec(t, p, b, "DROP ROLE pgu_snapshot")
	})
}
