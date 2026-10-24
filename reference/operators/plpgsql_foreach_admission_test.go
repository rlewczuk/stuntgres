package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLForeachAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int64
	}{
		{"ordinary_slice_local", `DECLARE slice integer:=1; BEGIN slice:=slice+1; RETURN slice; END`, 2},
		{"slice_target", `DECLARE slice integer:=99; BEGIN FOREACH slice IN ARRAY '{1,2}'::integer[] LOOP END LOOP; RETURN slice; END`, 2},
		{"slice_loop_label", `DECLARE x integer; n integer:=0; BEGIN <<slice>> FOREACH x IN ARRAY '{1,2,3}'::integer[] LOOP n:=n+x; CONTINUE slice WHEN x=1; EXIT slice WHEN x=2; END LOOP slice; RETURN n; END`, 3},
		{"slice_block_qualified", `<<slice>> DECLARE x integer:=99; BEGIN FOREACH slice.x IN ARRAY '{1,2}'::integer[] LOOP END LOOP; RETURN slice.x; END slice`, 2},
		{"quoted_slice_local", `DECLARE "slice" integer:=1; BEGIN "slice":="slice"+1; RETURN "slice"; END`, 2},
		{"quoted_slice_target", `DECLARE "slice" integer:=99; BEGIN FOREACH "slice" IN ARRAY '{1,2}'::integer[] LOOP END LOOP; RETURN "slice"; END`, 2},
		{"quoted_slice_label", `DECLARE x integer; n integer:=0; BEGIN <<"slice">> FOREACH x IN ARRAY '{1,2,3}'::integer[] LOOP n:=n+x; CONTINUE "slice" WHEN x=1; EXIT "slice" WHEN x=2; END LOOP "slice"; RETURN n; END`, 3},
		{"slice_named_slice_target", `DECLARE slice integer[]; n integer:=0; BEGIN FOREACH slice SLICE 1 IN ARRAY '{{1,2},{3,4}}'::integer[] LOOP n:=n+1; END LOOP; RETURN n; END`, 2},
		{"nonshadowing_slice_clause", `DECLARE x integer[]; n integer:=0; BEGIN FOREACH x SLICE 1 IN ARRAY '{{1,2},{3,4}}'::integer[] LOOP n:=n+1; END LOOP; RETURN n; END`, 2},
		{"quoted_slice_label_clause", `DECLARE x integer[]; n integer:=0; BEGIN <<"slice">> FOREACH x SLICE 1 IN ARRAY '{{1,2},{3,4}}'::integer[] LOOP n:=n+1; END LOOP "slice"; RETURN n; END`, 2},
		{"slice_field_slice_target", `DECLARE r fe_admit_slice_box; n integer:=0; BEGIN FOREACH r.slice SLICE 1 IN ARRAY '{{1,2},{3,4}}'::integer[] LOOP n:=n+1; END LOOP; RETURN n; END`, 2},
	} {
		for _, preparedDDL := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/direct_definition", true: "/prepared_definition"}[preparedDDL], func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					definition := "CREATE FUNCTION fe_admit_slice() RETURNS integer LANGUAGE plpgsql AS $$" + tc.body + "$$"
					if tc.name == "slice_named_slice_target" {
						want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "SLICE"`)
						want.Position = "131"
						plpgsqlError(t, p, c, definition, false, want)
						plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='fe_admit_slice'", "INT8", int64(0))
						userOperatorExec(t, p, b, c, "CREATE FUNCTION fe_admit_slice() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 42; END$$")
						plpgsqlValue(t, p, b, c, "SELECT fe_admit_slice() AS v", "INT4", int64(42))
						return
					}
					if tc.name == "slice_field_slice_target" {
						userOperatorExec(t, p, b, c, "CREATE TYPE fe_admit_slice_box AS (slice integer[])")
					}
					s := fixture.Exec(definition, 0)
					s.Prepared = preparedDDL
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					q := "SELECT fe_admit_slice() AS v"
					plpgsqlValue(t, p, b, c, q, "INT4", tc.want)
					foreachHeld(t, p, b, c, q, "INT4", [][]any{nil, nil}, []any{tc.want, tc.want})
				})
			})
		}
	}
	for _, tc := range []struct{ name, declarations, target, message, position, unreachablePosition string }{
		{"record_first", "r record; x integer;", "r,x", `syntax error at or near ","`, "130", "144"},
		{"record_second", "r record; x integer;", "x,r", `"r" is not a scalar variable`, "131", "145"},
		{"row_first", "r fe_admit_pair; x integer;", "r,x", `syntax error at or near ","`, "137", "151"},
		{"row_second", "r fe_admit_pair; x integer;", "x,r", `"r" is not a scalar variable`, "138", "152"},
		{"element", "a integer[];", "a[1]", `syntax error at or near "["`, "122", "136"},
		{"slice", "a integer[];", "a[1:2]", `syntax error at or near "["`, "122", "136"},
		{"field_element", "r fe_admit_box;", "r.a[1]", `syntax error at or near "["`, "127", "141"},
		{"element_first", "a integer[]; x integer;", "a[1],x", `syntax error at or near "["`, "133", "147"},
		{"element_second", "a integer[]; x integer;", "x,a[1]", `syntax error at or near "["`, "135", "149"},
	} {
		for _, unreachable := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/reached", true: "/unreachable"}[unreachable], func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c, "CREATE TYPE fe_admit_pair AS (a integer,b text)", "CREATE TYPE fe_admit_box AS (a integer[])")
					loop := "FOREACH " + tc.target + " IN ARRAY '{1}'::integer[] LOOP END LOOP;"
					if unreachable {
						loop = "IF false THEN " + loop + " END IF;"
					}
					q := "CREATE FUNCTION fe_admit_bad() RETURNS integer LANGUAGE plpgsql AS $$\n-- é admission\nDECLARE " + tc.declarations + "\nBEGIN\n" + loop + "\nRETURN 0; END$$"
					want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
					want.Position = tc.position
					if unreachable {
						want.Position = tc.unreachablePosition
					}
					plpgsqlError(t, p, c, q, false, want)
					plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='fe_admit_bad'", "INT8", int64(0))
					userOperatorExec(t, p, b, c, "CREATE FUNCTION fe_admit_bad() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN FOREACH x IN ARRAY '{42}'::integer[] LOOP END LOOP; RETURN x; END$$")
					plpgsqlValue(t, p, b, c, "SELECT fe_admit_bad() AS v", "INT4", int64(42))
				})
			})
		}
	}
}
