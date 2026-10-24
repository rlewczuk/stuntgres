package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func siiCatalogShape(c *siiCase) {
	c.t.Helper()
	var attributes, declared [][]any
	for i, name := range siiColumns {
		domain, datatype, base, collation := "character_data", "character varying", int64(1043), int64(950)
		if i == 2 {
			domain, datatype, base, collation = "cardinal_number", "integer", 23, 0
		}
		attributes = append(attributes, []any{int64(i + 1), name, "information_schema", domain, "d", base, int64(-1), false, collation, int64(0), false, int64(-1), false})
		declared = append(declared, []any{name, int64(i + 1), "YES", datatype, nil, "information_schema", domain, nil})
	}
	fixture.PgDescriptionExecutorRows(c.t, c.p, c.b, c.conn, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull,t.typtypmod,a.atthasdef FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.sql_implementation_info'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull", "typtypmod", "atthasdef"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL", "INT4", "BOOL"}, attributes))
	steps := []fixture.Step{
		fixture.Select(`SELECT column_name,ordinal_position,is_nullable,data_type,character_maximum_length,domain_schema,domain_name,column_default FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='sql_implementation_info' ORDER BY ordinal_position`, []string{"column_name", "ordinal_position", "is_nullable", "data_type", "character_maximum_length", "domain_schema", "domain_name", "column_default"}, []string{"NAME", "INT4", "VARCHAR", "VARCHAR", "INT4", "NAME", "NAME", "VARCHAR"}, declared),
		fixture.Select(`SELECT c.relkind,c.relhasindex,c.reltype>0 AS positive_type,c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.sql_implementation_info'::regclass`, []string{"relkind", "relhasindex", "positive_type", "same_type", "same_relation", "array_element", "array_link"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"r", false, true, true, true, true, true}}),
		fixture.Select(`SELECT table_type FROM information_schema.tables WHERE table_schema='information_schema' AND table_name='sql_implementation_info'`, []string{"table_type"}, []string{"VARCHAR"}, [][]any{{"BASE TABLE"}}),
		fixture.Select(`SELECT table_name FROM information_schema.views WHERE table_schema='information_schema' AND table_name='sql_implementation_info'`, []string{"table_name"}, []string{"NAME"}, nil),
	}
	for _, s := range steps {
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	}
}

func TestReferenceInfoSQLImplementationInfoIdentity(t *testing.T) {
	siiScenario(t, false, siiCatalogShape)
}

func TestReferenceInfoSQLImplementationInfoShape(t *testing.T) {
	siiScenario(t, false, func(c *siiCase) {
		for _, q := range []string{siiQuery, "SELECT * FROM " + siiTable, siiQuery + " WHERE FALSE", "SELECT * FROM " + siiTable + " WHERE FALSE", "SELECT integer_value FROM " + siiTable + " WHERE implementation_info_id='17'", "SELECT character_value FROM " + siiTable + " WHERE implementation_info_id='23'", "SELECT comments FROM " + siiTable + " WHERE implementation_info_id='17'"} {
			names := siiColumns
			want := c.want
			switch q {
			case "SELECT integer_value FROM " + siiTable + " WHERE implementation_info_id='17'":
				names, want = []string{"integer_value"}, [][]any{{nil}}
			case "SELECT character_value FROM " + siiTable + " WHERE implementation_info_id='23'":
				names, want = []string{"character_value"}, [][]any{{nil}}
			case "SELECT comments FROM " + siiTable + " WHERE implementation_info_id='17'":
				names, want = []string{"comments"}, [][]any{{nil}}
			case siiQuery + " WHERE FALSE", "SELECT * FROM " + siiTable + " WHERE FALSE":
				want = nil
			}
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, prepareErr := c.conn.PrepareContext(c.p.Ctx, q)
					if prepareErr != nil {
						c.t.Fatal(prepareErr)
					}
					rows, err = stmt.QueryContext(c.p.Ctx)
					defer stmt.Close()
				} else {
					rows, err = c.conn.QueryContext(c.p.Ctx, q)
				}
				if err != nil {
					c.t.Fatal(err)
				}
				columns, err := rows.ColumnTypes()
				if err != nil {
					rows.Close()
					c.t.Fatal(err)
				}
				siiMetadata(c.t, columns, names, c.b.PG)
				var got [][]any
				for rows.Next() {
					values, dest := make([]any, len(names)), make([]any, len(names))
					for i := range values {
						dest[i] = &values[i]
					}
					if err := rows.Scan(dest...); err != nil {
						rows.Close()
						c.t.Fatal(err)
					}
					for i, value := range values {
						c.projection(q, nil).CheckRaw(c.t, i, len(got), value)
						values[i] = fixture.Normalize(value)
					}
					got = append(got, values)
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					c.t.Fatal(err)
				}
				if diff, ok := fixture.CompareRows(got, want, true); !ok {
					c.t.Fatal(diff)
				}
				siiMetadata(c.t, columns, names, c.b.PG)
				if err := rows.Close(); err != nil {
					c.t.Fatal(err)
				}
				siiMetadata(c.t, columns, names, c.b.PG)
			}
		}
		for _, tc := range []struct {
			column, predicate string
			count             int
		}{
			{"integer_value", "implementation_info_id='17'", 1},
			{"character_value", "implementation_info_id='23'", 1},
			{"comments", "implementation_info_id='17'", 1},
			{"integer_value", "integer_value IS NULL", 7},
			{"character_value", "character_value IS NULL", 5},
			{"comments", "comments IS NULL", 6},
		} {
			want := make([][]any, tc.count)
			for i := range want {
				want[i] = []any{nil}
			}
			typ := "VARCHAR"
			if tc.column == "integer_value" {
				typ = "INT4"
			}
			s := fixture.Select("SELECT "+tc.column+" FROM "+siiTable+" WHERE "+tc.predicate, []string{tc.column}, []string{typ}, want)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				siiMetadata(t, columns, []string{tc.column}, c.b.PG)
			}
			c.run(c.conn, s)
			c.run(c.conn, fixture.Prepared(s))
		}
		for _, tc := range []struct {
			predicate string
			keep      func([]any) bool
		}{
			{"character_value=''", func(r []any) bool { return r[3] == "" }},
			{"integer_value=0", func(r []any) bool { return r[2] == int64(0) }},
			{"implementation_info_id IS NULL", func(r []any) bool { return false }},
			{"implementation_info_id=NULL", func(r []any) bool { return false }},
		} {
			c.check(c.conn, siiQuery+" WHERE "+tc.predicate, sfFilter(c.want, tc.keep))
		}
	})
}
