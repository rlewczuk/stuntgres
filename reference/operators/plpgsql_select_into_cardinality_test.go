package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSelectIntoCardinality(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(fmt.Sprintf("strict_%t", strict), func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				clause := ""
				if strict {
					clause = "STRICT "
				}
				userOperatorExec(t, p, b, c,
					"CREATE TABLE si_card_input(id integer)",
					"CREATE FUNCTION si_card(n integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=99; f boolean; status text:='ok'; BEGIN BEGIN SELECT id INTO "+clause+"x FROM si_card_input WHERE id<=n ORDER BY id DESC; EXCEPTION WHEN no_data_found THEN status:='P0002'; WHEN SQLSTATE 'P0003' THEN status:='P0003'; END; f:=FOUND; RETURN status||':'||coalesce(x::text,'NULL')||':'||f; END$$")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO si_card_input VALUES(1),(2),(3)", 3))
				for _, tc := range []struct {
					n    int
					want string
				}{{0, "ok:NULL:false"}, {1, "ok:1:true"}, {3, "ok:3:true"}} {
					if strict && tc.n == 0 {
						tc.want = "P0002:99:false"
					}
					if strict && tc.n == 3 {
						tc.want = "P0003:99:true"
					}
					s := fixture.Select("SELECT si_card($1) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.n)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
				}
			})
		})
	}
	t.Run("found_order_and_isolation", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION si_found_child() RETURNS boolean LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN SELECT 1 INTO x WHERE false; RETURN FOUND; END$$",
				"CREATE FUNCTION si_found() RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer:=9; initial boolean; empty boolean; nullrow boolean; nested boolean; final boolean; BEGIN initial:=FOUND; SELECT NULL::integer INTO x WHERE false; empty:=FOUND; SELECT NULL::integer INTO x; nullrow:=FOUND; nested:=si_found_child(); final:=FOUND; SELECT false INTO found; RETURN initial||':'||empty||':'||nullrow||':'||nested||':'||final||':'||FOUND; END$$",
				"CREATE FUNCTION si_found_sql_error() RETURNS boolean LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN SELECT 1 INTO x; BEGIN SELECT missing INTO x; EXCEPTION WHEN undefined_column THEN RETURN FOUND; END; RETURN false; END$$")
			plpgsqlValue(t, p, b, c, "SELECT si_found() AS v", "TEXT", "false:false:true:false:true:false")
			plpgsqlValue(t, p, b, c, "SELECT si_found_sql_error() AS v", "BOOL", true)
		})
	})
}
