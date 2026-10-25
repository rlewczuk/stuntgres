package reference_test

import "testing"

func icExtensionDimensions(t *testing.T) {
	t.Helper()
	for _, tc := range []struct{ extension, typ string }{{"citext", "citext"}, {"cube", "cube"}, {"intarray", "query_int"}, {"earthdistance", "earth"}} {
		t.Run(tc.extension, func(t *testing.T) {
			icScenario(t, func(c *icCase) {
				if tc.extension == "earthdistance" {
					c.exec("CREATE EXTENSION cube WITH SCHEMA ic_scope")
				}
				c.exec("CREATE EXTENSION " + tc.extension + " WITH SCHEMA ic_scope")
				defer c.conn.ExecContext(c.p.Ctx, "DROP EXTENSION IF EXISTS "+tc.extension+" CASCADE")
				if tc.extension == "earthdistance" {
					defer c.conn.ExecContext(c.p.Ctx, "DROP EXTENSION IF EXISTS cube CASCADE")
				}
				c.exec("CREATE TABLE ic_scope.ext(v ic_scope." + tc.typ + ")")
				fields := map[string]any{}
				udt := tc.typ
				if tc.typ == "earth" {
					udt = "cube"
					fields["domain_catalog"], fields["domain_schema"], fields["domain_name"] = c.database, "ic_scope", "earth"
				}
				c.check("ext", icRow(c.database, "ic_scope", "ext", "v", 1, "USER-DEFINED", "ic_scope", udt, fields))
			})
		})
	}
	t.Run("information_domains", func(t *testing.T) {
		icScenario(t, func(c *icCase) {
			c.exec("CREATE TABLE ic_scope.info(a information_schema.cardinal_number,b information_schema.character_data,c information_schema.sql_identifier,d information_schema.yes_or_no,e information_schema.time_stamp)")
			var want [][]any
			for i, domain := range []string{"cardinal_number", "character_data", "sql_identifier", "yes_or_no", "time_stamp"} {
				fields := map[string]any{"domain_catalog": c.database, "domain_schema": "information_schema", "domain_name": domain}
				dataType, udt := "integer", "int4"
				switch i {
				case 0:
					for k, v := range icIntFields() {
						fields[k] = v
					}
				case 1:
					dataType, udt = "character varying", "varchar"
					fields["character_octet_length"] = int64(1073741824)
				case 2:
					dataType, udt = "name", "name"
				case 3:
					dataType, udt = "character varying", "varchar"
					fields["character_maximum_length"], fields["character_octet_length"] = int64(3), int64(12)
				case 4:
					dataType, udt = "timestamp with time zone", "timestamptz"
					fields["datetime_precision"] = int64(2)
				}
				if i > 0 && i < 4 {
					fields["collation_catalog"], fields["collation_schema"], fields["collation_name"] = c.database, "pg_catalog", "C"
				}
				want = append(want, c.row("info", string(rune('a'+i)), i+1, dataType, udt, fields))
			}
			c.check("info", want...)
		})
	})
}
