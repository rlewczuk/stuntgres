package reference_test

import (
	"testing"
)

func TestReferenceInfoColumnsViewUpdatability(t *testing.T) {
	t.Run("views", func(t *testing.T) {
		icScenario(t, func(c *icCase) {
			c.exec("CREATE TABLE ic_scope.base(gap integer,a integer NOT NULL,b integer)", "ALTER TABLE ic_scope.base DROP COLUMN gap", "CREATE VIEW ic_scope.simple AS SELECT a,b,a+b AS computed FROM ic_scope.base", "CREATE VIEW ic_scope.nested AS SELECT b,a,computed FROM ic_scope.simple")
			c.check("simple", c.integer("simple", "a", 1), c.integer("simple", "b", 2), icPatch(c.integer("simple", "computed", 3), map[string]any{"is_updatable": "NO"}))
			c.check("nested", c.integer("nested", "b", 1), c.integer("nested", "a", 2), icPatch(c.integer("nested", "computed", 3), map[string]any{"is_updatable": "NO"}))
			c.exec("ALTER TABLE ic_scope.base RENAME COLUMN a TO renamed")
			c.check("nested", c.integer("nested", "b", 1), c.integer("nested", "a", 2), icPatch(c.integer("nested", "computed", 3), map[string]any{"is_updatable": "NO"}))
			// Unconditional UPDATE rules union with automatic DELETE capability.
			c.exec("CREATE RULE computed_update AS ON UPDATE TO ic_scope.simple DO INSTEAD NOTHING")
			c.check("simple", c.integer("simple", "a", 1), c.integer("simple", "b", 2), c.integer("simple", "computed", 3))
			c.exec("DROP RULE computed_update ON ic_scope.simple")
			for _, tc := range []struct {
				name, query, column, typ, udt string
				fields                        map[string]any
			}{
				{"distinct_v", "SELECT DISTINCT b FROM ic_scope.base", "b", "integer", "int4", icIntFields()},
				{"aggregate_v", "SELECT count(*) AS n FROM ic_scope.base", "n", "bigint", "int8", map[string]any{"numeric_precision": int64(64), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)}},
				{"set_v", "SELECT b FROM ic_scope.base UNION SELECT b FROM ic_scope.base", "b", "integer", "int4", icIntFields()},
				{"join_v", "SELECT x.b FROM ic_scope.base x JOIN ic_scope.base y ON x.b=y.b", "b", "integer", "int4", icIntFields()},
				{"limit_v", "SELECT b FROM ic_scope.base LIMIT 1", "b", "integer", "int4", icIntFields()},
				{"window_v", "SELECT row_number() OVER() AS n FROM ic_scope.base", "n", "bigint", "int8", map[string]any{"numeric_precision": int64(64), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)}},
			} {
				c.exec("CREATE VIEW ic_scope." + tc.name + " AS " + tc.query)
				tc.fields["is_updatable"] = "NO"
				c.check(tc.name, c.row(tc.name, tc.column, 1, tc.typ, tc.udt, tc.fields))
			}
			for _, control := range []string{"update_only", "delete_only", "both", "conditional", "also"} {
				// DISTINCT prevents automatic DELETE from completing an UPDATE-only rule.
				c.exec("CREATE VIEW ic_scope." + control + " AS SELECT DISTINCT b+1 AS value FROM ic_scope.base")
				for _, event := range []string{"UPDATE", "DELETE"} {
					if control == "update_only" && event == "DELETE" || control == "delete_only" && event == "UPDATE" {
						continue
					}
					qual, action := "", "INSTEAD"
					if control == "conditional" {
						qual = " WHERE OLD.value>0"
					}
					if control == "also" {
						action = "ALSO"
					}
					c.exec("CREATE RULE capability_" + event + " AS ON " + event + " TO ic_scope." + control + qual + " DO " + action + " NOTHING")
				}
				flag := "NO"
				if control == "both" {
					flag = "YES"
				}
				c.check(control, icPatch(c.integer(control, "value", 1), map[string]any{"is_updatable": flag}))
			}
		})
	})
	t.Run("foreign", func(t *testing.T) {
		icConfiguredScenario(t, icConfigureForeign, func(c *icCase) {
			if c.b.PG {
				// This is the fixture's real installed C wrapper, not a fake handler.
				c.exec("CREATE EXTENSION postgres_fdw", "CREATE SERVER ic_live FOREIGN DATA WRAPPER postgres_fdw OPTIONS(host '127.0.0.1',port '5432',dbname 'stuntgres_reference')", "CREATE USER MAPPING FOR CURRENT_USER SERVER ic_live OPTIONS(user 'stuntgres',password 'stuntgres')")
				for _, tc := range []struct{ name, option, flag string }{{"both", "true", "YES"}, {"neither", "false", "NO"}} {
					c.exec("CREATE FOREIGN TABLE ic_scope." + tc.name + "(a integer) SERVER ic_live OPTIONS(schema_name 'public',table_name 'absent_remote',updatable '" + tc.option + "')")
					c.check(tc.name, icPatch(c.integer(tc.name, "a", 1), map[string]any{"is_updatable": tc.flag}))
				}
				return // Individual Go method sets have no PostgreSQL SQL equivalent.
			}
			for _, name := range []string{"neither", "update_only", "delete_only", "both"} {
				c.exec("CREATE FOREIGN DATA WRAPPER ic_"+name+" HANDLER public.ic_"+name, "CREATE SERVER ic_"+name+" FOREIGN DATA WRAPPER ic_"+name)
				defer c.conn.ExecContext(c.p.Ctx, "DROP FOREIGN DATA WRAPPER ic_"+name+" CASCADE")
				c.exec("CREATE FOREIGN TABLE ic_scope." + name + "(a integer) SERVER ic_" + name)
				flag := "NO"
				if name == "both" {
					flag = "YES"
				}
				c.check(name, icPatch(c.integer(name, "a", 1), map[string]any{"is_updatable": flag}))
			}
		})
	})
}
