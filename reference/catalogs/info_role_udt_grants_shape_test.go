package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleUdtGrantsShape(t *testing.T) {
	ugScenario(t, func(c *ugCase) {
		c.seed()
		want := c.baseline("ug_o", "public", "ug_t")
		held := c.hold(c.conn, ugQuery)
		c.check(c.conn, "public", "ug_t", want...)
		c.check(held, "public", "ug_t", want...)
		q := "SELECT " + strings.Join(ugColumns, ",") + " FROM information_schema.role_udt_grants WHERE udt_schema=$1 AND udt_name=$2"
		c.run(c.conn, c.step(q, want, "public", "ug_t"))
		c.run(c.conn, fixture.Prepared(c.step(q, want, "public", "ug_t")))
		for _, predicate := range []string{"false", "udt_name=NULL", "udt_name='ug_missing'", "udt_name IS NULL", "udt_name=$1", "udt_name=''"} {
			q := "SELECT * FROM information_schema.role_udt_grants WHERE " + predicate
			var args []any
			if predicate == "udt_name=$1" {
				args = []any{nil}
			}
			s := c.step(q, nil, args...)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(c.hold(c.conn, q), s)
		}
		// Retained metadata must survive EOF and Close, including an empty result.
		for _, name := range []string{"ug_t", "ug_missing"} {
			rows, err := held.QueryContext(c.p.Ctx, ugQuery, "public", name)
			if err != nil {
				c.t.Fatal(err)
			}
			saved := ugMetadata(c.t, rows, c.b.PG)
			count := 0
			for rows.Next() {
				count++
			}
			if err := rows.Err(); err != nil {
				c.t.Fatal(err)
			}
			expected := 0
			if name == "ug_t" {
				expected = 2
			}
			if count != expected {
				c.t.Fatalf("rows=%d want %d", count, expected)
			}
			ugColumnTypes(c.t, saved, c.b.PG)
			if err := rows.Close(); err != nil {
				c.t.Fatal(err)
			}
			ugColumnTypes(c.t, saved, c.b.PG)
		}
		q = "SELECT g.* FROM (SELECT 1 AS witness) w LEFT JOIN information_schema.role_udt_grants g ON g.udt_name='ug_missing'"
		s := fixture.Select(q, ugColumns, ugTypes, [][]any{make([]any, 7)})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ugMetadata(t, rows, c.b.PG) }
		s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
			if value != nil {
				t.Fatalf("NULL extension %d,%d=%#v", row, column, value)
			}
			return false
		}
		s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
			nullable, ptrs, targets := make([]sql.NullString, 7), make([]*string, 7), make([]any, 7)
			for i := range targets {
				targets[i] = &nullable[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			for i := range targets {
				if nullable[i].Valid {
					t.Fatal("NULL scanned valid", i)
				}
				targets[i] = &ptrs[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			for i := range ptrs {
				if ptrs[i] != nil {
					t.Fatal("NULL pointer", i)
				}
			}
		}
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		c.run(c.hold(c.conn, q), s)
		q = `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull,t.typtypmod FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='role_udt_grants' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
		var logical, columns [][]any
		for i, name := range ugColumns {
			domain, base, oid, modifier := "sql_identifier", "name", int64(19), int64(-1)
			if i == 5 {
				domain, base, oid = "character_data", "varchar", 1043
			}
			if i == 6 {
				domain, base, oid, modifier = "yes_or_no", "varchar", 1043, 7
			}
			logical = append(logical, []any{int64(i + 1), name, "information_schema", domain, "pg_catalog", base, oid, int64(950), int64(-1), int64(0), false, false, modifier})
			columns = append(columns, []any{name, int64(i + 1), "YES", "information_schema", domain})
		}
		s = fixture.Select(q, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL", "INT4"}, logical)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q = `SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='role_udt_grants' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`
		s = fixture.Select(q, []string{"row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		s = fixture.Select("SELECT column_name,ordinal_position,is_nullable,domain_schema,domain_name FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='role_udt_grants' ORDER BY ordinal_position", []string{"column_name", "ordinal_position", "is_nullable", "domain_schema", "domain_name"}, []string{"NAME", "INT4", "VARCHAR", "NAME", "NAME"}, columns)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	})
}
