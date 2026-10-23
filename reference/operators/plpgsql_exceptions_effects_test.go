package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLExceptionEffects(t *testing.T) {
	for _, tc := range []struct{ name, definition, seed, failed, replacement, condition string }{
		{"foreign_key", "CREATE TABLE ex_effect(v integer REFERENCES ex_parent(v))", "INSERT INTO ex_effect VALUES(1)", "2", "3", "foreign_key_violation"},
		{"unique", "CREATE TABLE ex_effect(v integer PRIMARY KEY)", "INSERT INTO ex_effect VALUES(1)", "1", "3", "unique_violation"},
		{"exclusion", "CREATE TABLE ex_effect(v integer, CONSTRAINT ex_effect_ex EXCLUDE USING btree(v WITH =))", "INSERT INTO ex_effect VALUES(1)", "1", "3", "exclusion_violation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE ex_parent(v integer PRIMARY KEY)", tc.definition,
					"CREATE FUNCTION ex_effect_put(x integer) RETURNS integer LANGUAGE SQL AS $$INSERT INTO ex_effect VALUES(x); SELECT x$$",
					"CREATE FUNCTION ex_effect_fail() RETURNS integer LANGUAGE SQL AS $$INSERT INTO ex_effect VALUES(4); INSERT INTO ex_effect VALUES("+tc.failed+"); SELECT 99$$")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ex_parent VALUES(1),(3),(4)", 3))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(tc.seed, 1))
				plpgsqlExceptionDefine(t, p, b, c, "ex_effect_run", "integer", "DECLARE x integer; BEGIN x := ex_effect_fail(); RETURN 99; EXCEPTION WHEN "+tc.condition+" THEN x := ex_effect_put("+tc.replacement+"); RETURN 7; END")
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT ex_effect_run() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM ex_effect ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ex_effect WHERE v=3", 1))
				}
			})
		})
	}
	t.Run("settings_and_sequence", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE SEQUENCE ex_seq START 1",
				"CREATE FUNCTION ex_set_outer() RETURNS integer LANGUAGE SQL AS $$SET application_name TO 'outer'; SELECT 1$$",
				"CREATE FUNCTION ex_set_inner() RETURNS integer LANGUAGE SQL AS $$SET application_name TO 'inner'; SELECT 1$$")
			plpgsqlExceptionDefine(t, p, b, c, "ex_state", "text", `DECLARE s text; n bigint; x integer; BEGIN x := ex_set_outer(); BEGIN x := ex_set_inner(); n := nextval('ex_seq'); x := 1/0; EXCEPTION WHEN division_by_zero THEN s := (SELECT setting FROM pg_catalog.pg_settings WHERE name='application_name'); END; RETURN s; END`)
			// Two invocations each consume one number even though their body fails.
			plpgsqlValue(t, p, b, c, "SELECT ex_state() AS v", "TEXT", "outer")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('ex_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
			shown := fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"outer"}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, shown)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(shown))
		})
	})
	t.Run("lock_boundaries", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE ex_prior(v integer)", "CREATE TABLE ex_failed_lock(v integer)",
				"CREATE FUNCTION ex_touch() RETURNS integer LANGUAGE SQL AS $$UPDATE ex_failed_lock SET v=v; SELECT 1$$")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ex_prior VALUES(1)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ex_failed_lock VALUES(2)", 1))
			plpgsqlExceptionDefine(t, p, b, c, "ex_lock_run", "integer", "DECLARE x integer; BEGIN x := ex_touch(); x := 1/0; EXCEPTION WHEN division_by_zero THEN RETURN 7; END")
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			userOperatorExec(t, p, b, c, "BEGIN")
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE ex_prior SET v=v", 1))
			plpgsqlValue(t, p, b, c, "SELECT ex_lock_run() AS v", "INT4", int64(7))
			// NOWAIT is bounded admission, not a sleep or an elapsed-time guess.
			fixture.QueryLockRows(t, p, b, peer, "SELECT v FROM ex_failed_lock FOR UPDATE NOWAIT", false, "v", 2)
			fixture.QueryLockNowait(t, p, b, peer, "SELECT v FROM ex_prior FOR UPDATE NOWAIT", false, "ex_prior")
			userOperatorExec(t, p, b, peer, "BEGIN")
			defer peer.ExecContext(p.Ctx, "ROLLBACK")
			userOperatorExec(t, p, b, peer, "LOCK TABLE ex_failed_lock IN ACCESS EXCLUSIVE MODE NOWAIT", "ROLLBACK")
			userOperatorExec(t, p, b, c, "ROLLBACK")
			fixture.QueryLockRows(t, p, b, peer, "SELECT v FROM ex_prior FOR UPDATE NOWAIT", true, "v", 1)
		})
	})
}
