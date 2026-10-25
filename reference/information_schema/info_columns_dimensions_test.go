package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnsDimensions(t *testing.T) {
	cases := []struct {
		decl, dataType, udt string
		fields              map[string]any
	}{
		{"smallint", "smallint", "int2", map[string]any{"numeric_precision": int64(16), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)}},
		{"integer", "integer", "int4", icIntFields()},
		{"bigint", "bigint", "int8", map[string]any{"numeric_precision": int64(64), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)}},
		{"real", "real", "float4", map[string]any{"numeric_precision": int64(24), "numeric_precision_radix": int64(2)}},
		{"double precision", "double precision", "float8", map[string]any{"numeric_precision": int64(53), "numeric_precision_radix": int64(2)}},
		{"numeric(12,3)", "numeric", "numeric", map[string]any{"numeric_precision": int64(12), "numeric_precision_radix": int64(10), "numeric_scale": int64(3)}},
		{"numeric", "numeric", "numeric", map[string]any{"numeric_precision_radix": int64(10)}},
		// The released helper exposes unsigned scale bits, deliberately not
		// the signed -2 used for numeric value coercion.
		{"numeric(5,-2)", "numeric", "numeric", map[string]any{"numeric_precision": int64(5), "numeric_precision_radix": int64(10), "numeric_scale": int64(2046)}},
		{"numeric(3,5)", "numeric", "numeric", map[string]any{"numeric_precision": int64(3), "numeric_precision_radix": int64(10), "numeric_scale": int64(5)}},
		{"text", "text", "text", map[string]any{"character_octet_length": int64(1073741824)}},
		{"varchar", "character varying", "varchar", map[string]any{"character_octet_length": int64(1073741824)}},
		{"varchar(7)", "character varying", "varchar", map[string]any{"character_maximum_length": int64(7), "character_octet_length": int64(28)}},
		{"char(3)", "character", "bpchar", map[string]any{"character_maximum_length": int64(3), "character_octet_length": int64(12)}},
		{"bpchar", "character", "bpchar", map[string]any{"character_octet_length": int64(1073741824)}},
		{"bit(5)", "bit", "bit", map[string]any{"character_maximum_length": int64(5)}},
		{"bit varying(9)", "bit varying", "varbit", map[string]any{"character_maximum_length": int64(9)}},
		{"bit varying", "bit varying", "varbit", nil},
		{"date", "date", "date", map[string]any{"datetime_precision": int64(0)}},
		{"time", "time without time zone", "time", map[string]any{"datetime_precision": int64(6)}},
		{"time(1) with time zone", "time with time zone", "timetz", map[string]any{"datetime_precision": int64(1)}},
		{"timestamp", "timestamp without time zone", "timestamp", map[string]any{"datetime_precision": int64(6)}},
		{"timestamp(2)", "timestamp without time zone", "timestamp", map[string]any{"datetime_precision": int64(2)}},
		{"timestamp with time zone", "timestamp with time zone", "timestamptz", map[string]any{"datetime_precision": int64(6)}},
		{"interval", "interval", "interval", map[string]any{"datetime_precision": int64(6)}},
		{"interval day to second(3)", "interval", "interval", map[string]any{"datetime_precision": int64(3), "interval_type": "DAY TO SECOND(3)"}},
		{"boolean", "boolean", "bool", nil},
		{"bytea", "bytea", "bytea", nil},
		{"json", "json", "json", nil},
		{"jsonb", "jsonb", "jsonb", nil},
		{"uuid", "uuid", "uuid", nil},
		{"integer[]", "ARRAY", "_int4", nil},
		{"varchar(2)[]", "ARRAY", "_varchar", nil},
		{"int4range", "int4range", "int4range", nil},
		{"int4multirange", "int4multirange", "int4multirange", nil},
		{"inet", "inet", "inet", nil},
		{"cidr", "cidr", "cidr", nil},
		{"macaddr", "macaddr", "macaddr", nil},
		{"macaddr8", "macaddr8", "macaddr8", nil},
		{"money", "money", "money", nil},
		{"point", "point", "point", nil},
		{"line", "line", "line", nil},
		{"lseg", "lseg", "lseg", nil},
		{"box", "box", "box", nil},
		{"path", "path", "path", nil},
		{"polygon", "polygon", "polygon", nil},
		{"circle", "circle", "circle", nil},
		{"name", "name", "name", nil},
		{"oid", "oid", "oid", nil},
		{"regclass", "regclass", "regclass", nil},
		{"tsvector", "tsvector", "tsvector", nil},
		{"tsquery", "tsquery", "tsquery", nil},
		{"tid", "tid", "tid", nil},
		{"xid", "xid", "xid", nil},
		{"cid", "cid", "cid", nil},
		{"xid8", "xid8", "xid8", nil},
		{"jsonpath", "jsonpath", "jsonpath", nil},
		{"pg_node_tree", "pg_node_tree", "pg_node_tree", nil},
		{"aclitem", "aclitem", "aclitem", nil},
		{"regtype", "regtype", "regtype", nil},
		{"regproc", "regproc", "regproc", nil},
		{"regrole", "regrole", "regrole", nil},
		{"regoper", "regoper", "regoper", nil},
		{"regoperator", "regoperator", "regoperator", nil},
		{"regprocedure", "regprocedure", "regprocedure", nil},
		{"regcollation", "regcollation", "regcollation", nil},
		{"regnamespace", "regnamespace", "regnamespace", nil},
		{"regconfig", "regconfig", "regconfig", nil},
		{"regdictionary", "regdictionary", "regdictionary", nil},
		{"numrange", "numrange", "numrange", nil},
		{"int8range", "int8range", "int8range", nil},
		{"daterange", "daterange", "daterange", nil},
		{"tsrange", "tsrange", "tsrange", nil},
		{"tstzrange", "tstzrange", "tstzrange", nil},
		{"nummultirange", "nummultirange", "nummultirange", nil},
		{"int8multirange", "int8multirange", "int8multirange", nil},
	}
	// Serial subtests each get the fixture's normal bounded lifetime.
	for i, tc := range cases {
		t.Run(fmt.Sprintf("scalar_%02d", i), func(t *testing.T) {
			icScenario(t, func(c *icCase) {
				c.exec("CREATE TABLE ic_scope.dim(v " + tc.decl + ")")
				row := c.row("dim", "v", 1, tc.dataType, tc.udt, tc.fields)
				if tc.decl == "name" {
					row = icPatch(row, map[string]any{"collation_catalog": c.database, "collation_schema": "pg_catalog", "collation_name": "C"})
				}
				c.check("dim", row)
				if tc.decl == "numeric(5,-2)" {
					c.run(c.conn, fixture.Select("SELECT atttypmod FROM pg_attribute WHERE attrelid='ic_scope.dim'::regclass AND attname='v'", []string{"atttypmod"}, []string{"INT4"}, [][]any{{int64(329730)}}))
				}
			})
		})
	}
	for _, qualifier := range []string{"YEAR", "MONTH", "DAY", "HOUR", "MINUTE", "SECOND", "YEAR TO MONTH", "DAY TO HOUR", "DAY TO MINUTE", "DAY TO SECOND", "HOUR TO MINUTE", "HOUR TO SECOND", "MINUTE TO SECOND"} {
		t.Run(strings.ReplaceAll(qualifier, " ", "_"), func(t *testing.T) {
			icScenario(t, func(c *icCase) {
				c.exec("CREATE TABLE ic_scope.dim(v interval " + qualifier + ")")
				c.check("dim", c.row("dim", "v", 1, "interval", "interval", map[string]any{"datetime_precision": int64(6), "interval_type": qualifier}))
			})
		})
	}
	t.Run("domains_udts_collation", func(t *testing.T) {
		icScenario(t, func(c *icCase) {
			c.exec("CREATE DOMAIN ic_scope.nn AS integer DEFAULT 11 NOT NULL", "CREATE DOMAIN ic_scope.nested AS ic_scope.nn", "CREATE DOMAIN ic_scope.words AS varchar(4)", "CREATE DOMAIN ic_scope.arr AS integer[]", "CREATE TYPE ic_scope.mood AS ENUM('happy','sad')", "CREATE TYPE ic_scope.pair AS (x integer,y text)", "CREATE COLLATION ic_scope.custom (provider=libc,locale='C')",
				"CREATE TABLE ic_scope.dim(a ic_scope.nn,b ic_scope.nested,c ic_scope.words,d ic_scope.arr,e integer[],f ic_scope.mood,g ic_scope.pair,h int4range,i text COLLATE \"C\",j text COLLATE ic_scope.custom,k ic_scope.words DEFAULT 'own')")
			want := [][]any{}
			for i, domain := range []string{"nn", "nested", "words", "arr"} {
				dataType, udt, fields := "integer", "int4", icIntFields()
				if i == 0 {
					fields["is_nullable"] = "NO"
				}
				// The released view unwraps one declared domain level, not the
				// recursive value-coercion base; nested itself is nullable.
				if i == 1 {
					dataType, udt, fields = "USER-DEFINED", "nn", map[string]any{"udt_schema": "ic_scope"}
				}
				if i == 2 {
					dataType, udt, fields = "character varying", "varchar", map[string]any{"character_maximum_length": int64(4), "character_octet_length": int64(16)}
				}
				if i == 3 {
					dataType, udt, fields = "ARRAY", "_int4", map[string]any{}
				}
				fields["domain_catalog"], fields["domain_schema"], fields["domain_name"] = c.database, "ic_scope", domain
				want = append(want, c.row("dim", string(rune('a'+i)), i+1, dataType, udt, fields))
			}
			want = append(want, c.row("dim", "e", 5, "ARRAY", "_int4", nil))
			for i, name := range []string{"mood", "pair"} {
				want = append(want, icRow(c.database, "ic_scope", "dim", string(rune('f'+i)), i+6, "USER-DEFINED", "ic_scope", name, nil))
			}
			want = append(want, c.row("dim", "h", 8, "int4range", "int4range", nil))
			for i, collation := range []string{"C", "custom"} {
				schema := "pg_catalog"
				if i == 1 {
					schema = "ic_scope"
				}
				want = append(want, c.row("dim", string(rune('i'+i)), i+9, "text", "text", map[string]any{"character_octet_length": int64(1073741824), "collation_catalog": c.database, "collation_schema": schema, "collation_name": collation}))
			}
			want = append(want, c.row("dim", "k", 11, "character varying", "varchar", map[string]any{"character_maximum_length": int64(4), "character_octet_length": int64(16), "domain_catalog": c.database, "domain_schema": "ic_scope", "domain_name": "words", "column_default": "'own'::character varying"}))
			c.check("dim", want...)
			c.exec("CREATE VIEW ic_scope.dv AS SELECT a,b,c FROM ic_scope.dim")
			for i := range want[:3] {
				want[i] = icPatch(want[i], map[string]any{"table_name": "dv"})
			}
			c.check("dv", want[:3]...)
		})
	})
	icExtensionDimensions(t)
}
