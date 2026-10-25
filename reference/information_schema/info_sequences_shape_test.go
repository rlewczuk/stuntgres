package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoSequencesDefinitions(t *testing.T) {
	isqScenario(t, func(c *isqCase) {
		held := c.hold(c.conn, isqQuery)
		c.all(held, "isq_def_%")
		c.exec("CREATE SEQUENCE isq_def_default", "CREATE SEQUENCE isq_def_desc AS SMALLINT INCREMENT -1", `CREATE SEQUENCE "isq_def_MiXeD_λ" AS INTEGER START 15 MINVALUE -20 MAXVALUE 200 INCREMENT 7 CYCLE`)
		c.all(held, "isq_def_%", c.def("public", "isq_def_default"), c.row("public", "isq_def_desc", "smallint", 16, "-1", "-32768", "-1", "-1", "NO"), c.custom("public", "isq_def_MiXeD_λ"))
	})
}

func TestReferenceInfoSequencesMetadata(t *testing.T) {
	isqScenario(t, func(c *isqCase) {
		c.exec("CREATE SEQUENCE isq_meta AS INTEGER START 15 MINVALUE -20 MAXVALUE 200 INCREMENT 7 CYCLE")
		held := c.hold(c.conn, isqQuery)
		c.all(held, "isq_meta", c.custom("public", "isq_meta"))
		equality := "SELECT * FROM information_schema.sequences WHERE sequence_name=$1"
		equalityHeld := c.hold(c.conn, equality)
		for _, argument := range []any{"isq_meta", nil} {
			var want [][]any
			if argument != nil {
				want = [][]any{c.custom("public", "isq_meta")}
			}
			s := c.step(argument, want)
			s.Query = equality
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(equalityHeld, s)
			rows, err := equalityHeld.QueryContext(c.p.Ctx, equality, argument)
			if err != nil {
				c.t.Fatal(err)
			}
			isqConsume(c.t, rows, c.b.PG, want)
		}
		for _, q := range []string{isqQuery, "SELECT * FROM information_schema.sequences WHERE false"} {
			for _, prepare := range []bool{false, true} {
				var e fixture.Executor = c.conn
				if prepare {
					e = c.hold(c.conn, q)
				}
				if q == isqQuery {
					for _, pattern := range []any{"isq_meta", "isq_meta_missing", nil} {
						rows, err := e.QueryContext(c.p.Ctx, q, pattern)
						if err != nil {
							c.t.Fatal(err)
						}
						var want [][]any
						if pattern == "isq_meta" {
							want = [][]any{c.custom("public", "isq_meta")}
						}
						isqConsume(c.t, rows, c.b.PG, want)
					}
				} else {
					rows, err := e.QueryContext(c.p.Ctx, q)
					if err != nil {
						c.t.Fatal(err)
					}
					isqConsume(c.t, rows, c.b.PG, nil)
				}
			}
		}
		q := `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull,t.typtypmod FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='sequences' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
		want := make([][]any, 12)
		for i, name := range isqColumns {
			domain, base, oid, collation, modifier := "character_data", "varchar", int64(1043), int64(950), int64(-1)
			if i < 3 {
				domain, base, oid = "sql_identifier", "name", 19
			}
			if i >= 4 && i <= 6 {
				domain, base, oid, collation = "cardinal_number", "int4", 23, 0
			}
			if i == 11 {
				domain, modifier = "yes_or_no", 7
			}
			want[i] = []any{int64(i + 1), name, "information_schema", domain, "pg_catalog", base, oid, collation, int64(-1), int64(0), false, false, modifier}
		}
		s := fixture.Select(q, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL", "INT4"}, want)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q = `SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='sequences' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`
		c.run(c.conn, fixture.Select(q, []string{"row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}}))
	})
}

func TestReferenceInfoSequencesNullBoundaries(t *testing.T) {
	isqScenario(t, func(c *isqCase) {
		c.exec("CREATE SEQUENCE isq_null")
		held := c.hold(c.conn, isqQuery)
		c.all(held, nil)
		c.all(held, "isq_null", c.def("public", "isq_null"))
		s := c.step(nil, nil)
		s.Query = "SELECT * FROM information_schema.sequences WHERE sequence_name IS NULL"
		s.Args = nil
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q := "SELECT s.* FROM (SELECT 1 AS witness) w LEFT JOIN information_schema.sequences s ON s.sequence_name='isq_null_missing'"
		s = fixture.Select(q, isqColumns, isqTypes, [][]any{make([]any, 12)})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { isqMetadata(t, rows, c.b.PG) }
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	})
}
