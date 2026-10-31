package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var pgstatindexNames = []string{"version", "tree_level", "index_size", "root_block_no", "internal_pages", "leaf_pages", "empty_pages", "deleted_pages", "avg_leaf_density", "leaf_fragmentation"}
var pgstatindexTypes = []string{"INT4", "INT4", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "FLOAT8", "FLOAT8"}

func pgstatindexCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "ps_part")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS pgstattuple CASCADE", "DROP SCHEMA IF EXISTS ps_part CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			for _, q := range []string{"CREATE SCHEMA ps_part", "CREATE EXTENSION pgstattuple SCHEMA ps_part", "CREATE TABLE ps_part.ps_heap(k integer)", "CREATE INDEX ps_index ON ps_part.ps_heap(k)"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func pgstatindexBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func pgstatindexError(query, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func pgstatindexNull(query string, args ...any) fixture.Step {
	return fixture.Select(query, pgstatindexNames, pgstatindexTypes, [][]any{make([]any, 10)}, args...)
}

// Compare NaN with IsNaN instead of the generic fixture's DeepEqual. All ten
// uncast columns still travel through the public driver with their exact types.
// The empty witness is independently published in REL_18_6 pgstattuple.out.
func pgstatindexEmpty(t *testing.T, p *fixture.Pair, c *sql.Conn, query string, args ...any) {
	t.Helper()
	check := func(rows *sql.Rows, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(columns, pgstatindexNames) {
			t.Fatalf("columns: %v", columns)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range types {
			if typ.DatabaseTypeName() != pgstatindexTypes[i] {
				t.Fatalf("column %s type: %s", columns[i], typ.DatabaseTypeName())
			}
		}
		if !rows.Next() {
			t.Fatalf("missing empty-index row: %v", rows.Err())
		}
		values := make([]any, 10)
		dest := make([]any, 10)
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		want := []int64{4, 0, 8192, 0, 0, 0, 0, 0}
		for i, v := range want {
			if values[i] != v {
				t.Fatalf("%s: got %T(%v), want %d", columns[i], values[i], values[i], v)
			}
		}
		for i := 8; i < 10; i++ {
			v, ok := values[i].(float64)
			if !ok || !math.IsNaN(v) {
				t.Fatalf("%s: got %T(%v), want float64 NaN", columns[i], values[i], values[i])
			}
		}
		if rows.Next() {
			t.Fatal("extra empty-index row")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	check(c.QueryContext(p.Ctx, query, args...))
	st, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	check(st.QueryContext(p.Ctx, args...))
}
