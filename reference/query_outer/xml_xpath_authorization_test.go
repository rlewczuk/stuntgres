package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceXMLXPathAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"xpath_reader"})
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE ROLE xpath_reader", 0))
		for _, signature := range []string{"xpath(text,xml)", "xpath(text,xml,text[])", "xpath_exists(text,xml)", "xpath_exists(text,xml,text[])"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog."+signature+" FROM PUBLIC", 0))
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION pg_catalog."+signature+" TO PUBLIC"); err != nil {
					t.Error(err)
				}
			}()
		}
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
				t.Error(err)
			}
		}()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xpath_reader", 0))
		for _, name := range []string{"xpath", "xpath_exists"} {
			for _, args := range []string{"'/r','<r/>'::xml", "'/r','<r/>'::xml,'{}'::text[]", "$1::text,$2::xml", "$1::text,$2::xml,'{}'::text[]"} {
				var parameters []any
				if args[0] == '$' {
					parameters = []any{"/r", "<r/>"}
				}
				xpathFailure(t, p, b, c, "SELECT pg_catalog."+name+"("+args+")", xpathError("42501", "permission denied for function "+name, "", 0), false, parameters...)
			}
		}
	})
	t.Run("formal-casts", func(t *testing.T) {
		resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION xpath_source(integer) RETURNS xml LANGUAGE sql IMMUTABLE AS 'SELECT XMLPARSE(DOCUMENT ''<r/>'')'", 0))
			for _, mode := range []string{"", " AS ASSIGNMENT", " AS IMPLICIT"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE CAST (integer AS xml) WITH FUNCTION xpath_source(integer)"+mode, 0))
				for _, name := range []string{"xpath", "xpath_exists"} {
					q := "SELECT pg_catalog." + name + "('/r',NULL::integer) AS v"
					if mode != " AS IMPLICIT" {
						d := xpathError("42883", "function pg_catalog."+name+"(unknown, integer) does not exist", "", 8)
						d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						xpathFailure(t, p, b, c, q, d, true)
					} else {
						typ, want := "_XML", any("{<r/>}")
						if name == "xpath_exists" {
							typ, want = "BOOL", true
						}
						fixture.SQLJSONBoth(t, p, b, c, xpathStep(b, q, typ, want))
					}
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP CAST (integer AS xml)", 0))
			}
		})
	})
}
