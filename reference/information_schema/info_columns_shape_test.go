package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnsShape(t *testing.T) {
	icScenario(t, func(c *icCase) {
		t := c.t
		var attributes, self [][]any
		for i, name := range icColumns {
			domain, base, collation, dataType, udt := "sql_identifier", int64(19), int64(950), "name", "name"
			fields := map[string]any{"domain_catalog": c.database, "domain_schema": "information_schema", "domain_name": domain, "is_updatable": "NO"}
			switch icDomains[i] {
			case "N":
				domain, base, collation, dataType, udt = "cardinal_number", 23, 0, "integer", "int4"
				for k, v := range icIntFields() {
					fields[k] = v
				}
			case "C":
				domain, base, dataType, udt = "character_data", 1043, "character varying", "varchar"
				fields["character_octet_length"] = int64(1073741824)
			case "Y":
				domain, base, dataType, udt = "yes_or_no", 1043, "character varying", "varchar"
				fields["character_maximum_length"], fields["character_octet_length"] = int64(3), int64(12)
			}
			fields["domain_name"] = domain
			// Domain collations are C, not the database's default collation.
			if collation != 0 {
				fields["collation_catalog"], fields["collation_schema"], fields["collation_name"] = c.database, "pg_catalog", "C"
			}
			attributes = append(attributes, []any{int64(i + 1), name, "information_schema", domain, "d", base, int64(-1), false, collation, int64(0), false})
			self = append(self, icRow(c.database, "information_schema", "columns", name, i+1, dataType, "pg_catalog", udt, fields))
		}
		fixture.PgDescriptionExecutorRows(t, c.p, c.b, c.conn, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.columns'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes))
		c.run(c.conn, fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.columns'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		c.run(c.conn, icProjection(c.b.PG, "table_schema='information_schema' AND table_name='columns'", self))
		c.exec("CREATE TABLE ic_scope.shape(a integer NOT NULL DEFAULT 7)")
		ordinary := icPatch(c.integer("shape", "a", 1), map[string]any{"is_nullable": "NO", "column_default": "7"})
		for _, where := range []string{"table_schema='ic_scope' AND table_name='shape'", "table_schema='ic_absent'", "table_schema='ic_scope' AND table_name='absent'", "FALSE", "NULL::boolean", "column_name IS NULL"} {
			var want [][]any
			if where == "table_schema='ic_scope' AND table_name='shape'" {
				want = [][]any{ordinary}
			}
			for _, star := range []bool{false, true} {
				s := icProjection(c.b.PG, where, want)
				if star {
					s.Query = strings.Replace(s.Query, strings.Join(icColumns, ","), "*", 1)
				}
				for _, prepared := range []bool{false, true} {
					s.Prepared = prepared
					c.run(c.conn, s)
					rows, closeQuery, err := fixture.TransactionQuery(c.p.Ctx, c.conn, s.Query, prepared)
					if err != nil {
						t.Fatal(err)
					}
					c.consume(rows, want)
					if err := closeQuery(); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		for _, star := range []bool{false, true} {
			s := icProjection(c.b.PG, "table_schema='ic_scope' AND table_name=$1", nil)
			if star {
				s.Query = strings.Replace(s.Query, strings.Join(icColumns, ","), "*", 1)
			}
			held := c.hold(s.Query)
			for _, arg := range []any{"shape", "absent", nil} {
				s.Args, s.Rows = []any{arg}, nil
				if arg == "shape" {
					s.Rows = [][]any{ordinary}
				}
				c.run(c.conn, s)
				c.run(c.conn, fixture.Prepared(s))
				c.run(held, s)
			}
		}
	})
}
