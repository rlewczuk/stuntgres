package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Select the backend before any feature SQL, allowing the root's /postgres
// oracle checkpoint to run while native installation is still red.
func stScenario(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "st_moved")
	fixture.RoleReferenceCleanup(t, []string{"st_scan_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET search_path", "DROP EXTENSION IF EXISTS tsm_system_time CASCADE", "DROP SCHEMA IF EXISTS st_moved CASCADE"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error(q, err)
					}
				}
			}()
			if install {
				srRun(t, p, b, c, fixture.Exec("CREATE EXTENSION tsm_system_time", 0))
			}
			srRun(t, p, b, c, fixture.Exec("CREATE TABLE sr(id integer NOT NULL,payload text)", 0))
			srRun(t, p, b, c, fixture.Exec("CREATE TABLE sr_empty(id integer NOT NULL,payload text)", 0))
			srRun(t, p, b, c, fixture.Exec("INSERT INTO sr VALUES(1,'v1'),(2,'v2'),(3,'v3'),(4,'v4'),(5,'v5'),(6,'v6'),(7,'v7'),(8,'v8'),(9,'v9'),(10,'v10')", 10))
			body(t, p, b, c)
		})
	}
}

func stCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	stScenario(t, true, body)
}

// A timing-dependent sample has no portable count or order. Compare its bag
// against independently specified visible occurrences, never another backend.
func stSample(t *testing.T, ctx context.Context, c fixture.Executor, q string, visible [][]any, args ...any) {
	t.Helper()
	rows, err := c.QueryContext(ctx, q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, srNames) {
		t.Fatal("sample columns", names, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 2 {
		t.Fatal("sample metadata", columns, err)
	}
	for i, column := range columns {
		if column.DatabaseTypeName() != srTypes[i] {
			t.Fatal("sample type", i, column.DatabaseTypeName())
		}
	}
	type occurrence struct { id int64; payload string; null bool }
	bag := make(map[occurrence]int, len(visible))
	for _, row := range visible {
		item := occurrence{id: row[0].(int64), null: row[1] == nil}
		if !item.null { item.payload = row[1].(string) }
		bag[item]++
	}
	for rows.Next() {
		var id int64
		var payload sql.NullString
		if err := rows.Scan(&id, &payload); err != nil { t.Fatal(err) }
		item := occurrence{id: id, payload: payload.String, null: !payload.Valid}
		if bag[item] == 0 { t.Fatalf("sample returned invisible or duplicate occurrence: %+v", item) }
		bag[item]--
	}
	if err := rows.Err(); err != nil { t.Fatal(q, err) }
}

func stSampleBoth(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, visible [][]any, args ...any) {
	t.Helper()
	stSample(t, p.Ctx, c, q, visible, args...)
	h := srHeld(t, p, c, q)
	stSample(t, p.Ctx, h, q, visible, args...)
}
