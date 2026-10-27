package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLDynamicSQLInto(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, seed := range []bool{false, true} {
			t.Run(fmt.Sprintf("strict_%t_found_%t", strict, seed), func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					clause := ""
					if strict {
						clause = "STRICT "
					}
					userOperatorExec(t, p, b, c, "CREATE TABLE ds_into_input(i integer)",
						fmt.Sprintf("CREATE FUNCTION ds_into(n integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=99; status text:='ok'; BEGIN PERFORM 1 WHERE %t; BEGIN EXECUTE 'SELECT i FROM ds_into_input WHERE i<=$1 ORDER BY i DESC' INTO %sx USING n; EXCEPTION WHEN no_data_found THEN status:='P0002'; WHEN too_many_rows THEN status:='P0003'; END; RETURN status||':'||coalesce(x::text,'NULL')||':'||FOUND; END$$", seed, clause))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ds_into_input VALUES(1),(2),(3)", 3))
					for _, n := range []int{0, 1, 3} {
						value := fmt.Sprint(n)
						status := "ok"
						if n == 0 {
							value = "NULL"
						}
						if strict && n != 1 {
							value = "99"
							status = "P0003"
							if n == 0 {
								status = "P0002"
							}
						}
						plpgsqlValue(t, p, b, c, fmt.Sprintf("SELECT ds_into(%d) AS v", n), "TEXT", fmt.Sprintf("%s:%s:%t", status, value, seed))
					}
				})
			})
		}
	}
	t.Run("targets_and_clause_order", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TYPE ds_pair AS (i integer,s text)",
				"CREATE FUNCTION ds_targets() RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; n ds_pair; a integer:=10; b integer:=20; x integer; BEGIN EXECUTE 'SELECT $1::integer' USING 5 INTO x; EXECUTE 'SELECT 7::integer AS i, ''word''::text AS s' INTO r; n:=r; EXECUTE 'SELECT 9::integer AS j WHERE false' INTO r; BEGIN EXECUTE 'SELECT 7::integer,''bad''::text' INTO a,b; EXCEPTION WHEN invalid_text_representation THEN END; EXECUTE 'SELECT true' INTO found; RETURN x||':'||n.i||':'||n.s||':'||coalesce(r.j::text,'NULL')||':'||a||':'||b||':'||FOUND; END$$",
				"CREATE FUNCTION ds_target_field() RETURNS text LANGUAGE plpgsql AS $$DECLARE r ds_pair; BEGIN EXECUTE 'SELECT 11::integer,''field''::text' INTO r.i,r.s; RETURN r.i||':'||r.s; END$$")
			plpgsqlValue(t, p, b, c, "SELECT ds_targets() AS v", "TEXT", "5:7:word:NULL:7:20:true")
			plpgsqlValue(t, p, b, c, "SELECT ds_target_field() AS v", "TEXT", "11:field")
		})
	})
}

