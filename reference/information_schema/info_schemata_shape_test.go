package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoSchemataShape(t *testing.T) {
	iscScenario(t, func(c *iscCase) {
		c.exec("CREATE ROLE isc_owner", "CREATE SCHEMA isc_owned AUTHORIZATION isc_owner")
		want := c.row("isc_owned", "isc_owner")
		for _, selectSQL := range []string{iscQuery, "SELECT " + strings.Join(iscColumns, ",") + " FROM information_schema.schemata"} {
			for _, tc := range []struct {
				where string
				rows  [][]any
				args  []any
			}{
				{"schema_name='isc_owned'", want, nil},
				{"schema_name='isc_absent'", nil, nil},
				{"FALSE", nil, nil}, {"NULL::boolean", nil, nil}, {"schema_name IS NULL", nil, nil},
				{"schema_name=$1", want, []any{"isc_owned"}}, {"schema_name=$1", nil, []any{"isc_absent"}}, {"schema_name=$1", nil, []any{nil}},
			} {
				q := selectSQL + " WHERE " + tc.where
				c.routes(c.projection(q, tc.rows, tc.args...))
				rows, err := c.conn.QueryContext(c.p.Ctx, q, tc.args...)
				if err != nil {
					t.Fatal(err)
				}
				c.consume(rows, tc.rows)
			}
		}
		held := c.hold(iscQuery + " WHERE schema_name=$1")
		for _, name := range []any{"isc_owned", nil, "isc_absent", "isc_owned"} {
			var expected [][]any
			if name == "isc_owned" {
				expected = want
			}
			c.run(held, c.projection(held.SQL, expected, name))
			rows, err := held.Statement.QueryContext(c.p.Ctx, name)
			if err != nil {
				t.Fatal(err)
			}
			c.consume(rows, expected)
		}
		tail := fixture.Select("SELECT default_character_set_catalog,default_character_set_schema,default_character_set_name,sql_path FROM information_schema.schemata WHERE schema_name='isc_owned'", iscColumns[3:], iscTypes[3:], [][]any{{nil, nil, nil, nil}})
		tail.CheckRaw = func(t *testing.T, column, row int, v any) bool {
			if v != nil {
				t.Fatalf("tail %d=%T(%v)", column, v, v)
			}
			return true
		}
		tail.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			iscProjectionMetadata(t, columns, c.b.PG, 3)
		}
		c.routes(tail)
		rows, err := c.conn.QueryContext(c.p.Ctx, tail.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		iscProjectionMetadata(t, columns, c.b.PG, 3)
		if !rows.Next() {
			t.Fatal("missing NULL-only row", rows.Err())
		}
		var values [4]any
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3]); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			tail.CheckRaw(t, i, 0, v)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("NULL-only row boundary", rows.Err())
		}
		iscProjectionMetadata(t, columns, c.b.PG, 3)
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		iscProjectionMetadata(t, columns, c.b.PG, 3)
		c.identity()
	})
}
