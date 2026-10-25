package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func sfCatalogShape(c *sfCase) {
	c.t.Helper()
	var attributes [][]any
	var declared [][]any
	for i, name := range fixture.SFColumns {
		domain, baseMod := "character_data", int64(-1)
		var maximum any
		if i == 4 {
			domain, baseMod, maximum = "yes_or_no", 7, int64(3)
		}
		attributes = append(attributes, []any{int64(i + 1), name, "information_schema", domain, "d", int64(1043), int64(-1), false, int64(950), int64(0), false, baseMod, false})
		declared = append(declared, []any{name, int64(i + 1), "YES", "character varying", maximum, "information_schema", domain, nil})
	}
	fixture.PgDescriptionExecutorRows(c.t, c.p, c.b, c.conn, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull,t.typtypmod,a.atthasdef FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.sql_features'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull", "typtypmod", "atthasdef"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL", "INT4", "BOOL"}, attributes))
	c.run(c.conn, fixture.Select(`SELECT column_name,ordinal_position,is_nullable,data_type,character_maximum_length,domain_schema,domain_name,column_default FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='sql_features' ORDER BY ordinal_position`, []string{"column_name", "ordinal_position", "is_nullable", "data_type", "character_maximum_length", "domain_schema", "domain_name", "column_default"}, []string{"NAME", "INT4", "VARCHAR", "VARCHAR", "INT4", "NAME", "NAME", "VARCHAR"}, declared))
	c.run(c.conn, fixture.Select(`SELECT c.relkind,c.relhasindex,c.reltype>0 AS positive_type,c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.sql_features'::regclass`, []string{"relkind", "relhasindex", "positive_type", "same_type", "same_relation", "array_element", "array_link"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"r", false, true, true, true, true, true}}))
	c.run(c.conn, fixture.Select(`SELECT table_type FROM information_schema.tables WHERE table_schema='information_schema' AND table_name='sql_features'`, []string{"table_type"}, []string{"VARCHAR"}, [][]any{{"BASE TABLE"}}))
	c.run(c.conn, fixture.Select(`SELECT table_name FROM information_schema.views WHERE table_schema='information_schema' AND table_name='sql_features'`, []string{"table_name"}, []string{"NAME"}, nil))
}

func TestReferenceInfoSQLFeaturesShape(t *testing.T) {
	t.Run("catalog declaration", func(t *testing.T) { sfScenario(t, false, sfCatalogShape) })
	for _, star := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			for _, boundary := range []string{"populated", "empty", "all NULL"} {
				name := boundary + "/explicit/direct"
				if star {
					name = boundary + "/star/direct"
				}
				if prepared {
					name += "/prepared"
				}
				t.Run(name, func(t *testing.T) {
					sfScenario(t, false, func(c *sfCase) {
						t := c.t
						q, names, want := sfQuery, fixture.SFColumns, c.release
						if star {
							q = "SELECT * FROM " + sfTable
						}
						if boundary == "empty" {
							q += " WHERE FALSE"
							want = nil
						}
						if boundary == "all NULL" {
							q = "SELECT is_verified_by FROM " + sfTable
							names = []string{"is_verified_by"}
						}
						var stmt *sql.Stmt
						var rows *sql.Rows
						var err error
						if prepared {
							stmt, err = c.conn.PrepareContext(c.p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							rows, err = stmt.QueryContext(c.p.Ctx)
						} else {
							rows, err = c.conn.QueryContext(c.p.Ctx, q)
						}
						if err != nil {
							t.Fatal(err)
						}
						defer rows.Close()
						columns, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						sfMetadata(t, columns, names, c.b.PG)
						var observed [][]any
						for rows.Next() {
							values := make([]any, len(names))
							dest := make([]any, len(names))
							for i := range values {
								dest[i] = &values[i]
							}
							if err := rows.Scan(dest...); err != nil {
								t.Fatal(err)
							}
							for i, value := range values {
								column := i
								if boundary == "all NULL" {
									column = 5
								}
								c.projection(q, nil).CheckRaw(t, column, len(observed), value)
								values[i] = fixture.Normalize(value)
							}
							observed = append(observed, values)
						}
						if err := rows.Err(); err != nil {
							t.Fatal(err)
						}
						if boundary == "all NULL" {
							want = make([][]any, 755)
							for i := range want {
								want[i] = []any{nil}
							}
						}
						if _, ok := fixture.CompareRows(observed, want, true); !ok {
							t.Fatalf("metadata cursor returned unexpected rows: count=%d want=%d", len(observed), len(want))
						}
						sfMetadata(t, columns, names, c.b.PG)
						if err := rows.Close(); err != nil {
							t.Fatal(err)
						}
						sfMetadata(t, columns, names, c.b.PG)
					})
				})
			}
		}
	}
}
