package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func szCatalogShape(c *szCase) {
	c.t.Helper()
	attributes := [][]any{
		{int64(1), "sizing_id", "information_schema", "cardinal_number", "d", int64(23), int64(-1), false, int64(0), int64(0), false, int64(-1), false},
		{int64(2), "sizing_name", "information_schema", "character_data", "d", int64(1043), int64(-1), false, int64(950), int64(0), false, int64(-1), false},
		{int64(3), "supported_value", "information_schema", "cardinal_number", "d", int64(23), int64(-1), false, int64(0), int64(0), false, int64(-1), false},
		{int64(4), "comments", "information_schema", "character_data", "d", int64(1043), int64(-1), false, int64(950), int64(0), false, int64(-1), false},
	}
	declared := [][]any{
		{"sizing_id", int64(1), "YES", "integer", nil, "information_schema", "cardinal_number", nil},
		{"sizing_name", int64(2), "YES", "character varying", nil, "information_schema", "character_data", nil},
		{"supported_value", int64(3), "YES", "integer", nil, "information_schema", "cardinal_number", nil},
		{"comments", int64(4), "YES", "character varying", nil, "information_schema", "character_data", nil},
	}
	fixture.PgDescriptionExecutorRows(c.t, c.p, c.b, c.conn, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull,t.typtypmod,a.atthasdef FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.sql_sizing'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull", "typtypmod", "atthasdef"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL", "INT4", "BOOL"}, attributes))
	c.run(c.conn, fixture.Select(`SELECT column_name,ordinal_position,is_nullable,data_type,character_maximum_length,domain_schema,domain_name,column_default FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='sql_sizing' ORDER BY ordinal_position`, []string{"column_name", "ordinal_position", "is_nullable", "data_type", "character_maximum_length", "domain_schema", "domain_name", "column_default"}, []string{"NAME", "INT4", "VARCHAR", "VARCHAR", "INT4", "NAME", "NAME", "VARCHAR"}, declared))
	c.run(c.conn, fixture.Select(`SELECT c.relkind,c.relhasindex,c.reltype>0 AS positive_type,c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.sql_sizing'::regclass`, []string{"relkind", "relhasindex", "positive_type", "same_type", "same_relation", "array_element", "array_link"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"r", false, true, true, true, true, true}}))
	c.run(c.conn, fixture.Select(`SELECT table_type FROM information_schema.tables WHERE table_schema='information_schema' AND table_name='sql_sizing'`, []string{"table_type"}, []string{"VARCHAR"}, [][]any{{"BASE TABLE"}}))
	c.run(c.conn, fixture.Select(`SELECT table_name FROM information_schema.views WHERE table_schema='information_schema' AND table_name='sql_sizing'`, []string{"table_name"}, []string{"NAME"}, nil))
}

// Retained ColumnTypes descriptors remain valid at EOF and after Close;
// database/sql does not allow fetching them again from a closed Rows.
func szConsume(c *szCase, rows *sql.Rows, names, types []string, want [][]any) []*sql.ColumnType {
	c.t.Helper()
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	szMetadata(c.t, columns, names, types, c.b.PG)
	var observed [][]any
	for rows.Next() {
		values, dest := make([]any, len(names)), make([]any, len(names))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			c.t.Fatal(err)
		}
		for i, value := range values {
			szRaw(c.t, types[i], value)
			values[i] = fixture.Normalize(value)
		}
		observed = append(observed, values)
	}
	if err := rows.Err(); err != nil {
		c.t.Fatal(err)
	}
	if _, ok := fixture.CompareRows(observed, want, true); !ok {
		c.t.Fatalf("rows=%#v want %#v", observed, want)
	}
	szMetadata(c.t, columns, names, types, c.b.PG)
	if err := rows.Close(); err != nil {
		c.t.Fatal(err)
	}
	szMetadata(c.t, columns, names, types, c.b.PG)
	return columns
}

func TestReferenceInfoSQLSizingShape(t *testing.T) {
	t.Run("catalog declaration", func(t *testing.T) { szScenario(t, false, szCatalogShape) })
	for _, boundary := range []string{"explicit", "star", "empty explicit", "empty star", "all NULL"} {
		for _, route := range []string{"direct", "prepared", "held"} {
			t.Run(boundary+"/"+route, func(t *testing.T) {
				szScenario(t, false, func(c *szCase) {
					q, names, types, want := szQuery, szColumns, szTypes, c.want
					switch boundary {
					case "star":
						q = "SELECT * FROM " + szTable
					case "empty explicit":
						q += " WHERE FALSE"
						want = nil
					case "empty star":
						q = "SELECT * FROM " + szTable + " WHERE FALSE"
						want = nil
					case "all NULL":
						q = "SELECT supported_value,comments FROM " + szTable + " WHERE sizing_id=0"
						names, types, want = []string{"supported_value", "comments"}, []string{"INT4", "VARCHAR"}, [][]any{{nil, nil}}
					}
					var e fixture.Executor = c.conn
					if route != "direct" {
						e = c.hold(q)
					}
					iterations := 1
					if route == "held" {
						iterations = 2
					}
					for range iterations {
						rows, err := e.QueryContext(c.p.Ctx, q)
						if err != nil {
							c.t.Fatal(err)
						}
						szConsume(c, rows, names, types, want)
					}
				})
			})
		}
	}
}
