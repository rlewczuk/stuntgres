package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func returnNextRepro(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c, "BEGIN", "CREATE TABLE r3_rows(id integer)", "CREATE FUNCTION r3_return_next() RETURNS SETOF r3_rows LANGUAGE plpgsql AS $$DECLARE r r3_rows; BEGIN SELECT 1 INTO r; RETURN NEXT r; RETURN; END;$$")
	sqlExecutionBoth(t, p, b, c, "SELECT * FROM r3_return_next()", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
	userOperatorExec(t, p, b, c, "ROLLBACK")
}

func TestReferencePLpgSQLReturnNextRows(t *testing.T) {
	t.Run("original_repro", func(t *testing.T) { plpgsqlScenario(t, returnNextRepro) })
	t.Run("completion_and_snapshot", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE rn_row(id integer)", "CREATE TABLE rn_marks(id integer)",
				"CREATE FUNCTION rn_complete(mode integer) RETURNS SETOF rn_row LANGUAGE plpgsql AS $$DECLARE r rn_row; BEGIN IF mode=0 THEN RETURN; END IF; IF mode=1 THEN RETURN; END IF; SELECT 1 INTO r; RETURN NEXT r; INSERT INTO rn_marks VALUES(1); r.id:=2; RETURN NEXT r; IF mode=2 THEN RETURN; INSERT INTO rn_marks VALUES(99); END IF; END$$",
				"CREATE FUNCTION rn_empty() RETURNS SETOF rn_row LANGUAGE plpgsql AS $$BEGIN END$$")
			for _, q := range []string{"SELECT * FROM rn_complete(0)", "SELECT * FROM rn_empty()"} {
				sqlExecutionBoth(t, p, b, c, q, []string{"id"}, []string{"INT4"}, nil)
			}
			for _, mode := range []string{"2", "3"} {
				sqlExecutionBoth(t, p, b, c, "SELECT * FROM rn_complete("+mode+") ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
			}
			marks := sqlExecutionStep(b, "SELECT id FROM rn_marks ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}})
			marks.CheckMetadata = sqlExecutionMetadata(b, []string{"id"}, []string{"INT4"}, true)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, marks)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(marks))
		})
	})
	t.Run("positional_and_typed_null", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TYPE rn_pair AS(id integer,label text)", "CREATE TYPE rn_other AS(a integer,b text)",
				"CREATE FUNCTION rn_pairs(mode integer) RETURNS SETOF rn_pair LANGUAGE plpgsql AS $$DECLARE r rn_pair; s rn_other; x record; BEGIN IF mode=0 THEN RETURN NEXT r; ELSIF mode=1 THEN SELECT 7,NULL::text INTO r; RETURN NEXT r; SELECT 8,'eight'::text INTO r; RETURN NEXT r; ELSIF mode=2 THEN SELECT 7,NULL::text INTO s; RETURN NEXT s; ELSE SELECT 7 AS different,NULL::text AS names INTO x; RETURN NEXT x; END IF; END$$",
				"CREATE TABLE rn_dropped(id integer,obsolete text,label text)", "ALTER TABLE rn_dropped DROP COLUMN obsolete",
				"CREATE FUNCTION rn_drop() RETURNS SETOF rn_pair LANGUAGE plpgsql AS $$DECLARE r rn_dropped; BEGIN SELECT 7,NULL::text INTO r; RETURN NEXT r; END$$")
			userOperatorExec(t, p, b, c, "CREATE FUNCTION rn_null_table() RETURNS SETOF rn_pair LANGUAGE plpgsql AS $$DECLARE r rn_dropped; BEGIN RETURN NEXT r; END$$")
			for _, tc := range []struct {
				q    string
				rows [][]any
			}{
				{"SELECT * FROM rn_pairs(0)", [][]any{{nil, nil}}},
				{"SELECT * FROM rn_pairs(1) ORDER BY id", [][]any{{int64(7), nil}, {int64(8), "eight"}}},
				{"SELECT * FROM rn_pairs(2)", [][]any{{int64(7), nil}}},
				{"SELECT * FROM rn_pairs(3)", [][]any{{int64(7), nil}}},
				{"SELECT * FROM rn_drop()", [][]any{{int64(7), nil}}},
				{"SELECT * FROM rn_null_table()", [][]any{{nil, nil}}},
			} {
				sqlExecutionBoth(t, p, b, c, tc.q, []string{"id", "label"}, []string{"INT4", "TEXT"}, tc.rows)
			}
		})
	})
	t.Run("status_and_mixed_sink", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TYPE rn_status AS(id integer,f boolean,n bigint)",
				"CREATE FUNCTION rn_status_child() RETURNS integer LANGUAGE plpgsql AS $$BEGIN PERFORM 1 WHERE false; RETURN 2; END$$",
				"CREATE FUNCTION rn_status_rows(empty boolean) RETURNS SETOF rn_status LANGUAGE plpgsql AS $$DECLARE r rn_status; before_f boolean; before_n bigint; after_n bigint; BEGIN SELECT 1,true,1::bigint INTO r WHERE NOT empty; before_f:=FOUND; GET DIAGNOSTICS before_n=ROW_COUNT; RETURN NEXT r; IF FOUND IS DISTINCT FROM before_f THEN RAISE EXCEPTION 'FOUND changed'; END IF; GET DIAGNOSTICS r.n=ROW_COUNT; IF r.n IS DISTINCT FROM before_n THEN RAISE EXCEPTION 'ROW_COUNT changed'; END IF; r.id:=rn_status_child(); IF FOUND IS DISTINCT FROM before_f THEN RAISE EXCEPTION 'child FOUND leaked'; END IF; r.f:=FOUND; RETURN NEXT r; GET DIAGNOSTICS after_n=ROW_COUNT; IF after_n IS DISTINCT FROM before_n THEN RAISE EXCEPTION 'second NEXT count changed'; END IF; END$$",
				"CREATE TABLE rn_mix_row(id integer)", "CREATE FUNCTION rn_mix() RETURNS SETOF rn_mix_row LANGUAGE plpgsql AS $$DECLARE r rn_mix_row; f boolean; n bigint; after_n bigint; BEGIN SELECT 1 INTO r; RETURN NEXT r; RETURN QUERY EXECUTE 'SELECT 2::integer'; f:=FOUND; GET DIAGNOSTICS n=ROW_COUNT; r.id:=3; RETURN NEXT r; GET DIAGNOSTICS after_n=ROW_COUNT; IF FOUND IS DISTINCT FROM f OR after_n IS DISTINCT FROM n THEN RAISE EXCEPTION 'dynamic status changed'; END IF; END$$")
			sqlExecutionBoth(t, p, b, c, "SELECT * FROM rn_status_rows(false) ORDER BY id", []string{"id", "f", "n"}, []string{"INT4", "BOOL", "INT8"}, [][]any{{int64(1), true, int64(1)}, {int64(2), true, int64(1)}})
			sqlExecutionBoth(t, p, b, c, "SELECT * FROM rn_status_rows(true) ORDER BY id NULLS FIRST", []string{"id", "f", "n"}, []string{"INT4", "BOOL", "INT8"}, [][]any{{nil, nil, nil}, {int64(2), false, int64(0)}})
			sqlExecutionBoth(t, p, b, c, "SELECT * FROM rn_mix() ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
		})
	})
}
