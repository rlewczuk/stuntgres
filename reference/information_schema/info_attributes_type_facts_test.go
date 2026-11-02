package reference_test

import "testing"

func TestReferenceInfoAttributesTypeFacts(t *testing.T) {
	t.Run("domains", func(t *testing.T) {
		attrScenario(t, func(c *attrCase) {
			c.exec("CREATE SCHEMA attr_scope", "CREATE DOMAIN attr_scope.required AS integer NOT NULL DEFAULT 9", "CREATE DOMAIN attr_scope.label AS varchar(4) DEFAULT 'x'", "CREATE DOMAIN attr_scope.numbers AS integer[]", "CREATE TYPE attr_scope.domains AS (required attr_scope.required,label attr_scope.label,numbers attr_scope.numbers)")
			want := [][]any{
				attrPatch(attrRow(c.database, "attr_scope", "domains", "required", 1, "USER-DEFINED", "attr_scope", "required"), map[string]any{"is_nullable": "NO", "numeric_precision": int64(32), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)}),
				attrPatch(attrRow(c.database, "attr_scope", "domains", "label", 2, "USER-DEFINED", "attr_scope", "label"), map[string]any{"character_maximum_length": int64(4), "character_octet_length": int64(16)}),
				attrRow(c.database, "attr_scope", "domains", "numbers", 3, "USER-DEFINED", "attr_scope", "numbers"),
			}
			where := "udt_schema='attr_scope' AND udt_name='domains'"
			c.check(where, want...)
			c.held(c.hold(where), where, nil, want...)
		})
	})
	t.Run("dimensions", func(t *testing.T) {
		attrScenario(t, func(c *attrCase) {
			c.exec("CREATE SCHEMA attr_scope", `CREATE TYPE attr_scope.dimensions AS (s smallint,b bigint,r real,d double precision,n numeric,f numeric(8,2),t text,v varchar,c char(3),bits bit(5),varying varbit(6),flag boolean,day date,stamp timestamp(2),clock time(3),zone timestamptz,span interval DAY TO SECOND(3),plain text,collated text COLLATE pg_catalog."C")`)
			var want [][]any
			add := func(member, dataType, typeName string, facts map[string]any) {
				want = append(want, attrPatch(attrRow(c.database, "attr_scope", "dimensions", member, int64(len(want)+1), dataType, "pg_catalog", typeName), facts))
			}
			add("s", "smallint", "int2", map[string]any{"numeric_precision": int64(16), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)})
			add("b", "bigint", "int8", map[string]any{"numeric_precision": int64(64), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)})
			add("r", "real", "float4", map[string]any{"numeric_precision": int64(24), "numeric_precision_radix": int64(2)})
			add("d", "double precision", "float8", map[string]any{"numeric_precision": int64(53), "numeric_precision_radix": int64(2)})
			add("n", "numeric", "numeric", map[string]any{"numeric_precision_radix": int64(10)})
			add("f", "numeric", "numeric", map[string]any{"numeric_precision": int64(8), "numeric_precision_radix": int64(10), "numeric_scale": int64(2)})
			add("t", "text", "text", map[string]any{"character_octet_length": int64(1073741824)})
			add("v", "character varying", "varchar", map[string]any{"character_octet_length": int64(1073741824)})
			add("c", "character", "bpchar", map[string]any{"character_maximum_length": int64(3), "character_octet_length": int64(12)})
			add("bits", "bit", "bit", map[string]any{"character_maximum_length": int64(5)})
			add("varying", "bit varying", "varbit", map[string]any{"character_maximum_length": int64(6)})
			add("flag", "boolean", "bool", nil)
			add("day", "date", "date", map[string]any{"datetime_precision": int64(0)})
			add("stamp", "timestamp without time zone", "timestamp", map[string]any{"datetime_precision": int64(2)})
			add("clock", "time without time zone", "time", map[string]any{"datetime_precision": int64(3)})
			add("zone", "timestamp with time zone", "timestamptz", map[string]any{"datetime_precision": int64(6)})
			add("span", "interval", "interval", map[string]any{"datetime_precision": int64(3), "interval_type": "DAY TO SECOND(3)"})
			add("plain", "text", "text", map[string]any{"character_octet_length": int64(1073741824)})
			add("collated", "text", "text", map[string]any{"character_octet_length": int64(1073741824), "collation_catalog": c.database, "collation_schema": "pg_catalog", "collation_name": "C"})
			where := "udt_schema='attr_scope' AND udt_name='dimensions'"
			c.check(where, want...)
			c.held(c.hold(where), where, nil, want...)
		})
	})
}
