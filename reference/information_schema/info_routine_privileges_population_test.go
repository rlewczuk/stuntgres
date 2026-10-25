package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinePrivilegesPopulation(t *testing.T) {
	t.Run("authored_names_and_kinds", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			ascii, unicode := strings.Repeat("a", 63), strings.Repeat("雪", 21)
			c.exec("CREATE FUNCTION rp_over(integer) RETURNS integer RETURN $1", "CREATE FUNCTION rp_over(text) RETURNS text RETURN $1", "CREATE FUNCTION rp_atomic() RETURNS void BEGIN ATOMIC END", "CREATE FUNCTION rp_text() RETURNS integer LANGUAGE SQL AS 'SELECT 7'", "CREATE FUNCTION rp_fail(integer) RETURNS integer RETURN 7/$1", `CREATE FUNCTION "RP Quote"() RETURNS integer RETURN 7`, `CREATE FUNCTION "`+ascii+`"() RETURNS integer RETURN 7`, `CREATE FUNCTION "`+unicode+`"() RETURNS integer RETURN 7`, "CREATE PROCEDURE rp_proc() BEGIN ATOMIC SELECT 7; END", "CREATE PROCEDURE rp_text_proc() LANGUAGE SQL AS 'SELECT 8'")
			held := c.hold(c.conn, rpQuery)
			var overloads [][]any
			var overloadIDs []int64
			for _, signature := range []string{"public.rp_over(integer)", "public.rp_over(text)", "public.rp_atomic()", "public.rp_text()", "public.rp_fail(integer)", `public."RP Quote"()`, `public."` + ascii + `"()`, `public."` + unicode + `"()`, "public.rp_proc()", "public.rp_text_proc()"} {
				schema, name, _, _, oid := c.identity(signature)
				want := c.baseline(signature)
				if name == "rp_over" {
					overloads = append(overloads, want...)
					overloadIDs = append(overloadIDs, oid)
				} else {
					c.check(c.conn, schema, name, want...)
					c.check(held, schema, name, want...)
				}
			}
			if overloadIDs[0] == overloadIDs[1] {
				c.t.Fatal("collapsed overload identities")
			}
			c.check(c.conn, "public", "rp_over", overloads...)
			c.check(held, "public", "rp_over", overloads...)
			c.exec("DROP FUNCTION public.rp_over(integer)")
			c.check(held, "public", "rp_over", c.baseline("public.rp_over(text)")...)
			c.exec("CREATE ROLE rp_r", "CREATE TABLE rp_nonroutine(a integer)", "CREATE SEQUENCE rp_nonroutine_seq", "CREATE TYPE rp_nonroutine_type AS ENUM ('a')", "GRANT SELECT ON rp_nonroutine TO rp_r", "GRANT USAGE ON SEQUENCE rp_nonroutine_seq TO rp_r", "GRANT USAGE ON TYPE rp_nonroutine_type TO rp_r")
			for _, name := range []string{"rp_nonroutine", "rp_nonroutine_seq", "rp_nonroutine_type", "rp_missing"} {
				c.check(c.conn, "public", name)
				c.check(held, "public", name)
			}
		})
	})
	t.Run("represented_provider_families", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			c.exec("CREATE SCHEMA rp_ext", "CREATE EXTENSION pgcrypto SCHEMA rp_ext")
			for _, signature := range []string{"pg_catalog.abs(integer)", "pg_catalog.sum(integer)", "pg_catalog.row_number()", "pg_catalog.generate_series(integer,integer)", "rp_ext.digest(text,text)"} {
				schema, name, _, specific, _ := c.identity(signature)
				q := rpQuery + " AND specific_name=$3"
				s := c.step(q, c.baseline(signature), schema, name, specific)
				c.run(c.conn, s)
				c.run(c.conn, fixture.Prepared(s))
				c.run(c.hold(c.conn, q), s)
			}
			c.exec("DROP EXTENSION pgcrypto")
			c.check(c.conn, "rp_ext", "digest")
		})
	})
}
