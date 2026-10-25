package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnColumnUsageMetadata(t *testing.T) {
	colcuScenario(t, func(c *colcuCase) {
		c.check(c.conn, "colcu_usage")
		c.exec(colcuSeed)
		held := c.hold(c.conn, colcuQuery)
		want := c.seedRows()
		c.check(c.conn, "colcu_usage", want...)
		c.check(held, "colcu_usage", want...)
		for _, pattern := range []string{"colcu_usage", "colcu_missing"} {
			for _, e := range []fixture.Executor{c.conn, held} {
				rows, err := e.QueryContext(c.p.Ctx, colcuQuery, pattern)
				if err != nil {
					c.t.Fatal(err)
				}
				saved := colcuMetadata(c.t, rows, c.b.PG)
				var expected [][]any
				if pattern == "colcu_usage" {
					expected = want
				}
				colcuAssertRows(c.t, rows, expected)
				colcuColumnTypes(c.t, saved, c.b.PG)
				if err := rows.Close(); err != nil {
					c.t.Fatal(err)
				}
				colcuColumnTypes(c.t, saved, c.b.PG)
			}
		}
		q := `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='column_column_usage' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
		identity := make([][]any, 5)
		for i, name := range colcuColumns {
			identity[i] = []any{int64(i + 1), name, "information_schema", "sql_identifier", "pg_catalog", "name", int64(19), int64(950), int64(-1), int64(0), false, false}
		}
		s := fixture.Select(q, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL"}, identity)
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
		q = `SELECT c.relkind,c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='column_column_usage' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`
		s = fixture.Select(q, []string{"relkind", "row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", true, true, true, true, true, true, true, true}})
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	})
}

func TestReferenceInfoColumnColumnUsagePopulation(t *testing.T) {
	colcuScenario(t, func(c *colcuCase) {
		c.exec(colcuSeed,
			"CREATE TABLE colcu_negative(a integer DEFAULT 3,b integer GENERATED ALWAYS AS IDENTITY,CHECK(a>0))",
			"CREATE INDEX colcu_negative_index ON colcu_negative(a)",
			"CREATE VIEW colcu_negative_view AS SELECT a FROM colcu_negative",
			"CREATE FUNCTION colcu_negative_function() RETURNS integer BEGIN ATOMIC SELECT a FROM colcu_negative; END")
		want := c.seedRows()
		held := c.hold(c.conn, colcuQuery)
		c.check(c.conn, "colcu_%", want...)
		c.check(held, "colcu_%", want...)
		for _, boundary := range []struct {
			predicate string
			arg       any
			rows      [][]any
		}{
			{"table_name LIKE $1", nil, nil},
			{"table_name LIKE $1", "", nil},
			{"table_name LIKE $1", "colcu_absent", nil},
			{"table_name LIKE $1 AND column_name IS NULL", "colcu_usage", nil},
			{"table_name LIKE $1 AND column_name IS NOT NULL", "colcu_usage", want},
			{"table_name LIKE $1 AND column_name=NULL", "colcu_usage", nil},
			{"table_name LIKE 'colcu_usage' AND column_name=$1", nil, nil},
			{"table_name LIKE 'colcu_usage' AND column_name=$1", "", nil},
		} {
			s := c.step(boundary.arg, boundary.rows)
			s.Query = strings.Replace(colcuQuery, "table_name LIKE $1", boundary.predicate, 1)
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
			c.run(c.hold(c.conn, s.Query), s)
		}
		c.exec("DROP TABLE colcu_usage")
		c.check(c.conn, "colcu_%")
		c.check(held, "colcu_%")
	})
}
