package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const foreachElementsDefinition = `CREATE FUNCTION fe_elements(a integer[]) RETURNS text LANGUAGE plpgsql AS $$
DECLARE x integer := 99; n integer := 0; s text := '';
BEGIN
  FOREACH x IN ARRAY a LOOP
    n := n + 1;
    s := s || coalesce(x::text,'N') || ',';
  END LOOP;
  RETURN n::text || ':' || s || ':' || coalesce(x::text,'N') || ':' || FOUND::text;
END $$`

func TestReferencePLpgSQLForeachElements(t *testing.T) {
	for _, preparedDDL := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct_definition", true: "prepared_definition"}[preparedDDL], func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				s := fixture.Exec(foreachElementsDefinition, 0)
				s.Prepared = preparedDDL
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				q := "SELECT fe_elements($1::integer[]) AS v"
				inputs := [][]any{{"{}"}, {"{7}"}, {"{2,2,NULL,3}"}, {"{NULL}"}, {"{{1,2},{3,4}}"}, {"[-1:0][3:4]={{1,NULL},{2,3}}"}, {"{{{1,2},{3,4}},{{5,6},{7,8}}}"}, {"{}"}}
				values := []any{"0::99:false", "1:7,:7:true", "4:2,2,N,3,:3:true", "1:N,:N:true", "4:1,2,3,4,:4:true", "4:1,N,2,3,:3:true", "8:1,2,3,4,5,6,7,8,:8:true", "0::99:false"}
				for i, args := range inputs {
					userOperatorValue(t, p, b, c, q, "TEXT", values[i], args...)
				}
				foreachHeld(t, p, b, c, q, "TEXT", inputs, values)
				userOperatorExec(t, p, b, c,
					"CREATE FUNCTION fe_zero(a integer[]) RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer; s text:=''; BEGIN FOREACH x SLICE 0 IN ARRAY a LOOP s:=s||coalesce(x::text,'N')||','; END LOOP; RETURN s; END$$",
					"DO $$DECLARE x integer; BEGIN FOREACH x IN ARRAY '{1,2}'::integer[] LOOP END LOOP; IF x<>2 THEN RAISE EXCEPTION 'wrong last target'; END IF; END$$")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("DO $$DECLARE x integer; BEGIN FOREACH x IN ARRAY '{1,2}'::integer[] LOOP END LOOP; IF x<>2 THEN RAISE EXCEPTION 'wrong last target'; END IF; END$$", 0)))
				userOperatorValue(t, p, b, c, "SELECT fe_zero($1::integer[]) AS v", "TEXT", "1,2,3,4,", "{{1,2},{3,4}}")
			})
		})
	}
}

func TestReferencePLpgSQLForeachSlices(t *testing.T) {
	for _, tc := range []struct {
		name, level, input, want string
	}{
		{"rows", "1", "[-1:0][3:4]={{1,NULL},{2,3}}", "[3:4]={1,NULL};[3:4]={2,3};"},
		{"whole_rank2", "2", "[-1:0][3:4]={{1,NULL},{2,3}}", "[-1:0][3:4]={{1,NULL},{2,3}};"},
		{"rank3_lines", "1", "[0:1][-2:-1][5:6]={{{1,2},{3,4}},{{5,6},{7,8}}}", "[5:6]={1,2};[5:6]={3,4};[5:6]={5,6};[5:6]={7,8};"},
		{"rank3_planes", "2", "[0:1][-2:-1][5:6]={{{1,2},{3,4}},{{5,6},{7,8}}}", "[-2:-1][5:6]={{1,2},{3,4}};[-2:-1][5:6]={{5,6},{7,8}};"},
		{"whole_rank3", "3", "[0:1][-2:-1][5:6]={{{1,2},{3,4}},{{5,6},{7,8}}}", "[0:1][-2:-1][5:6]={{{1,2},{3,4}},{{5,6},{7,8}}};"},
		{"null_slices", "1", "{{NULL,NULL},{NULL,NULL}}", "{NULL,NULL};{NULL,NULL};"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION fe_slices(a integer[]) RETURNS text LANGUAGE plpgsql AS $$DECLARE x integer[]; first_slice integer[]; s text:=''; BEGIN FOREACH x SLICE "+tc.level+" IN ARRAY a LOOP IF first_slice IS NULL THEN first_slice:=x; END IF; IF x IS NULL THEN RAISE EXCEPTION 'NULL slice'; END IF; s:=s||x::text||';'; END LOOP; RETURN s||'first='||first_slice::text; END$$")
				// The saved first slice is inspected after all subsequent deliveries.
				first := tc.want
				for i, ch := range first {
					if ch == ';' {
						first = first[:i]
						break
					}
				}
				want := tc.want + "first=" + first
				q := "SELECT fe_slices($1::integer[]) AS v"
				userOperatorValue(t, p, b, c, q, "TEXT", want, tc.input)
				foreachHeld(t, p, b, c, q, "TEXT", [][]any{{tc.input}, {tc.input}}, []any{want, want})
			})
		})
	}
}

func foreachHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, typ string, inputs [][]any, values []any) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i, args := range inputs {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder,
			fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{values[i]}}, args...))
	}
}
