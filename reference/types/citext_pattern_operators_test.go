package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCitextPatternOperators(t *testing.T) {
	for _, op := range citextPatternOps {
		for _, rhs := range []string{"citext", "text", "unknown"} {
			t.Run(op.sql+"/"+rhs, func(t *testing.T) {
				citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					citextInstall(t, p, b, c)
					if rhs == "citext" {
						insensitive := op.sql == "~*" || op.sql == "!~*" || op.sql == "ILIKE" || op.sql == "NOT ILIKE"
						for _, right := range []string{"citext", "text"} {
							citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT 'ABC'::text "+op.sql+" 'abc'::"+right+" AS v", insensitive != op.negative))
						}
					}
					for _, match := range []bool{true, false} {
						pattern := "abc"
						if !match {
							pattern = "abd"
						}
						if op.regex {
							pattern = "^" + pattern + "$"
						}
						operand := "'" + pattern + "'"
						if rhs != "unknown" {
							operand += "::" + rhs
						}
						citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT 'ABC'::citext "+op.sql+" "+operand+" AS v", match != op.negative))
						if rhs != "unknown" {
							citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT $1::citext "+op.sql+" $2::"+rhs+" AS v", match != op.negative, "ABC", pattern))
						}
					}
					if rhs == "unknown" {
						return
					}
					for _, args := range [][]any{{nil, "abc"}, {"ABC", nil}, {nil, nil}} {
						citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT $1::citext "+op.sql+" $2::"+rhs+" AS v", nil, args...))
					}
					if op.regex {
						citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT $1::citext "+op.sql+" $2::"+rhs+" AS v", nil, nil, "["))
					}
					for _, tc := range []struct {
						s, p  string
						match bool
					}{
						{"ABC", "b", op.regex}, {"ABC", "%b%", !op.regex}, {"", "", true}, {"ABC", "", op.regex},
						{"", "%", !op.regex}, {"é", "_", !op.regex}, {"🙂", "_", !op.regex}, {"", "_", false}, {"ab", "_", false},
						{"é", "é", false},
					} {
						// Percent and underscore are literal regex characters here.
						citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT $1::citext "+op.sql+" $2::"+rhs+" AS v", tc.match != op.negative, tc.s, tc.p))
					}
					if op.regex {
						for _, tc := range []struct {
							p     string
							match bool
						}{{`\D`, false}, {`\d`, true}} {
							citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT '7'::citext "+op.sql+" $1::"+rhs+" AS v", tc.match != op.negative, tc.p))
						}
					}
				})
			})
		}
	}
	t.Run("physical_columns_and_empty_metadata", func(t *testing.T) {
		citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextPatternStoredSetup(t, p, b, c)
			citextPatternStoredValues(t, p, b, c)
			projection, names, types := citextPatternProjection("t")
			citextPatternRoutes(t, p, b, c, fixture.Select("SELECT "+projection+" FROM citext_pattern_source WHERE false", names, types, [][]any{}))
			var physical [][]any
			for _, row := range citextPatternRows() {
				physical = append(physical, row[1:17])
			}
			citextPatternRoutes(t, p, b, c, fixture.Select("SELECT "+projection+" FROM citext_pattern_source ORDER BY id", names, types, physical))
			citextPatternRoutes(t, p, b, c, fixture.Select("SELECT s::text AS s,p::text AS p,t FROM citext_pattern_source ORDER BY id", []string{"s", "p", "t"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"ABC", "abc", "abc"}, {"ABC", "abd", "abd"}, {nil, "abc", "abc"}}))
		})
	})
	t.Run("visibility_and_text_fallback", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "citext_pattern_hidden")
		citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, s := range []fixture.Step{fixture.Exec("CREATE SCHEMA citext_pattern_hidden", 0), fixture.Exec("CREATE EXTENSION citext SCHEMA citext_pattern_hidden", 0), fixture.Exec("SET search_path=pg_catalog,public", 0)} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			for _, op := range citextPatternOps {
				insensitive := op.sql == "~*" || op.sql == "!~*" || op.sql == "ILIKE" || op.sql == "NOT ILIKE"
				for _, operands := range []struct{ left, right string }{{"'ABC'::citext_pattern_hidden.citext", "'abc'::citext_pattern_hidden.citext"}, {"'ABC'::text", "'abc'::citext_pattern_hidden.citext"}, {"'ABC'::text", "'abc'::text"}} {
					citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT "+operands.left+" "+op.sql+" "+operands.right+" AS v", insensitive != op.negative))
				}
				citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT 'ABC'::citext_pattern_hidden.citext OPERATOR(citext_pattern_hidden."+op.catalog+") 'abc'::citext_pattern_hidden.citext AS v", !op.negative))
			}
		})
	})
}
