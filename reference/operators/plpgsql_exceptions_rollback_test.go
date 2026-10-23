package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLExceptionRollback(t *testing.T) {
	t.Run("writes_locals_visibility", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			plpgsqlExceptionSetup(t, p, b, c)
			plpgsqlExceptionDefine(t, p, b, c, "ex_run", "integer", plpgsqlExceptionRollbackBody)
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT ex_run() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}})
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				plpgsqlExceptionRows(t, p, b, c, 1, 3, 4)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ex_log", 3))
			}
		})
	})
	for _, tc := range []struct {
		name, body string
		want       int64
		rows       []int64
	}{
		{"return", "DECLARE x integer; BEGIN x := ex_put(2); RETURN 7; EXCEPTION WHEN OTHERS THEN x := ex_put(99); RETURN 99; END", 7, []int64{2}},
		{"normal_success", "DECLARE x integer; BEGIN BEGIN x := ex_put(2); EXCEPTION WHEN OTHERS THEN x := ex_put(99); END; RETURN 7; END", 7, []int64{2}},
		{"handler_return", "DECLARE x integer; BEGIN x := ex_put(2); x := 1/0; EXCEPTION WHEN division_by_zero THEN x := ex_put(3); RETURN 7; END", 7, []int64{3}},
		{"initializer_error", "BEGIN DECLARE x integer := 1/0; BEGIN RETURN 99; EXCEPTION WHEN division_by_zero THEN RETURN 98; END; EXCEPTION WHEN division_by_zero THEN RETURN 7; END", 7, nil},
		{"initializer_effect", "BEGIN DECLARE x integer := ex_put(5); BEGIN x := ex_put(6); x := 1/0; EXCEPTION WHEN division_by_zero THEN RETURN 7; END; END", 7, []int64{5}},
		{"outer_undoes_inner_success", "DECLARE x integer; BEGIN x := ex_put(1); BEGIN BEGIN x := ex_put(2); EXCEPTION WHEN OTHERS THEN x := ex_put(99); END; x := 1/0; EXCEPTION WHEN division_by_zero THEN x := ex_put(3); END; RETURN 7; END", 7, []int64{1, 3}},
		{"update_delete", "DECLARE x integer; BEGIN x := ex_put(1); x := ex_put(2); BEGIN x := ex_update(); x := ex_delete(); x := ex_put(3); x := 1/0; EXCEPTION WHEN division_by_zero THEN x := (SELECT sum(v) FROM ex_log); END; RETURN x; END", 3, []int64{1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plpgsqlExceptionSetup(t, p, b, c)
				userOperatorExec(t, p, b, c,
					"CREATE FUNCTION ex_update() RETURNS integer LANGUAGE SQL AS $$UPDATE ex_log SET v=10 WHERE v=1; SELECT 1$$",
					"CREATE FUNCTION ex_delete() RETURNS integer LANGUAGE SQL AS $$DELETE FROM ex_log WHERE v=2; SELECT 1$$")
				plpgsqlExceptionDefine(t, p, b, c, "ex_run", "integer", tc.body)
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT ex_run() AS v", []string{"v"}, []string{"INT4"}, [][]any{{tc.want}})
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					plpgsqlExceptionRows(t, p, b, c, tc.rows...)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ex_log", int64(len(tc.rows))))
				}
			})
		})
	}
}

func TestReferencePLpgSQLExceptionInline(t *testing.T) {
	for _, ending := range []string{"", " RETURN;"} {
		t.Run("handled"+ending, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				plpgsqlExceptionSetup(t, p, b, c)
				q := "DO $$DECLARE x integer; BEGIN x := ex_put(1); BEGIN x := ex_put(2); x := 1/0; EXCEPTION WHEN division_by_zero THEN x := ex_put(3); END; x := ex_put(4);" + ending + " END$$"
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec(q, 0)
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					plpgsqlExceptionRows(t, p, b, c, 1, 3, 4)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ex_log", 3))
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DO $$DECLARE x integer; BEGIN x := ex_put(2); RETURN; EXCEPTION WHEN OTHERS THEN x := ex_put(99); END$$", 0))
				plpgsqlExceptionRows(t, p, b, c, 2)
			})
		})
	}
	t.Run("unmatched", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			plpgsqlExceptionSetup(t, p, b, c)
			plpgsqlError(t, p, c, "DO $$DECLARE x integer; BEGIN x := ex_put(2); x := 1/0; EXCEPTION WHEN unique_violation THEN x := ex_put(99); END$$", false, plpgsqlExceptionDivision("inline_code_block"))
			plpgsqlExceptionRows(t, p, b, c)
		})
	})
}
