package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLIntegerForBounds(t *testing.T) {
	for _, tc := range []struct {
		name, bounds, body, want string
		calls                    int64
	}{
		{"once", "if_mark(1,1)..if_mark(3,2) BY if_mark(1,3)", "IF (SELECT count(*) FROM if_marks)<>3 THEN RETURN 'late bounds'; END IF; s:=s||i::text;", "123", 3},
		{"empty", "if_mark(3,1)..if_mark(1,2) BY if_mark(1,3)", "s:=s||i::text;", "", 3},
		{"reentry", "if_mark(1,1)..if_mark(2,2) BY if_mark(1,3)", "s:=s||i::text;", "1212", 6},
		{"mutation", "1..hi BY stride", "s:=s||i::text; hi:=1; stride:=99;", "123", 0},
		{"text_conversion", "'1'::text..'3'::text BY '1'::text", "s:=s||i::text;", "123", 0},
		{"numeric_conversion", "1.2::numeric..2.8::numeric BY 1.0::numeric", "s:=s||i::text;", "123", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				loop := "FOR i IN " + tc.bounds + " LOOP " + tc.body + " END LOOP;"
				if tc.name == "reentry" {
					loop = "FOR outer_i IN 1..2 LOOP " + loop + " END LOOP;"
				}
				userOperatorExec(t, p, b, c,
					"CREATE TABLE if_marks(ord integer, marker integer)",
					"CREATE FUNCTION if_mark(v integer,m integer) RETURNS integer LANGUAGE SQL AS 'INSERT INTO if_marks SELECT count(*)+1,m FROM if_marks; SELECT v'",
					"CREATE FUNCTION if_bounds() RETURNS text LANGUAGE plpgsql AS $$DECLARE s text:=''; hi integer:=3; stride integer:=1; BEGIN "+loop+" RETURN s; END$$")
				q := "SELECT if_bounds() AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM if_marks", "INT8", int64(0))
				for _, prepared := range []bool{false, true} {
					s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}})
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					rows := [][]any(nil)
					for n := int64(0); n < tc.calls; n++ {
						rows = append(rows, []any{n + 1, n%3 + 1})
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT ord,marker FROM if_marks ORDER BY ord", []string{"ord", "marker"}, []string{"INT4", "INT4"}, rows))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM if_marks", tc.calls))
				}
			})
		})
	}
	for _, tc := range []struct{ name, bounds, want string }{
		{"lower_failure", "if_sequence(NULL,1)..if_sequence(3,2) BY if_sequence(1,3)", "1"},
		{"upper_failure", "if_sequence(1,1)..if_sequence(NULL,2) BY if_sequence(1,3)", "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				// Sequence changes survive the caught subtransaction error, unlike rows.
				userOperatorExec(t, p, b, c,
					"CREATE SEQUENCE if_eval_order",
					"CREATE FUNCTION if_sequence(v integer,m integer) RETURNS integer LANGUAGE SQL AS 'SELECT setval(''if_eval_order'',m); SELECT v'",
					"CREATE FUNCTION if_failed_bounds() RETURNS bigint LANGUAGE plpgsql AS $$BEGIN BEGIN FOR i IN "+tc.bounds+" LOOP END LOOP; EXCEPTION WHEN null_value_not_allowed THEN RETURN currval('if_eval_order'); END; RETURN -1; END$$")
				want := int64(1)
				if tc.want == "2" {
					want = 2
				}
				plpgsqlValue(t, p, b, c, "SELECT if_failed_bounds() AS v", "INT8", want)
			})
		})
	}
}
