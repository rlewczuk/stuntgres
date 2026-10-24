package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSelectIntoIsolation(t *testing.T) {
	for _, rewrite := range []string{"also", "instead"} {
		for _, demand := range []string{"one", "two_projections", "no_third_proposal", "second_projection_error"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("rule_%s/%s/prepared_%t", rewrite, demand, prepared), func(t *testing.T) {
					plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						constraint := ""
						if demand == "no_third_proposal" {
							constraint = " CHECK(id<3)"
						}
						userOperatorExec(t, p, b, c,
							"CREATE SEQUENCE si_rule_draw",
							"CREATE TABLE si_rule_source(id integer"+constraint+")",
							"CREATE TABLE si_rule_sink(id integer"+constraint+")",
							"CREATE TABLE si_rule_log(id integer)",
							"CREATE TABLE si_rule_prior(id integer)")
						// Every action produces three rows even for a single result row.
						// This action must never receive the result command's capture policy.
						action := "INSERT INTO si_rule_log SELECT NEW.id*10+g FROM generate_series(1,3) AS s(g)"
						rule := "CREATE RULE si_rule AS ON INSERT TO si_rule_source DO ALSO " + action
						if rewrite == "instead" {
							rule = "CREATE RULE si_rule AS ON INSERT TO si_rule_source DO INSTEAD (" + action + "; INSERT INTO si_rule_sink VALUES(NEW.id) RETURNING id)"
						}
						userOperatorExec(t, p, b, c, rule)
						values, projection := "(1),(2),(3)", "nextval('si_rule_draw')"
						want, draws := "many:99:true", int64(2)
						if demand == "one" {
							values, want, draws = "(1)", "ok:1:true", 1
						}
						if demand == "second_projection_error" {
							projection = "nextval('si_rule_draw')+10/(2-id)"
							want = "division:99:false"
						}
						query := "INSERT INTO si_rule_source VALUES" + values + " RETURNING " + projection + " INTO x"
						// The prior INSERT sets FOUND true, so explicitly reset it without
						// changing data before entering the protected capture block.
						userOperatorExec(t, p, b, c,
							"CREATE FUNCTION si_rule_capture() RETURNS text LANGUAGE plpgsql AS $$DECLARE x bigint:=99; prior_id integer; status text:='ok'; BEGIN INSERT INTO si_rule_prior VALUES(9) RETURNING id INTO prior_id; FOUND:=false; BEGIN "+query+"; EXCEPTION WHEN too_many_rows THEN status:='many'; WHEN division_by_zero THEN status:='division'; END; RETURN status||':'||x||':'||FOUND; END$$")
						step := fixture.Select("SELECT si_rule_capture() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{want}})
						if prepared {
							step = fixture.Prepared(step)
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
						plpgsqlValue(t, p, b, c, "SELECT last_value AS v FROM si_rule_draw", "INT8", draws)
						plpgsqlValue(t, p, b, c, "SELECT sum(id) AS v FROM si_rule_prior", "INT8", int64(9))
						sourceRows, sinkRows, logRows := int64(0), int64(0), int64(0)
						if demand == "one" {
							logRows = 3
							if rewrite == "also" {
								sourceRows = 1
							} else {
								sinkRows = 1
							}
						}
						plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM si_rule_source", "INT8", sourceRows)
						plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM si_rule_sink", "INT8", sinkRows)
						plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM si_rule_log", "INT8", logRows)
					})
				})
			}
		}
	}
	for _, many := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("writable_cte/many_%t/prepared_%t", many, prepared), func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					primary, want := "SELECT id FROM changed WHERE id=10", "ok:10:true"
					if many {
						primary, want = "SELECT id FROM changed", "many:99:true"
					}
					userOperatorExec(t, p, b, c,
						"CREATE TABLE si_cte_aux(id integer)", "CREATE TABLE si_cte_sink(id integer)", "CREATE TABLE si_cte_prior(id integer)",
						"CREATE FUNCTION si_cte_capture() RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=99; prior_id integer; status text:='ok'; BEGIN INSERT INTO si_cte_prior VALUES(9) RETURNING id INTO prior_id; FOUND:=false; BEGIN WITH changed AS (INSERT INTO si_cte_aux VALUES(10),(20),(30) RETURNING id) INSERT INTO si_cte_sink "+primary+" RETURNING id INTO x; EXCEPTION WHEN too_many_rows THEN status:='many'; END; RETURN status||':'||x||':'||FOUND; END$$")
					step := fixture.Select("SELECT si_cte_capture() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{want}})
					if prepared {
						step = fixture.Prepared(step)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
					var auxRows, sinkRows [][]any
					if !many {
						auxRows = [][]any{{int64(10)}, {int64(20)}, {int64(30)}}
						sinkRows = [][]any{{int64(10)}}
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM si_cte_aux ORDER BY id", []string{"id"}, []string{"INT4"}, auxRows))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM si_cte_sink ORDER BY id", []string{"id"}, []string{"INT4"}, sinkRows))
					plpgsqlValue(t, p, b, c, "SELECT sum(id) AS v FROM si_cte_prior", "INT8", int64(9))
				})
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("trigger_nested_write/prepared_%t", prepared), func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				// Query FOR consumes all three nested RETURNING rows without a PL
				// single-row destination; only the outer INSERT owns capture.
				userOperatorExec(t, p, b, c,
					"CREATE TABLE si_trigger_log(id integer)", "CREATE TABLE si_trigger_sink(id integer)",
					"CREATE FUNCTION si_trigger_write() RETURNS trigger LANGUAGE plpgsql AS $$DECLARE r record; n integer:=0; BEGIN FOR r IN INSERT INTO si_trigger_log VALUES(1),(2),(3) RETURNING id LOOP n:=n+r.id; END LOOP; NEW.id:=NEW.id+n; RETURN NEW; END$$",
					"CREATE TRIGGER si_trigger BEFORE INSERT ON si_trigger_sink FOR EACH ROW EXECUTE FUNCTION si_trigger_write()",
					"CREATE FUNCTION si_trigger_capture() RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=99; BEGIN INSERT INTO si_trigger_sink VALUES(1) RETURNING id INTO x; RETURN x||':'||FOUND; END$$")
				step := fixture.Select("SELECT si_trigger_capture() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"7:true"}})
				if prepared {
					step = fixture.Prepared(step)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM si_trigger_log ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM si_trigger_sink", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
		})
	}
}
