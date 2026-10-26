package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCheckConstraintsShape(t *testing.T) {
	iccScenario(t, func(c *iccCase) {
		c.exec("CREATE TABLE icc_shape(v integer CONSTRAINT icc_shape CHECK(NULL::boolean))")
		want := [][]any{c.row("public", "icc_shape", "NULL::boolean")}
		for _, projection := range []string{"*", "constraint_catalog,constraint_schema,constraint_name,check_clause"} {
			q := "SELECT " + projection + " FROM information_schema.check_constraints WHERE constraint_name='icc_shape'"
			s := c.step(q, want)
			c.run(s)
			c.run(fixture.Prepared(s))
			c.hold(q)(s)
		}
		for _, predicate := range []string{"false", "constraint_name='icc_missing'", "constraint_name=''", "constraint_name=NULL", "constraint_name IS NULL"} {
			q := "SELECT * FROM information_schema.check_constraints WHERE " + predicate
			s := c.step(q, nil)
			c.run(s)
			c.run(fixture.Prepared(s))
			c.hold(q)(s)
		}
		q := "SELECT * FROM information_schema.check_constraints WHERE constraint_name=$1"
		held := c.hold(q)
		for _, arg := range []any{nil, "", "icc_missing", "icc_shape"} {
			var rows [][]any
			if arg == "icc_shape" {
				rows = want
			}
			s := c.step(q, rows, arg)
			c.run(s)
			c.run(fixture.Prepared(s))
			held(s)
		}
		iccNullBoundary(c)
		iccLogicalShape(c)
		for _, query := range []string{iccQuery, "SELECT * FROM information_schema.check_constraints WHERE false"} {
			rows, err := c.conn.QueryContext(c.p.Ctx, query)
			if err != nil {
				c.t.Fatal(err)
			}
			columns, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				c.t.Fatal(err)
			}
			iccMetadata(c.t, columns, c.b.PG)
			for rows.Next() {
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				c.t.Fatal(err)
			}
			iccMetadata(c.t, columns, c.b.PG)
			if err := rows.Close(); err != nil {
				c.t.Fatal(err)
			}
			iccMetadata(c.t, columns, c.b.PG)
		}
	})
}

func iccNullBoundary(c *iccCase) {
	c.t.Helper()
	q := "SELECT u.* FROM (SELECT 1 AS witness) w LEFT JOIN information_schema.check_constraints u ON u.constraint_name='icc_missing'"
	s := c.step(q, [][]any{{nil, nil, nil, nil}})
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		var values [4]sql.NullString
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3]); err != nil {
			t.Fatal(err)
		}
		var pointers [4]*string
		if err := rows.Scan(&pointers[0], &pointers[1], &pointers[2], &pointers[3]); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			if values[i].Valid || pointers[i] != nil {
				t.Fatalf("column %d: SQL NULL became %v/%v", i, values[i], pointers[i])
			}
		}
	}
	c.run(s)
	c.run(fixture.Prepared(s))
	c.hold(q)(s)
}

func iccLogicalShape(c *iccCase) {
	c.t.Helper()
	q := `SELECT a.attnum,a.attname,tn.nspname,t.typname,bn.nspname,b.typname,b.oid::bigint,a.attcollation::bigint,a.atttypmod,a.attndims,a.attnotnull,t.typnotnull,t.typtypmod FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='check_constraints' JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace JOIN pg_type b ON b.oid=t.typbasetype JOIN pg_namespace bn ON bn.oid=b.typnamespace ORDER BY a.attnum`
	var logical, columns [][]any
	for i, name := range iccColumns {
		domain, base, oid := "sql_identifier", "name", int64(19)
		if i == 3 {
			domain, base, oid = "character_data", "varchar", 1043
		}
		logical = append(logical, []any{int64(i + 1), name, "information_schema", domain, "pg_catalog", base, oid, int64(950), int64(-1), int64(0), false, false, int64(-1)})
		columns = append(columns, []any{name, int64(i + 1), "YES", "information_schema", domain})
	}
	s := fixture.Select(q, []string{"attnum", "attname", "nspname", "typname", "nspname", "typname", "oid", "attcollation", "atttypmod", "attndims", "attnotnull", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "NAME", "NAME", "INT8", "INT8", "INT4", "INT2", "BOOL", "BOOL", "INT4"}, logical)
	c.run(s)
	c.run(fixture.Prepared(s))
	q = `SELECT c.reltype=t.oid AS row_link,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link,t.typnamespace=c.relnamespace AS row_namespace,a.typnamespace=t.typnamespace AS array_namespace,c.oid<>t.oid AS distinct_row,t.oid<>a.oid AS distinct_array FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='information_schema' AND c.relname='check_constraints' JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray`
	s = fixture.Select(q, []string{"row_link", "relation_link", "element_link", "array_link", "row_namespace", "array_namespace", "distinct_row", "distinct_array"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}})
	c.run(s)
	c.run(fixture.Prepared(s))
	s = fixture.Select("SELECT column_name,ordinal_position,is_nullable,domain_schema,domain_name FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='check_constraints' ORDER BY ordinal_position", []string{"column_name", "ordinal_position", "is_nullable", "domain_schema", "domain_name"}, []string{"NAME", "INT4", "VARCHAR", "NAME", "NAME"}, columns)
	c.run(s)
	c.run(fixture.Prepared(s))
}
