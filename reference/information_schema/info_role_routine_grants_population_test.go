package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoRoleRoutineGrantsPopulation(t *testing.T) {
	t.Run("user_kinds_and_names", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			longASCII := strings.Repeat("a", 63)
			longUTF8 := strings.Repeat("雪", 21)
			c.exec("CREATE FUNCTION rrg_over(integer) RETURNS integer RETURN $1", "CREATE FUNCTION rrg_over(text) RETURNS text RETURN $1", "CREATE FUNCTION rrg_void() RETURNS void BEGIN ATOMIC END", "CREATE FUNCTION rrg_string() RETURNS integer LANGUAGE SQL AS 'SELECT 7'", "CREATE FUNCTION rrg_fail(integer) RETURNS integer RETURN 7/$1", `CREATE FUNCTION "Routine Quote"() RETURNS integer RETURN 7`, `CREATE FUNCTION "`+longASCII+`"() RETURNS integer RETURN 7`, `CREATE FUNCTION "`+longUTF8+`"() RETURNS integer RETURN 7`)
			signatures := []string{"rrg_over(integer)", "rrg_over(text)", "rrg_void()", "rrg_string()", "rrg_fail(integer)", `"Routine Quote"()`, `"` + longASCII + `"()`, `"` + longUTF8 + `"()`}
			var overloads [][]any
			var seen []int64
			for _, signature := range signatures {
				schema, name, _, _, oid := c.identity(signature)
				want := c.baseline(signature)
				if name == "rrg_over" {
					overloads = append(overloads, want...)
					seen = append(seen, oid)
					continue
				}
				c.check(c.conn, schema, name, want...)
			}
			if seen[0] == seen[1] {
				c.t.Fatal("overload identities collapsed")
			}
			c.check(c.conn, "public", "rrg_over", overloads...)
			// Unrelated supported object ACL classes never become routine rows.
			c.exec("CREATE ROLE rrg_r", "CREATE TABLE rrg_nonroutine(a integer)", "CREATE SEQUENCE rrg_nonroutine_seq", "CREATE TYPE rrg_nonroutine_type AS ENUM ('a')", "GRANT SELECT ON rrg_nonroutine TO rrg_r", "GRANT USAGE ON SEQUENCE rrg_nonroutine_seq TO rrg_r", "GRANT USAGE ON TYPE rrg_nonroutine_type TO rrg_r")
			for _, name := range []string{"rrg_nonroutine", "rrg_nonroutine_seq", "rrg_nonroutine_type"} {
				c.check(c.conn, "public", name)
			}
		})
	})
	t.Run("sql_procedures", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.seed()
			c.exec("CREATE PROCEDURE public.rrg_proc() BEGIN ATOMIC SELECT 7; END", "CREATE PROCEDURE public.rrg_text_proc() LANGUAGE SQL AS 'SELECT 8'", "ALTER PROCEDURE public.rrg_proc() OWNER TO rrg_o", "ALTER PROCEDURE public.rrg_text_proc() OWNER TO rrg_o")
			held := c.hold(c.conn, rrgQuery)
			for _, name := range []string{"rrg_proc", "rrg_text_proc"} {
				signature := "public." + name + "()"
				c.check(c.conn, "public", name, c.baseline(signature)...)
				c.actor("rrg_r", func() { c.check(held, "public", name) })
				c.exec("SET ROLE rrg_o", "GRANT EXECUTE ON PROCEDURE "+signature+" TO rrg_r", "RESET ROLE")
				schema, routine, _, specific, _ := c.identity(signature)
				row := c.row("rrg_o", "rrg_r", schema, routine, specific, "NO")
				c.actor("rrg_r", func() { c.check(c.conn, schema, routine, row); c.check(held, schema, routine, row) })
				c.exec("SET ROLE rrg_o", "GRANT EXECUTE ON PROCEDURE "+signature+" TO rrg_r WITH GRANT OPTION", "RESET ROLE")
				c.actor("rrg_r", func() { c.check(c.conn, schema, routine, c.row("rrg_o", "rrg_r", schema, routine, specific, "YES")) })
				c.actor("rrg_r", func() { c.check(held, schema, routine, c.row("rrg_o", "rrg_r", schema, routine, specific, "YES")) })
				c.exec("SET ROLE rrg_o", "REVOKE GRANT OPTION FOR EXECUTE ON PROCEDURE "+signature+" FROM rrg_r", "RESET ROLE")
				c.actor("rrg_r", func() { c.check(c.conn, schema, routine, row); c.check(held, schema, routine, row) })
				c.check(held, schema, routine, append(c.baseline(signature), row)...)
				c.exec("SET ROLE rrg_o", "REVOKE EXECUTE ON PROCEDURE "+signature+" FROM rrg_r", "RESET ROLE")
				c.check(c.conn, schema, routine, c.baseline(signature)...)
				c.check(held, schema, routine, c.baseline(signature)...)
				c.actor("rrg_r", func() { c.check(c.conn, schema, routine); c.check(held, schema, routine) })
			}
		})
	})
	t.Run("installed_real_descriptors", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.exec("CREATE SCHEMA rrg_ext", "CREATE EXTENSION pgcrypto SCHEMA rrg_ext")
			for _, signature := range []string{"pg_catalog.abs(integer)", "pg_catalog.sum(integer)", "pg_catalog.row_number()", "pg_catalog.generate_series(integer,integer)", "rrg_ext.digest(text,text)"} {
				schema, name, _, specific, _ := c.identity(signature)
				// Constrain to exact independently resolved signature: builtin names have
				// additional overloads which are not a cross-backend inventory contract.
				q := rrgQuery + " AND specific_name=$3"
				s := c.step(q, c.baseline(signature), schema, name, specific)
				c.run(c.conn, s)
				c.run(c.conn, fixture.Prepared(s))
				c.run(c.hold(c.conn, q), s)
			}
			c.exec("DROP EXTENSION pgcrypto")
			c.check(c.conn, "rrg_ext", "digest")
		})
	})
}
