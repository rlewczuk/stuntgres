package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCheckConstraintRoutineUsageShape(t *testing.T) {
	ccruScenario(t, func(c *ccruCase) {
		t := c.t
		f, _ := c.seed()
		c.exec("CREATE TABLE ccru_shape(v integer CONSTRAINT ccru_shape_ck CHECK(ccru_f(v)))")
		want := [][]any{c.row("public", "ccru_shape_ck", "public", f)}
		c.read(want...)
		held := c.hold(ccruQuery)
		held(c.step(ccruQuery, want))
		for _, predicate := range []string{"false", "constraint_name='ccru_missing'", "constraint_name=''", "constraint_name=NULL", "constraint_name IS NULL", "constraint_name=$1"} {
			q := "SELECT * FROM information_schema.check_constraint_routine_usage WHERE " + predicate
			var args []any
			if predicate == "constraint_name=$1" {
				args = []any{nil}
			}
			s := c.step(q, nil, args...)
			c.run(s)
			c.run(fixture.Prepared(s))
			c.hold(q)(s)
		}
		q := "SELECT * FROM information_schema.check_constraint_routine_usage WHERE constraint_name=$1"
		c.run(c.step(q, nil, ""))
		c.run(fixture.Prepared(c.step(q, nil, "")))
		q = "SELECT u.* FROM (SELECT 1 AS witness) w LEFT JOIN information_schema.check_constraint_routine_usage u ON u.constraint_name='ccru_missing'"
		s := c.step(q, [][]any{make([]any, 6)})
		s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
			values, pointers, targets := make([]sql.NullString, 6), make([]*string, 6), make([]any, 6)
			for i := range targets {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			for i := range targets {
				if values[i].Valid {
					t.Fatal("valid NULL", i)
				}
				targets[i] = &pointers[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			for i := range pointers {
				if pointers[i] != nil {
					t.Fatal("nonnull pointer", i)
				}
			}
		}
		c.run(s)
		c.run(fixture.Prepared(s))
		c.hold(q)(s)
		q = `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull,t.typtypmod FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='check_constraint_routine_usage' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
		var logical, columns [][]any
		for i, name := range ccruColumns {
			logical = append(logical, []any{int64(i + 1), name, "information_schema", "sql_identifier", "pg_catalog", "name", int64(19), int64(950), int64(-1), int64(0), false, false, int64(-1)})
			columns = append(columns, []any{name, int64(i + 1), "YES", "information_schema", "sql_identifier"})
		}
		s = fixture.Select(q, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL", "INT4"}, logical)
		c.run(s)
		c.run(fixture.Prepared(s))
		q = `SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='check_constraint_routine_usage' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`
		s = fixture.Select(q, []string{"row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}})
		c.run(s)
		c.run(fixture.Prepared(s))
		c.run(fixture.Select("SELECT column_name,ordinal_position,is_nullable,domain_schema,domain_name FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='check_constraint_routine_usage' ORDER BY ordinal_position", []string{"column_name", "ordinal_position", "is_nullable", "domain_schema", "domain_name"}, []string{"NAME", "INT4", "VARCHAR", "NAME", "NAME"}, columns))
		for _, query := range []string{ccruQuery, "SELECT * FROM information_schema.check_constraint_routine_usage WHERE false"} {
			rows, err := c.conn.QueryContext(c.p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			ccruMetadata(t, rows, c.b.PG)
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			ccruColumnMetadata(t, columns, c.b.PG)
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			ccruColumnMetadata(t, columns, c.b.PG)
		}
	})
}
