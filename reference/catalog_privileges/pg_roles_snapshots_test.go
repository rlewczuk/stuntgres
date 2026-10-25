package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func pgRolesRetainedRow(t *testing.T, rows *sql.Rows, expected []any) {
	t.Helper()
	defer rows.Close()
	meta := pgRolesMetadata(t, rows, false)
	if !rows.Next() {
		t.Fatal("missing retained role", rows.Err())
	}
	cells := make([]any, len(fixture.PGRolesColumns))
	dest := make([]any, len(cells))
	for i := range cells {
		dest[i] = &cells[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cells, expected) {
		t.Fatalf("retained role=%v want %v", cells, expected)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained role cardinality", rows.Err())
	}
	pgRolesSavedMetadata(t, meta, false)
}

func TestReferencePGRolesSnapshots(t *testing.T) {
	pgRolesScenario(t, []string{"pgr_snapshot"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		query := "SELECT * FROM pg_roles WHERE oid=$1"
		byID, err := b.DB.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer byID.Close()
		nameQuery := "SELECT * FROM pg_roles WHERE rolname=$1"
		byName, err := b.DB.PrepareContext(p.Ctx, nameQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer byName.Close()
		heldID := fixture.HeldStatement{Executor: b.DB, Statement: byID, SQL: query}
		heldName := fixture.HeldStatement{Executor: b.DB, Statement: byName, SQL: nameQuery}
		check := func(executor fixture.Executor, q string, row [][]any, arg any) {
			fixture.PgDescriptionExecutorRows(t, p, b, executor, pgRolesSelect(q, row, arg))
		}
		check(heldName, nameQuery, [][]any{}, "pgr_snapshot")
		id := pgRolesCreate(t, p, b, "pgr_snapshot")
		check(heldName, nameQuery, [][]any{pgRolesRow("pgr_snapshot", id, nil)}, "pgr_snapshot")
		var fullOld *sql.Rows
		if !b.PG {
			fullOld, err = b.DB.QueryContext(p.Ctx, query, id)
			if err != nil {
				t.Fatal(err)
			}
			defer fullOld.Close()
		}
		old, err := b.DB.QueryContext(p.Ctx, "SELECT rolcreatedb FROM pg_roles WHERE oid=$1", id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil || len(metadata) != 1 {
			t.Fatal(metadata, err)
		}
		pgRolesExec(t, p, b, "ALTER ROLE pgr_snapshot CREATEDB")
		row := pgRolesRow("pgr_snapshot", id, nil)
		row[4] = true
		check(heldID, query, [][]any{row}, id)
		check(heldName, nameQuery, [][]any{row}, "pgr_snapshot")
		pgUserRetainedFlag(t, old, false)
		if metadata[0].Name() != "rolcreatedb" || metadata[0].DatabaseTypeName() != "BOOL" {
			t.Fatal("retained metadata changed")
		}
		pgRolesExec(t, p, b, "ALTER ROLE pgr_snapshot LOGIN CONNECTION LIMIT 3 VALID UNTIL '2030-01-02 03:04:05+00'")
		row[5], row[7], row[9] = true, int64(3), time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		check(heldID, query, [][]any{row}, id)
		check(heldName, nameQuery, [][]any{row}, "pgr_snapshot")
		if fullOld != nil {
			pgRolesRetainedRow(t, fullOld, pgRolesRow("pgr_snapshot", id, nil))
		}
		pgRolesExec(t, p, b, "CREATE VIEW pgr_snapshot_view AS SELECT rolname,oid,rolpassword FROM pg_catalog.pg_roles WHERE rolname='pgr_snapshot'")
		for _, q := range []string{"SELECT rolname,oid,rolpassword FROM pgr_snapshot_view", "SELECT rolname,oid,rolpassword FROM (SELECT * FROM pg_roles) d WHERE rolname='pgr_snapshot'", "SELECT rolname,oid,rolpassword FROM (SELECT * FROM (SELECT * FROM pg_roles) n) d WHERE rolname='pgr_snapshot'", "SELECT a.rolname,a.oid,a.rolpassword FROM pg_roles a JOIN pg_roles b ON a.oid=b.oid WHERE a.rolname='pgr_snapshot'"} {
			pgRolesBoth(t, p, b, fixture.Select(q, []string{"rolname", "oid", "rolpassword"}, []string{"NAME", "OID", "TEXT"}, [][]any{{"pgr_snapshot", id, "********"}}))
		}
		pgRolesExec(t, p, b, "ALTER ROLE pgr_snapshot VALID UNTIL 'infinity'")
		pgRolesBoth(t, p, b, fixture.Select("SELECT rolvaliduntil::text AS expiry FROM pg_roles WHERE oid=$1", []string{"expiry"}, []string{"TEXT"}, [][]any{{"infinity"}}, id))
		pgRolesExec(t, p, b, "ALTER ROLE pgr_snapshot NOLOGIN")
		row[5], row[9] = false, "infinity"
		check(heldID, query, [][]any{row}, id)
		check(heldName, nameQuery, [][]any{row}, "pgr_snapshot")
		pgRolesExec(t, p, b, "ALTER ROLE pgr_snapshot LOGIN")
		row[5] = true
		check(heldID, query, [][]any{row}, id)
		beforeDrop, err := b.DB.QueryContext(p.Ctx, "SELECT rolcreatedb FROM pg_roles WHERE oid=$1", id)
		if err != nil {
			t.Fatal(err)
		}
		defer beforeDrop.Close()
		pgRolesExec(t, p, b, "DROP ROLE pgr_snapshot")
		pgUserRetainedFlag(t, beforeDrop, true)
		check(heldID, query, [][]any{}, id)
		replacement := pgRolesCreate(t, p, b, "pgr_snapshot")
		if replacement == id {
			t.Fatal("dropped identity reused")
		}
		check(heldID, query, [][]any{}, id)
		check(heldName, nameQuery, [][]any{pgRolesRow("pgr_snapshot", replacement, nil)}, "pgr_snapshot")
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
		check(heldName, nameQuery, [][]any{pgRolesRow("pgr_snapshot", replacement, nil)}, "pgr_snapshot")
		// Native driver lifetime has no PostgreSQL driver-engine analogue.
		if !b.PG {
			pool := sql.OpenDB(p.NativeConnector)
			var before string
			if err := pool.QueryRowContext(p.Ctx, "SELECT rolpassword FROM pgr_snapshot_view").Scan(&before); err != nil || before != "********" {
				t.Fatal("original pool", err)
			}
			if err := pool.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := sql.OpenDB(p.NativeConnector)
			defer reopened.Close()
			var mask string
			if err := reopened.QueryRowContext(p.Ctx, "SELECT rolpassword FROM pg_roles WHERE oid=$1", replacement).Scan(&mask); err != nil || mask != "********" {
				t.Fatal("same engine reopen", err)
			}
			if err := reopened.QueryRowContext(p.Ctx, "SELECT rolpassword FROM pgr_snapshot_view").Scan(&mask); err != nil || mask != "********" {
				t.Fatal("stored view after reopen", err)
			}
			connector, err := stuntgres.NewDriver().OpenConnector("reference/" + t.Name())
			if err != nil {
				t.Fatal(err)
			}
			isolated := sql.OpenDB(connector)
			defer isolated.Close()
			var count int64
			if err := isolated.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_roles WHERE rolname='pgr_snapshot'").Scan(&count); err != nil || count != 0 {
				t.Fatal("driver isolation", count, err)
			}
		}
		pgRolesExec(t, p, b, "DROP VIEW pgr_snapshot_view")
		pgRolesExec(t, p, b, "DROP ROLE pgr_snapshot")
	})
}