func TestReferencePLpgSQLDynamicSQLDrain(t *testing.T) {
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE TABLE ds_drain(i integer)", "CREATE SEQUENCE ds_drain_seq",
			"CREATE FUNCTION ds_dml(strict_mode boolean) RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=99; status text:='ok'; BEGIN BEGIN IF strict_mode THEN EXECUTE 'UPDATE ds_drain SET i=i+10 RETURNING 9' INTO STRICT x; ELSE EXECUTE 'UPDATE ds_drain SET i=i+10 RETURNING 9' INTO x; END IF; EXCEPTION WHEN too_many_rows THEN status:='P0003'; END; RETURN status||':'||x||':'||FOUND; END$$",
			"CREATE FUNCTION ds_drain_rows(mode integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE x bigint:=99; status text:='ok'; BEGIN BEGIN IF mode=0 THEN EXECUTE 'SELECT nextval(''ds_drain_seq'') FROM ds_drain'; ELSIF mode=1 THEN EXECUTE 'SELECT nextval(''ds_drain_seq'') FROM ds_drain' INTO x; ELSE EXECUTE 'SELECT nextval(''ds_drain_seq'') FROM ds_drain' INTO STRICT x; END IF; EXCEPTION WHEN too_many_rows THEN status:='P0003'; END; RETURN status||':'||x; END$$",
			"CREATE FUNCTION ds_late_error() RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=99; BEGIN BEGIN EXECUTE 'SELECT 12/(3-i) FROM generate_series(1,3) g(i)' INTO STRICT x; EXCEPTION WHEN division_by_zero THEN RETURN '22012:'||x; WHEN too_many_rows THEN RETURN 'P0003:'||x; END; RETURN 'unexpected'; END$$",
			"CREATE FUNCTION ds_multi() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN EXECUTE ''; EXECUTE 'SELECT 8; SELECT 9' INTO x; RETURN x; END$$",
			"CREATE FUNCTION ds_no_descriptor() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN EXECUTE 'UPDATE ds_drain SET i=i' INTO x; RETURN x; END$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ds_drain VALUES(1),(2),(3)", 3))
		plpgsqlValue(t, p, b, c, "SELECT ds_dml(true) AS v", "TEXT", "P0003:99:false")
		plpgsqlValue(t, p, b, c, "SELECT sum(i) AS v FROM ds_drain", "INT8", int64(6))
		plpgsqlValue(t, p, b, c, "SELECT ds_dml(false) AS v", "TEXT", "ok:9:false")
		plpgsqlValue(t, p, b, c, "SELECT sum(i) AS v FROM ds_drain", "INT8", int64(66))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE ds_drain SET i=i-20", 3))
		for _, mode := range []int{0, 1, 2} {
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER SEQUENCE ds_drain_seq RESTART WITH 1", 0))
				want := "ok:99"
				if mode == 1 {
					want = "ok:1"
				}
				if mode == 2 {
					want = "P0003:99"
				}
				s := fixture.Select(fmt.Sprintf("SELECT ds_drain_rows(%d) AS v", mode), []string{"v"}, []string{"TEXT"}, [][]any{{want}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				plpgsqlValue(t, p, b, c, "SELECT last_value AS v FROM ds_drain_seq", "INT8", int64(3))
			}
		}
		plpgsqlValue(t, p, b, c, "SELECT ds_late_error() AS v", "TEXT", "22012:99")
		plpgsqlValue(t, p, b, c, "SELECT ds_multi() AS v", "INT4", int64(9))
		for _, sql := range []string{"SELECT i FROM ds_drain", "SELECT i FROM ds_drain WHERE false", "UPDATE ds_drain SET i=i RETURNING i"} {
			q := "DO $$BEGIN EXECUTE '" + sql + "'; END$$"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(q, 0)))
		}
		plpgsqlError(t, p, c, "SELECT ds_no_descriptor()", true, plpgsqlRuntimeDiagnostic("42601", "INTO used with a command that cannot return data", "ds_no_descriptor()", "EXECUTE"))
	})
	t.Run("protected_prefix_and_auxiliary_cte", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE ds_protected_marks(i integer)", "CREATE SEQUENCE ds_protected_seq",
				"CREATE FUNCTION ds_protected() RETURNS text LANGUAGE plpgsql AS $$DECLARE x bigint:=99; status text:='missed'; BEGIN INSERT INTO ds_protected_marks VALUES(1); BEGIN EXECUTE 'INSERT INTO ds_protected_marks VALUES(nextval(''ds_protected_seq'')),(nextval(''ds_protected_seq'')); SELECT nextval(''ds_protected_seq'')/(3-i) FROM generate_series(1,3) g(i)' INTO STRICT x; EXCEPTION WHEN division_by_zero THEN status:='22012'; END; RETURN status||':'||x; END$$",
				"CREATE FUNCTION ds_auxiliary() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN EXECUTE 'WITH c AS (INSERT INTO ds_protected_marks VALUES(4),(5),(6) RETURNING i) SELECT 9::integer' INTO x; RETURN x; END$$")
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER SEQUENCE ds_protected_seq RESTART WITH 1", 0))
				s := fixture.Select("SELECT ds_protected() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"22012:99"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				plpgsqlValue(t, p, b, c, "SELECT i AS v FROM ds_protected_marks", "INT4", int64(1))
				plpgsqlValue(t, p, b, c, "SELECT last_value AS v FROM ds_protected_seq", "INT8", int64(5))
				s = fixture.Select("SELECT ds_auxiliary() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(9)}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT i AS v FROM ds_protected_marks ORDER BY i", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}, {int64(5)}, {int64(6)}}))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ds_protected_marks", 4))
			}
		})
	})
}
