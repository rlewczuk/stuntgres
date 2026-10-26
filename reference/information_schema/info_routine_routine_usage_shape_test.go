package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutineRoutineUsageShape(t *testing.T) {
	rruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		rruCheck(t, p, b, c, "specific_schema='public'", nil)
		rruCheck(t, p, b, c, "FALSE", nil)
		var attributes [][]any
		for i, name := range rruColumns {
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false})
		}
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.routine_routine_usage'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.routine_routine_usage'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		rruExec(t, p, b, c, "CREATE FUNCTION rru_shape_leaf() RETURNS integer RETURN 1", "CREATE FUNCTION rru_shape_use() RETURNS integer RETURN rru_shape_leaf()+rru_shape_leaf()")
		want := [][]any{rruRow(database, "public", rruID(t, p, c, "public", "rru_shape_use", ""), "public", rruID(t, p, c, "public", "rru_shape_leaf", ""))}
		rruCheck(t, p, b, c, "specific_schema='public'", want)
		for _, where := range []string{"specific_schema='public'", "FALSE"} {
			rows, err := c.QueryContext(p.Ctx, rruProjection(b.PG, where, nil).Query)
			if err != nil {
				t.Fatal(err)
			}
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			rruMetadata(t, columns, b.PG)
			count := 0
			for rows.Next() {
				var a, b, c, d, e, f any
				if err := rows.Scan(&a, &b, &c, &d, &e, &f); err != nil {
					t.Fatal(err)
				}
				count++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			rruMetadata(t, columns, b.PG)
			expected := 1
			if where == "FALSE" {
				expected = 0
			}
			if count != expected {
				t.Fatal(count, expected)
			}
		}
		for _, prefix := range []string{strings.Repeat("a", 56), strings.Repeat("界", 18)} {
			leaf, use := prefix+"_leaf", prefix+"_use"
			rruExec(t, p, b, c, `CREATE FUNCTION "`+leaf+`"(integer) RETURNS integer RETURN 1`, `CREATE FUNCTION "`+leaf+`"(bigint) RETURNS integer RETURN 2`, `CREATE FUNCTION "`+use+`"() RETURNS integer RETURN "`+leaf+`"(1)`)
			id := rruID(t, p, c, "public", use, "")
			rruCheck(t, p, b, c, "specific_name='"+id+"'", [][]any{rruRow(database, "public", id, "public", rruID(t, p, c, "public", leaf, "integer"))})
		}
	})
}
