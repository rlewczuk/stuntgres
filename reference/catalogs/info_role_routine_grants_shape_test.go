package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleRoutineGrantsShape(t *testing.T) {
	rrgScenario(t, func(c *rrgCase) {
		c.seed()
		want := c.baseline("public.rrg_f()")
		held := c.hold(c.conn, rrgQuery)
		c.check(c.conn, "public", "rrg_f", want...)
		c.check(held, "public", "rrg_f", want...)
		rows, err := held.QueryContext(c.p.Ctx, rrgQuery, "public", "rrg_f")
		if err != nil {
			c.t.Fatal(err)
		}
		rrgConsume(c.t, rows, c.b.PG, want)
		q := "SELECT " + rrgProjection + " FROM information_schema.role_routine_grants WHERE routine_schema=$1 AND routine_name=$2"
		c.run(c.conn, c.step(q, want, "public", "rrg_f"))
		c.run(c.conn, fixture.Prepared(c.step(q, want, "public", "rrg_f")))
		for _, q := range []string{
			"SELECT * FROM information_schema.role_routine_grants WHERE false",
			"SELECT * FROM information_schema.role_routine_grants WHERE routine_name=NULL",
			"SELECT * FROM information_schema.role_routine_grants WHERE routine_name='rrg_missing'",
			"SELECT * FROM information_schema.role_routine_grants WHERE routine_name IS NULL",
			"SELECT * FROM information_schema.role_routine_grants WHERE routine_name=$1",
			"SELECT * FROM information_schema.role_routine_grants WHERE routine_name=''",
		} {
			var args []any
			if q == "SELECT * FROM information_schema.role_routine_grants WHERE routine_name=$1" {
				args = []any{nil}
			}
			s := c.step(q, nil, args...)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			boundary := c.hold(c.conn, q)
			c.run(boundary, s)
			rows, err := boundary.QueryContext(c.p.Ctx, q, args...)
			if err != nil {
				c.t.Fatal(err)
			}
			rrgConsume(c.t, rows, c.b.PG, nil)
		}
		q = "SELECT g.* FROM (SELECT 1 AS witness) w LEFT JOIN information_schema.role_routine_grants g ON g.routine_name='rrg_missing'"
		s := fixture.Select(q, rrgColumns, rrgTypes, [][]any{make([]any, 10)})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { rrgMetadata(t, rows, c.b.PG) }
		s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
			if value != nil {
				t.Fatalf("NULL extension %d,%d=%#v", row, column, value)
			}
			return false
		}
		s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
			nullable := make([]sql.NullString, 10)
			ptrs := make([]*string, 10)
			targets := make([]any, 10)
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
		q = `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull,t.typtypmod FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='role_routine_grants' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
		logical := make([][]any, 10)
		for i, name := range rrgColumns {
			domain, base, oid, modifier := "sql_identifier", "name", int64(19), int64(-1)
			if i == 8 {
				domain, base, oid = "character_data", "varchar", 1043
			}
			if i >= 9 {
				domain, base, oid, modifier = "yes_or_no", "varchar", 1043, 7
			}
			logical[i] = []any{int64(i + 1), name, "information_schema", domain, "pg_catalog", base, oid, int64(950), int64(-1), int64(0), false, false, modifier}
		}
		s = fixture.Select(q, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL", "INT4"}, logical)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q = `SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='role_routine_grants' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`
		s = fixture.Select(q, []string{"row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		var columns [][]any
		for i, name := range rrgColumns {
			columns = append(columns, []any{name, int64(i + 1), "YES", "information_schema", []string{"sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "character_data", "yes_or_no"}[i]})
		}
		s = fixture.Select("SELECT column_name,ordinal_position,is_nullable,domain_schema,domain_name FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='role_routine_grants' ORDER BY ordinal_position", []string{"column_name", "ordinal_position", "is_nullable", "domain_schema", "domain_name"}, []string{"NAME", "INT4", "VARCHAR", "NAME", "NAME"}, columns)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	})
}
