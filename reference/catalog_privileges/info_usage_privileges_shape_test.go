package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUsagePrivilegesShape(t *testing.T) {
	upScenario(t, func(c *upCase) {
		c.seed()
		c.exec("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM PUBLIC", "GRANT USAGE ON DOMAIN public.up_t TO up_r", "RESET ROLE")
		want := [][]any{c.origin("up_o", "up_o", "YES"), c.origin("up_o", "up_r", "NO")}
		held := c.hold(c.conn, upQuery)
		c.check(c.conn, "public", "up_t", want...)
		c.check(held, "public", "up_t", want...)
		q := "SELECT " + strings.Join(upColumns, ",") + " FROM information_schema.usage_privileges WHERE object_schema=$1 AND object_name=$2"
		c.run(c.conn, c.step(q, want, "public", "up_t"))
		c.run(c.conn, fixture.Prepared(c.step(q, want, "public", "up_t")))
		alias := fixture.Select("SELECT g.object_name AS name,g.object_type AS family FROM information_schema.usage_privileges g WHERE g.object_schema=$1 AND g.object_name=$2 AND g.grantee=$3", []string{"name", "family"}, []string{"NAME", "VARCHAR"}, [][]any{{"up_t", "DOMAIN"}}, "public", "up_t", "up_r")
		c.run(c.conn, alias)
		c.run(c.conn, fixture.Prepared(alias))
		for _, predicate := range []string{"false", "object_name=NULL", "object_name='up_missing'", "object_name IS NULL", "object_name=$1", "object_name=''"} {
			q := "SELECT * FROM information_schema.usage_privileges WHERE " + predicate
			var args []any
			if predicate == "object_name=$1" {
				args = []any{nil}
			}
			s := c.step(q, nil, args...)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(c.hold(c.conn, q), s)
		}
		// Retained metadata must survive EOF and Close, including an empty result.
		for _, name := range []string{"up_t", "up_missing"} {
			rows, err := held.QueryContext(c.p.Ctx, upQuery, "public", name)
			if err != nil {
				c.t.Fatal(err)
			}
			defer func() {
				if err := rows.Close(); err != nil {
					c.t.Error(err)
				}
			}()
			saved := upMetadata(c.t, rows, c.b.PG)
			if !c.b.PG {
				// Native owns detached metadata; pq aliases its column names.
				names, err := rows.Columns()
				if err != nil {
					c.t.Fatal(err)
				}
				names[0] = "corrupted"
				upMetadata(c.t, rows, false)
			}
			count := 0
			for rows.Next() {
				count++
			}
			if err := rows.Err(); err != nil {
				c.t.Fatal(err)
			}
			expected := 0
			if name == "up_t" {
				expected = 2
			}
			if count != expected {
				c.t.Fatalf("rows=%d want %d", count, expected)
			}
			upColumnTypes(c.t, saved, c.b.PG)
			if err := rows.Close(); err != nil {
				c.t.Fatal(err)
			}
			upColumnTypes(c.t, saved, c.b.PG)
		}
		q = "SELECT g.* FROM (SELECT 1 AS witness) w LEFT JOIN information_schema.usage_privileges g ON g.object_name='up_missing'"
		s := fixture.Select(q, upColumns, upTypes, [][]any{make([]any, 8)})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { upMetadata(t, rows, c.b.PG) }
		s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
			if value != nil {
				t.Fatalf("NULL extension %d,%d=%#v", row, column, value)
			}
			return false
		}
		s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
			nullable, ptrs, targets := make([]sql.NullString, 8), make([]*string, 8), make([]any, 8)
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
		q = `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull,t.typtypmod FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='usage_privileges' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
		var logical, columns [][]any
		for i, name := range upColumns {
			domain, base, oid, modifier := "sql_identifier", "name", int64(19), int64(-1)
			if i == 5 || i == 6 {
				domain, base, oid = "character_data", "varchar", 1043
			}
			if i == 7 {
				domain, base, oid, modifier = "yes_or_no", "varchar", 1043, 7
			}
			logical = append(logical, []any{int64(i + 1), name, "information_schema", domain, "pg_catalog", base, oid, int64(950), int64(-1), int64(0), false, false, modifier})
			columns = append(columns, []any{name, int64(i + 1), "YES", "information_schema", domain})
		}
		s = fixture.Select(q, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL", "INT4"}, logical)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q = `SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='usage_privileges' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`
		s = fixture.Select(q, []string{"row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		s = fixture.Select("SELECT column_name,ordinal_position,is_nullable,domain_schema,domain_name FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='usage_privileges' ORDER BY ordinal_position", []string{"column_name", "ordinal_position", "is_nullable", "domain_schema", "domain_name"}, []string{"NAME", "INT4", "VARCHAR", "NAME", "NAME"}, columns)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	})
}
