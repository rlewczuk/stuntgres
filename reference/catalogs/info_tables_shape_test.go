package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoTablesShape(t *testing.T) {
	itScenario(t, func(c *itCase) {
		c.exec("CREATE TABLE it_scope.base(a integer)", "CREATE TABLE it_scope.zero()")
		want := [][]any{c.row("base", "BASE TABLE", "YES")}
		held := c.hold(itSelect)
		s := itStep(c.b.PG, itSelect, want, "it_scope", "base")
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		c.run(held, s)
		q := "SELECT " + strings.Join(itColumns, ",") + " FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2"
		s = itStep(c.b.PG, q, want, "it_scope", "base")
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		c.check("zero", c.row("zero", "BASE TABLE", "YES"))
		for _, args := range [][]any{{"it_scope", "absent"}, {"absent", "base"}, {"it_scope", nil}} {
			s := itStep(c.b.PG, itSelect, nil, args...)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(held, s)
		}
		for _, where := range []string{"false", "NULL", "table_name IS NULL"} {
			s := itStep(c.b.PG, "SELECT * FROM information_schema.tables WHERE "+where, nil)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			rows, err := c.conn.QueryContext(c.p.Ctx, s.Query)
			if err != nil {
				c.t.Fatal(err)
			}
			itMetadata(c.t, rows, c.b.PG)
			if rows.Next() {
				c.t.Fatal("unexpected empty row")
			}
			if err := rows.Err(); err != nil {
				c.t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				c.t.Fatal(err)
			}
		}
		s = itStep(c.b.PG, "SELECT v.* FROM (SELECT 1 AS witness) w LEFT JOIN information_schema.tables v ON v.table_schema='it_scope' AND v.table_name='absent'", [][]any{make([]any, 12)})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		for _, tc := range []struct{ schema, name, kind, insert string }{{"information_schema", "tables", "VIEW", "NO"}, {"pg_catalog", "pg_namespace", "BASE TABLE", "YES"}} {
			s := itStep(c.b.PG, itSelect, [][]any{itRow(c.database, tc.schema, tc.name, tc.kind, tc.insert)}, tc.schema, tc.name)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
		}
		q = `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull,t.typtypmod FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='tables' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
		logical := make([][]any, 12)
		for i, name := range itColumns {
			domain, base, oid, mod := "sql_identifier", "name", int64(19), int64(-1)
			if itDomains[i] == "C" {
				domain, base, oid = "character_data", "varchar", 1043
			}
			if itDomains[i] == "Y" {
				domain, base, oid, mod = "yes_or_no", "varchar", 1043, 7
			}
			logical[i] = []any{int64(i + 1), name, "information_schema", domain, "pg_catalog", base, oid, int64(950), int64(-1), int64(0), false, false, mod}
		}
		s = fixture.Select(q, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL", "INT4"}, logical)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q = `SELECT c.relkind,c.oid>0 AS positive,t.oid>0 AS positive_row,a.oid>0 AS positive_array,c.reltype='information_schema.tables'::regtype AS nominal,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.tables'::regclass`
		s = fixture.Select(q, []string{"relkind", "positive", "positive_row", "positive_array", "nominal", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", true, true, true, true, true, true, true, true, true, true, true}})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	})
}

func TestReferenceInfoTablesPopulation(t *testing.T) {
	itScenario(t, func(c *itCase) {
		c.exec("CREATE TABLE it_scope.base(a integer)", "CREATE TABLE it_scope.zero()", "CREATE TABLE it_scope.parent(a integer) PARTITION BY RANGE(a)", "CREATE TABLE it_scope.child PARTITION OF it_scope.parent FOR VALUES FROM(0) TO(10)", "CREATE VIEW it_scope.simple AS SELECT a FROM it_scope.base", "CREATE TYPE it_scope.row_t AS(a integer)", "CREATE TABLE it_scope.typed OF it_scope.row_t", "CREATE SEQUENCE it_scope.seq", "CREATE INDEX excluded_idx ON it_scope.base(a)", "CREATE MATERIALIZED VIEW it_scope.mat AS SELECT a FROM it_scope.base", "CREATE TABLE it_scope.identity_t(id integer GENERATED ALWAYS AS IDENTITY)")
		typed := c.row("typed", "BASE TABLE", "YES")
		typed[6], typed[7], typed[8], typed[10] = c.database, "it_scope", "row_t", "YES"
		want := [][]any{c.row("base", "BASE TABLE", "YES"), c.row("zero", "BASE TABLE", "YES"), c.row("parent", "BASE TABLE", "YES"), c.row("child", "BASE TABLE", "YES"), c.row("simple", "VIEW", "YES"), typed, c.row("identity_t", "BASE TABLE", "YES")}
		c.bag(want...)
		for _, name := range []string{"seq", "excluded_idx", "mat", "row_t", "absent"} {
			c.check(name)
		}
		held := c.hold(itSelect)
		c.exec("CREATE SCHEMA it_moved", "ALTER TYPE it_scope.row_t RENAME TO renamed", "ALTER TYPE it_scope.renamed SET SCHEMA it_moved")
		typed = append([]any(nil), typed...)
		typed[7], typed[8] = "it_moved", "renamed"
		c.check("typed", typed)
		c.run(held, itStep(c.b.PG, itSelect, [][]any{typed}, "it_scope", "typed"))
	})
}
