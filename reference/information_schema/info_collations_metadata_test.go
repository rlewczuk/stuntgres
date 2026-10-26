package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCollationsMetadata(t *testing.T) {
	icoScenario(t, false, func(c *icoCase) {
		var attributes [][]any
		for i, name := range icoColumns {
			domain, base := "sql_identifier", int64(19)
			if i == 3 { domain, base = "character_data", 1043 }
			attributes = append(attributes, []any{int64(i+1), name, "information_schema", domain, "d", base, int64(-1), false, int64(950), int64(0), false})
		}
		s := fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.collations'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes)
		// Catalog OID cells use the common adapter's independently specified
		// OID transport expectations, not portable allocator equality.
		fixture.PgDescriptionExecutorRows(c.t, c.p, c.b, c.conn, s)
		fixture.PgDescriptionExecutorRows(c.t, c.p, c.b, c.conn, fixture.Prepared(s))
		fixture.PgDescriptionExecutorRows(c.t, c.p, c.b, c.hold(s.Query), s)
		s = fixture.Select(`SELECT c.relkind,t.typname,t.typrelid=c.oid AS same_relation,c.reltype=t.oid AS same_type,a.typelem=t.oid AS same_element,a.typnamespace=t.typnamespace AS same_namespace,n.nspname,a.typname AS array_name FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE c.oid='information_schema.collations'::regclass`, []string{"relkind", "typname", "same_relation", "same_type", "same_element", "same_namespace", "nspname", "array_name"}, []string{"CHAR", "NAME", "BOOL", "BOOL", "BOOL", "BOOL", "NAME", "NAME"}, [][]any{{"v", "collations", true, true, true, true, "information_schema", "_collations"}})
		c.routes(s)
	})
}
