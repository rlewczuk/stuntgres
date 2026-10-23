package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLExceptionCommandEffects(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		route := "direct"
		if prepared {
			route = "prepared"
		}
		for _, failed := range []bool{false, true} {
			name := "once_" + route
			if failed {
				name = "rollback_" + route
			}
			t.Run(name, func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c,
						"CREATE SEQUENCE ex_action_seq START 10",
						"CREATE TABLE ex_parent(id integer PRIMARY KEY)",
						"CREATE TABLE ex_child(pid integer DEFAULT nextval('ex_action_seq') REFERENCES ex_parent(id) ON DELETE SET DEFAULT)",
						"CREATE FUNCTION ex_delete_parent() RETURNS integer LANGUAGE SQL AS $$DELETE FROM ex_parent WHERE id=1; SELECT 7$$",
						"CREATE FUNCTION ex_noop() RETURNS integer LANGUAGE SQL AS $$SELECT 7$$")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ex_parent VALUES(1),(10),(11),(12)", 4))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ex_child VALUES(1)", 1))
					body := "DECLARE x integer; BEGIN BEGIN x := ex_delete_parent();"
					if failed {
						body += " x := 1/0;"
					}
					body += " EXCEPTION WHEN division_by_zero THEN x := ex_noop(); END; x := ex_noop(); RETURN (SELECT pid FROM ex_child); END"
					plpgsqlExceptionDefine(t, p, b, c, "ex_command", "integer", body)
					want := int64(10)
					if failed {
						want = 1
					}
					s := fixture.Select("SELECT ex_command() AS v", []string{"v"}, []string{"INT4"}, [][]any{{want}})
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pid AS v FROM ex_child", []string{"v"}, []string{"INT4"}, [][]any{{want}}))
					if failed {
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id AS v FROM ex_parent WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
						// The failed action's sequence draw survives its rolled-back rows.
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT ex_delete_parent() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT pid AS v FROM ex_child", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
					}
					next := int64(11)
					if failed {
						next = 12
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('ex_action_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{next}}))
				})
			})
		}
		for _, sameRelation := range []bool{false, true} {
			name := "pending_outer_different_" + route
			if sameRelation {
				name = "pending_outer_same_" + route
			}
			t.Run(name, func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c,
						"CREATE TABLE ex_node(id integer PRIMARY KEY, parent integer REFERENCES ex_node(id))",
						"CREATE TABLE ex_aux(v integer)")
					write := "INSERT INTO ex_aux VALUES(3)"
					if sameRelation {
						write = "INSERT INTO ex_node VALUES(3,NULL)"
					}
					userOperatorExec(t, p, b, c,
						"CREATE FUNCTION ex_second() RETURNS integer LANGUAGE SQL AS $$"+write+"; SELECT 2$$",
						"CREATE FUNCTION ex_outer_put() RETURNS integer LANGUAGE SQL AS $$INSERT INTO ex_node VALUES(1,2),(ex_second(),NULL); SELECT 7$$")
					plpgsqlExceptionDefine(t, p, b, c, "ex_command", "integer", "DECLARE x integer; BEGIN x := ex_outer_put(); RETURN x; EXCEPTION WHEN foreign_key_violation THEN RETURN 99; END")
					s := fixture.Select("SELECT ex_command() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					rows := [][]any{{int64(1), int64(2)}, {int64(2), nil}}
					aux := [][]any{{int64(3)}}
					if sameRelation {
						rows = append(rows, []any{int64(3), nil})
						aux = [][]any{}
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,parent FROM ex_node ORDER BY id", []string{"id", "parent"}, []string{"INT4", "INT4"}, rows))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM ex_aux ORDER BY v", []string{"v"}, []string{"INT4"}, aux))
				})
			})
		}
	}
}
