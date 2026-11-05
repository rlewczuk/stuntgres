package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheEvictionCallCarriers(t *testing.T) {
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c,
			"CREATE FUNCTION bc_part.carrier_scalar(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
			"CREATE FUNCTION bc_part.carrier_zero() RETURNS integer LANGUAGE SQL AS 'SELECT 77'",
			"CREATE SCHEMA bc_moved",
			"CREATE FUNCTION bc_moved.pg_buffercache_evict_all() RETURNS integer LANGUAGE SQL AS 'SELECT 77'")
		for _, spec := range []struct{ name, arg string; want int64 }{
			{"bc_part.carrier_scalar", "1", 1},
			{"bc_part.carrier_zero", "", 77},
			{"bc_moved.pg_buffercache_evict_all", "", 77},
		} {
			t.Run(spec.name, func(t *testing.T) {
				q := "SELECT v FROM "+spec.name+"("+spec.arg+") AS s(v)"
				s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{spec.want}})
				bcBoth(t, p, b, c, s)
				bcRun(t, p, b, bceHeld(t, p, c, q), s)
				star := "SELECT * FROM "+spec.name+"(*)"
				if spec.arg != "" {
					s := bcError(star, "42883", "function "+spec.name+"() does not exist")
					s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					s.Diagnostic.Position = fixture.Position(15)
					bcFailure(t, p, b, c, s, "prepare")
				} else {
					s := bcError(star, "42809", spec.name+"(*) specified, but "+spec.name+" is not an aggregate function")
					s.Diagnostic.Position = fixture.Position(15)
					bcFailure(t, p, b, c, s, "prepare")
				}
				if spec.arg != "" {
					for _, decoration := range []string{"DISTINCT", "ORDER BY"} {
						argument := "DISTINCT 1"
						if decoration == "ORDER BY" { argument = "1 ORDER BY 1" }
						q := "SELECT * FROM "+spec.name+"("+argument+")"
						s := bcError(q, "42809", decoration+" specified, but "+spec.name+" is not an aggregate function")
						s.Diagnostic.Position = fixture.Position(15)
						bcFailure(t, p, b, c, s, "prepare")
					}
				}
				q = "SELECT * FROM "+spec.name+"("+spec.arg+") FILTER (WHERE true)"
				syntax := bcError(q, "42601", "syntax error at or near \"WHERE\"")
				syntax.Diagnostic.Position = fixture.Position(strings.Index(q, "WHERE")+1)
				bcFailure(t, p, b, c, syntax, "prepare")
			})
		}
	})
}
