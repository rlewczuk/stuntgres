package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSelectIntoCTEDemand(t *testing.T) {
	for _, demand := range []string{"first_projection", "sequence_one", "sequence_strict_two", "strict_second_projection_error"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared_%t", demand, prepared), func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					projection, into := "10/(2-g)", "INTO x"
					want := "ok:10:true"
					var draws int64
					switch demand {
					case "sequence_one":
						projection, want, draws = "nextval('si_cte_draw')", "ok:1:true", 1
					case "sequence_strict_two":
						projection, into = "nextval('si_cte_draw')", "INTO STRICT x"
						want, draws = "many:99:true", 2
					case "strict_second_projection_error":
						into, want = "INTO STRICT x", "division:99:false"
					}
					// The primary does not read changed: auxiliary completion must not
					// depend on consuming its RETURNING rows or on primary demand.
					// No eager sort forces evaluation of ignored primary projections.
					query := "WITH changed AS (INSERT INTO si_cte_aux VALUES(10),(20),(30) RETURNING id) SELECT " + projection + " " + into + " FROM generate_series(1,3) AS s(g)"
					userOperatorExec(t, p, b, c,
						"CREATE TABLE si_cte_aux(id integer)",
						"CREATE TABLE si_cte_prior(id integer)",
						"CREATE SEQUENCE si_cte_draw",
						"CREATE FUNCTION si_cte_demand() RETURNS text LANGUAGE plpgsql AS $$DECLARE x bigint:=99; prior_id integer; status text:='ok'; BEGIN INSERT INTO si_cte_prior VALUES(9) RETURNING id INTO prior_id; FOUND:=false; BEGIN "+query+"; EXCEPTION WHEN too_many_rows THEN status:='many'; WHEN division_by_zero THEN status:='division'; END; RETURN status||':'||x||':'||FOUND; END$$")
					step := fixture.Select("SELECT si_cte_demand() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{want}})
					if prepared {
						step = fixture.Prepared(step)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					// Cardinality and projection errors leave the destination intact,
					// roll back protected auxiliary work, and retain prior outer work.
					var auxRows [][]any
					if demand == "first_projection" || demand == "sequence_one" {
						auxRows = [][]any{{int64(10)}, {int64(20)}, {int64(30)}}
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM si_cte_aux ORDER BY id", []string{"id"}, []string{"INT4"}, auxRows))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM si_cte_prior ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
					if draws != 0 {
						// Sequence effects survive protected rollback: strict capture
						// demands exactly two outputs, not the complete three-row main.
						plpgsqlValue(t, p, b, c, "SELECT last_value AS v FROM si_cte_draw", "INT8", draws)
					} else {
						plpgsqlValue(t, p, b, c, "SELECT is_called AS v FROM si_cte_draw", "BOOL", false)
					}
				})
			})
		}
	}
}
