package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoElementTypesShape(t *testing.T) {
	etScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		etExec(t, p, b, c, "CREATE TABLE et_shape(a integer[],s integer)")
		want := [][]any{etRow(database, "public", "et_shape", "TABLE", 1, "integer", "pg_catalog", "int4")}
		for _, s := range []fixture.Step{
			etProjection(b.PG, "object_schema='public' AND object_name='et_shape'", want),
			etProjection(b.PG, "object_name='et_absent'", nil),
			etProjection(b.PG, "FALSE", nil),
			etProjection(b.PG, "NULL", nil),
			etProjection(b.PG, "object_name IS NULL", nil),
			etProjection(b.PG, "object_name=$1", nil),
		} {
			if s.Query == "SELECT e.* FROM information_schema.element_types e WHERE object_name=$1" {
				s.Args = []any{nil}
			}
			etCheck(t, p, b, c, s)
			rows, err := c.QueryContext(p.Ctx, s.Query, s.Args...)
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			etMetadata(t, types, b.PG)
			etConsume(t, rows, types, b.PG, s.Rows)
		}
		left := etProjection(b.PG, "TRUE", [][]any{make([]any, 28)})
		left.Query = "SELECT e.* FROM (SELECT 1 AS n) x LEFT JOIN information_schema.element_types e ON e.object_name='et_absent'"
		etCheck(t, p, b, c, left)

		var attributes [][]any
		for i, name := range etColumns {
			base, collation := int64(19), int64(950)
			switch etDomains[i] {
			case "character_data":
				base = 1043
			case "cardinal_number":
				base, collation = 23, 0
			}
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", etDomains[i], "d", base, int64(-1), false, collation, int64(0), false, int64(-1)})
		}
		descriptor := fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull,t.typtypmod FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.element_types'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull", "typtypmod"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL", "INT4"}, attributes)
		fixture.PgDescriptionExecutorRows(t, p, b, c, descriptor)
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(descriptor))
		etCheck(t, p, b, c, fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.element_types'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		etCheck(t, p, b, c, fixture.Select(`SELECT c.column_name,e.collection_type_identifier,e.dtd_identifier FROM information_schema.columns c JOIN information_schema.element_types e ON e.object_catalog=c.table_catalog AND e.object_schema=c.table_schema AND e.object_name=c.table_name AND e.object_type='TABLE' AND e.collection_type_identifier=c.dtd_identifier WHERE c.table_schema='public' AND c.table_name='et_shape' ORDER BY c.ordinal_position`, []string{"column_name", "collection_type_identifier", "dtd_identifier"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"a", "1", "a1"}}))

		// Columns and their returned metadata remain detached consumer values.
		rows, err := c.QueryContext(p.Ctx, etProjection(b.PG, "object_name='et_shape'", want).Query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		names, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(names, etColumns) {
			t.Fatalf("columns=%v want=%v", names, etColumns)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		etConsume(t, rows, types, b.PG, want)
		if !reflect.DeepEqual(names, etColumns) {
			t.Fatalf("retained columns=%v want=%v", names, etColumns)
		}
	})
}
