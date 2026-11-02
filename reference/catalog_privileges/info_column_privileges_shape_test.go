package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnPrivilegesShape(t *testing.T) {
	cpScenario(t, func(c *cpCase) {
		c.exec("CREATE TABLE cp_shape(a integer,b integer)")
		want := c.baseline(c.owner, "cp_shape", "a", "b")
		c.check(c.conn, "cp_shape", c.owner, want...)
		held := c.hold(c.conn, cpQuery)
		c.check(held, "cp_shape", c.owner, want...)
		rows, err := held.QueryContext(c.p.Ctx, cpQuery, "cp_shape", c.owner)
		if err != nil {
			c.t.Fatal(err)
		}
		rcgConsume(c.t, rows, c.b.PG, want)
		for _, query := range []string{
			"SELECT * FROM information_schema.column_privileges WHERE false",
			"SELECT * FROM information_schema.column_privileges WHERE table_name='cp_missing'",
			"SELECT * FROM information_schema.column_privileges WHERE table_name='cp_shape' AND column_name='absent'",
			"SELECT * FROM information_schema.column_privileges WHERE table_name='cp_shape' AND grantee=$1",
			"SELECT * FROM information_schema.column_privileges WHERE table_name='cp_shape' AND column_name IS NULL",
		} {
			var args []any
			if query == "SELECT * FROM information_schema.column_privileges WHERE table_name='cp_shape' AND grantee=$1" {
				args = []any{nil}
			}
			s := c.step(query, nil, args...)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			boundary := c.hold(c.conn, query)
			c.run(boundary, s)
			rows, err := boundary.QueryContext(c.p.Ctx, query, args...)
			if err != nil {
				c.t.Fatal(err)
			}
			rcgConsume(c.t, rows, c.b.PG, nil)
		}
		query := "SELECT g.* FROM (SELECT 1 AS witness) w LEFT JOIN information_schema.column_privileges g ON g.table_name='cp_missing'"
		s := fixture.Select(query, cpColumns, cpTypes, [][]any{make([]any, 8)})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { rcgMetadata(t, rows, c.b.PG) }
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		c.run(c.hold(c.conn, query), s)
		query = `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull,t.typtypmod FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='column_privileges' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
		var logical [][]any
		for i, name := range cpColumns {
			domain, base, oid, modifier := "sql_identifier", "name", int64(19), int64(-1)
			if i == 6 {
				domain, base, oid = "character_data", "varchar", 1043
			}
			if i == 7 {
				domain, base, oid, modifier = "yes_or_no", "varchar", 1043, 7
			}
			logical = append(logical, []any{int64(i + 1), name, "information_schema", domain, "pg_catalog", base, oid, int64(950), int64(-1), int64(0), false, false, modifier})
		}
		s = fixture.Select(query, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL", "INT4"}, logical)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		query = `SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='column_privileges' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`
		s = fixture.Select(query, []string{"row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	})
}
