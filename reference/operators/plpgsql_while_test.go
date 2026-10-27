package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func whileScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		body(t, p, b, c)
	})
}

func whileBody(t *testing.T, body string, want int64) {
	t.Helper()
	whileScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE FUNCTION while_value() RETURNS integer LANGUAGE plpgsql AS $$"+body+"$$")
		plpgsqlValue(t, p, b, c, "SELECT while_value() AS v", "INT4", want)
	})
}

func TestReferencePLpgSQLWhileEntry(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int64
	}{
		{"false", "DECLARE n integer:=0; BEGIN WHILE false LOOP n:=n+1; END LOOP; RETURN n; END", 0},
		{"null", "DECLARE n integer:=0; BEGIN WHILE NULL::boolean LOOP n:=n+1; END LOOP; RETURN n; END", 0},
		{"one", "DECLARE n integer:=0; BEGIN WHILE n<1 LOOP n:=n+1; END LOOP; RETURN n; END", 1},
		{"five", "DECLARE n integer:=0; s integer:=0; BEGIN WHILE n<5 LOOP n:=n+1; s:=s+n; END LOOP; RETURN n*100+s; END", 515},
		{"empty_false", "BEGIN WHILE false LOOP END LOOP; RETURN 0; END", 0},
		{"unreached_body", "DECLARE n integer:=0; BEGIN WHILE false LOOP n:=while_missing(); END LOOP; RETURN n; END", 0},
		{"found_unchanged", "DECLARE n integer:=0; BEGIN WHILE n<3 LOOP n:=n+1; END LOOP; IF FOUND THEN RETURN 99; END IF; RETURN n; END", 3},
		{"contextual_names", "DECLARE \"while\" integer:=0; BEGIN WHILE \"while\"<3 LOOP \"while\":=\"while\"+1; END LOOP; RETURN \"while\"; END", 3},
		{"grouping_comments_case", "DECLARE \"é\" integer:=0; BEGIN WHILE /* LOOP ignored */ (CASE WHEN \"é\"<3 THEN true ELSE false END) LOOP \"é\":=\"é\"+1; END LOOP; RETURN \"é\"; END", 3},
	} {
		t.Run(tc.name, func(t *testing.T) { whileBody(t, tc.body, tc.want) })
	}
	whileScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		plpgsqlValue(t, p, b, c, "SELECT while AS v FROM (SELECT 3 AS while) AS named", "INT4", int64(3))
		q := "DO $$DECLARE n integer:=0; BEGIN WHILE n<3 LOOP n:=n+1; END LOOP; END$$"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(q, 0)))
	})
}

func TestReferencePLpgSQLWhileControl(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int64
	}{
		{"own_continue", "DECLARE n integer:=0; BEGIN WHILE n<3 LOOP n:=n+1; EXIT WHEN n>3; CONTINUE; n:=while_missing(); END LOOP; RETURN n; END", 3},
		{"outer_continue", "DECLARE i integer:=0; s integer:=0; BEGIN <<o>> WHILE i<3 LOOP i:=i+1; LOOP s:=s+i; IF true THEN CASE WHEN true THEN CONTINUE o; END CASE; END IF; END LOOP; s:=s+1000; END LOOP o; RETURN s; END", 6},
		{"outer_exit", "DECLARE i integer:=0; s integer:=0; BEGIN <<o>> WHILE true LOOP i:=i+1; LOOP s:=s+i; EXIT o WHEN i=2; EXIT; END LOOP; s:=s+10; END LOOP; RETURN s; END", 13},
		{"block_exit", "DECLARE n integer:=0; BEGIN <<b>> BEGIN WHILE true LOOP n:=n+1; EXIT b WHEN n=2; END LOOP; n:=999; END b; RETURN n; END", 2},
		{"loop_outer_while_inner", "DECLARE i integer:=0; j integer; s integer:=0; BEGIN <<o>> LOOP i:=i+1; EXIT WHEN i>3; j:=0; WHILE j<3 LOOP j:=j+1; CONTINUE WHEN j=2; EXIT WHEN j=3; s:=s+i; END LOOP; s:=s+100; END LOOP; RETURN s; END", 306},
		{"while_outer_loop_inner", "DECLARE i integer:=0; j integer; s integer:=0; BEGIN WHILE i<3 LOOP i:=i+1; j:=0; LOOP j:=j+1; CONTINUE WHEN j=2; EXIT WHEN j=3; s:=s+i; END LOOP; s:=s+100; END LOOP; RETURN s; END", 306},
		{"continue_outer_loop", "DECLARE n integer:=0; BEGIN <<o>> LOOP EXIT WHEN n=3; WHILE true LOOP n:=n+1; CONTINUE o; END LOOP; RETURN 99; END LOOP; RETURN n; END", 3},
		{"exit_outer_loop", "DECLARE n integer:=0; BEGIN <<o>> LOOP WHILE true LOOP n:=n+1; EXIT o; END LOOP; RETURN 99; END LOOP; RETURN n; END", 1},
		{"nearest_duplicate", "DECLARE n integer:=0; BEGIN <<x>> WHILE true LOOP <<x>> WHILE true LOOP n:=n+1; EXIT x; END LOOP x; n:=n+10; EXIT; END LOOP x; RETURN n; END", 11},
		{"quoted_unicode", "DECLARE n integer:=0; BEGIN <<\"Étage\">> WHILE n<3 LOOP n:=n+1; CONTINUE \"Étage\"; END LOOP \"Étage\"; RETURN n; END", 3},
		{"handler_transfer", "DECLARE n integer:=0; BEGIN <<o>> WHILE n<3 LOOP n:=n+1; BEGIN n:=n+1/0; EXCEPTION WHEN division_by_zero THEN CONTINUE o WHEN n<3; EXIT o; END; RETURN 99; END LOOP; RETURN n; END", 3},
		{"reenter_block", "DECLARE n integer:=0; s integer:=0; BEGIN <<o>> WHILE n<3 LOOP n:=n+1; DECLARE x integer:=10; BEGIN x:=x+1; s:=s+x; CONTINUE o; END; END LOOP; RETURN s; END", 33},
		{"return", "BEGIN WHILE true LOOP WHILE true LOOP RETURN 7; END LOOP; RETURN 1/0; END LOOP; RETURN 1/0; END", 7},
	} {
		t.Run(tc.name, func(t *testing.T) { whileBody(t, tc.body, tc.want) })
	}
}
