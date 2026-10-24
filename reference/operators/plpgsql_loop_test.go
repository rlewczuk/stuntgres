package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func loopScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func loopBody(t *testing.T, body string, want int64) {
	t.Helper()
	loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE FUNCTION loop_value() RETURNS integer LANGUAGE plpgsql AS $$"+body+"$$")
		plpgsqlValue(t, p, b, c, "SELECT loop_value() AS v", "INT4", want)
	})
}

func TestReferencePLpgSQLLoopBounded(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int64
	}{
		{"five", "DECLARE n integer:=0; s integer:=0; BEGIN LOOP n:=n+1; s:=s+n; EXIT WHEN n=5; END LOOP; RETURN n*100+s; END", 515},
		{"nested_return", "BEGIN LOOP LOOP RETURN 7; END LOOP; RETURN 1/0; END LOOP; RETURN 1/0; END", 7},
		{"found_unchanged", "BEGIN LOOP EXIT; END LOOP; IF FOUND THEN RETURN 99; END IF; RETURN 7; END", 7},
	} {
		t.Run(tc.name, func(t *testing.T) { loopBody(t, tc.body, tc.want) })
	}
	loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "DO $$DECLARE n integer:=0; BEGIN LOOP n:=n+1; EXIT WHEN n=3; END LOOP; END$$"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec(q, 0)))
	})
}

func TestReferencePLpgSQLLoopContinue(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int64
	}{
		{"odd_sum", "DECLARE n integer:=0; s integer:=0; BEGIN LOOP n:=n+1; EXIT WHEN n>5; CONTINUE WHEN n%2=0; s:=s+n; END LOOP; RETURN n*100+s; END", 609},
		{"unconditional", "DECLARE n integer:=0; BEGIN LOOP n:=n+1; EXIT WHEN n=3; CONTINUE; n:=loop_missing(); END LOOP; RETURN n; END", 3},
		{"innermost", "DECLARE i integer:=0; j integer; s integer:=0; BEGIN LOOP i:=i+1; j:=0; LOOP j:=j+1; CONTINUE WHEN j=2; EXIT WHEN j=3; s:=s+i; END LOOP; s:=s+100; EXIT WHEN i=3; END LOOP; RETURN s; END", 306},
	} {
		t.Run(tc.name, func(t *testing.T) { loopBody(t, tc.body, tc.want) })
	}
}

func TestReferencePLpgSQLLoopConditions(t *testing.T) {
	t.Run("reached_binding", loopReachedConditions)
	for _, tc := range []struct {
		name, body string
		want       int64
	}{
		{"null_false", "DECLARE n integer:=0; s integer:=0; BEGIN LOOP n:=n+1; EXIT WHEN NULL::boolean; CONTINUE WHEN NULL::boolean; EXIT WHEN false; CONTINUE WHEN false; s:=s+1; EXIT WHEN n=3; END LOOP; RETURN n*10+s; END", 33},
		{"conversion", "DECLARE n integer:=0; BEGIN LOOP n:=n+1; EXIT WHEN 0; CONTINUE WHEN 0; EXIT WHEN 'yes'::text; END LOOP; RETURN n; END", 1},
		{"numeric_continue", "DECLARE n integer:=0; BEGIN LOOP n:=n+1; EXIT WHEN n=3; CONTINUE WHEN 1; RETURN 99; END LOOP; RETURN n; END", 3},
		{"unreached_invalid", "BEGIN LOOP EXIT; CONTINUE WHEN 'nope'::text; END LOOP; RETURN 7; END", 7},
		{"case_comment", "DECLARE \"exit\" integer:=0; BEGIN LOOP \"exit\":=\"exit\"+1; EXIT /* WHEN ignored */ WHEN CASE WHEN \"exit\"=3 THEN true ELSE false END; END LOOP; RETURN \"exit\"; END", 3},
	} {
		t.Run(tc.name, func(t *testing.T) { loopBody(t, tc.body, tc.want) })
	}
	loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SEQUENCE loop_sequence",
			"CREATE FUNCTION loop_volatile() RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer:=0; BEGIN LOOP n:=n+1; EXIT WHEN nextval('loop_sequence')>=3; END LOOP; RETURN n; END$$",
			"CREATE FUNCTION loop_limit(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer:=0; BEGIN LOOP n:=n+1; EXIT WHEN n>=x; END LOOP; RETURN n; END$$")
		q := "SELECT loop_volatile() AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		plpgsqlValue(t, p, b, c, "SELECT CASE WHEN is_called THEN 99 ELSE 0 END AS v FROM loop_sequence", "INT4", int64(0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}))
		plpgsqlValue(t, p, b, c, "SELECT last_value AS v FROM loop_sequence", "INT8", int64(3))
		parametersHeld(t, p, b, c, "SELECT loop_limit($1) AS v", "INT4", [][]any{{1}, {4}, {2}, {1}}, []any{int64(1), int64(4), int64(2), int64(1)})
	})
}

func TestReferencePLpgSQLLoopLabels(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int64
	}{
		{"outer_continue", "DECLARE i integer:=0; s integer:=0; BEGIN <<o>> LOOP i:=i+1; EXIT WHEN i>3; LOOP s:=s+i; IF true THEN CASE WHEN true THEN CONTINUE o; END CASE; END IF; END LOOP; s:=s+1000; END LOOP o; RETURN s; END", 6},
		{"outer_exit", "DECLARE i integer:=0; s integer:=0; BEGIN <<o>> LOOP i:=i+1; <<inner_loop>> LOOP s:=s+i; EXIT o WHEN i=2; EXIT inner_loop; END LOOP; s:=s+10; END LOOP; RETURN s; END", 13},
		{"block_exit", "DECLARE n integer:=0; BEGIN <<b>> BEGIN LOOP n:=n+1; EXIT b WHEN n=2; END LOOP; n:=999; END b; RETURN n; END", 2},
		{"default_ignores_block", "DECLARE n integer:=0; BEGIN LOOP <<b>> BEGIN n:=n+1; EXIT; n:=999; END b; n:=998; END LOOP; RETURN n; END", 1},
		{"nearest_duplicate", "DECLARE n integer:=0; BEGIN <<x>> LOOP <<x>> LOOP n:=n+1; EXIT x; END LOOP x; n:=n+10; EXIT; END LOOP x; RETURN n; END", 11},
		{"quoted_unicode", "DECLARE n integer:=0; BEGIN <<\"Étage\">> LOOP n:=n+1; CONTINUE \"Étage\" WHEN n<3; EXIT \"Étage\"; END LOOP \"Étage\"; RETURN n; END", 3},
		{"reenter_block", "DECLARE n integer:=0; s integer:=0; BEGIN <<o>> LOOP n:=n+1; EXIT WHEN n>3; DECLARE x integer:=10; BEGIN x:=x+1; s:=s+x; CONTINUE o; END; END LOOP; RETURN s; END", 33},
		{"handler_transfer", "DECLARE n integer:=0; BEGIN <<o>> LOOP n:=n+1; BEGIN n:=n+1/0; EXCEPTION WHEN division_by_zero THEN CONTINUE o WHEN n<3; EXIT o; END; RETURN 99; END LOOP; RETURN n; END", 3},
	} {
		t.Run(tc.name, func(t *testing.T) { loopBody(t, tc.body, tc.want) })
	}
	loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE FUNCTION loop_visible() RETURNS void LANGUAGE plpgsql AS $$<<visible>> BEGIN EXIT visible; END visible$$")
		plpgsqlValue(t, p, b, c, "SELECT loop_visible() IS NOT NULL AS v", "BOOL", true)
	})
}
