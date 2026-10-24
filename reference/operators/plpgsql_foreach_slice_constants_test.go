package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLForeachSliceConstants(t *testing.T) {
	for _, tc := range []struct{ token, target, want string }{
		{"0", "integer", "1;2;3;4;"},
		{"00", "integer", "1;2;3;4;"},
		{"0_0", "integer", "1;2;3;4;"},
		{"0x0", "integer", "1;2;3;4;"},
		{"0o0", "integer", "1;2;3;4;"},
		{"0b0", "integer", "1;2;3;4;"},
		{"1", "integer[]", "{1,2};{3,4};"},
		{"01", "integer[]", "{1,2};{3,4};"},
		{"0_1", "integer[]", "{1,2};{3,4};"},
		{"0x1", "integer[]", "{1,2};{3,4};"},
		{"0X1", "integer[]", "{1,2};{3,4};"},
		{"0o1", "integer[]", "{1,2};{3,4};"},
		{"0O1", "integer[]", "{1,2};{3,4};"},
		{"0b1", "integer[]", "{1,2};{3,4};"},
		{"0B1", "integer[]", "{1,2};{3,4};"},
		{"0x_1", "integer[]", "{1,2};{3,4};"},
		{"0o_1", "integer[]", "{1,2};{3,4};"},
		{"0b_1", "integer[]", "{1,2};{3,4};"},
		{"2", "integer[]", "{{1,2},{3,4}};"},
		{"02", "integer[]", "{{1,2},{3,4}};"},
		{"0_2", "integer[]", "{{1,2},{3,4}};"},
		{"0x2", "integer[]", "{{1,2},{3,4}};"},
		{"0o2", "integer[]", "{{1,2},{3,4}};"},
		{"0b10", "integer[]", "{{1,2},{3,4}};"},
		{"0b1_0", "integer[]", "{{1,2},{3,4}};"},
	} {
		for _, preparedDDL := range []bool{false, true} {
			t.Run(tc.token+map[bool]string{false: "/direct_definition", true: "/prepared_definition"}[preparedDDL], func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					q := "CREATE FUNCTION fe_slice_literal() RETURNS text LANGUAGE plpgsql AS $$DECLARE x " + tc.target + "; s text:=''; BEGIN FOREACH x SLICE " + tc.token + " IN ARRAY '{{1,2},{3,4}}'::integer[] LOOP s:=s||x::text||';'; END LOOP; RETURN s; END$$"
					s := fixture.Exec(q, 0)
					s.Prepared = preparedDDL
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					q = "SELECT fe_slice_literal() AS v"
					plpgsqlValue(t, p, b, c, q, "TEXT", tc.want)
					foreachHeld(t, p, b, c, q, "TEXT", [][]any{nil, nil}, []any{tc.want, tc.want})
				})
			})
		}
	}
	for _, tc := range []struct{ token, value string }{
		{"010", "10"},
		{"08", "8"},
		{"0_8", "8"},
		{"2147483647", "2147483647"},
		{"2_147_483_647", "2147483647"},
		{"0x7fff_ffff", "2147483647"},
		{"0o17_777_777_777", "2147483647"},
		{"0b111_1111_1111_1111_1111_1111_1111_1111", "2147483647"},
	} {
		t.Run("rank_boundary/"+tc.token, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				loop := "FOREACH x SLICE " + tc.token + " IN ARRAY '{{1,2},{3,4}}'::integer[] LOOP END LOOP;"
				for _, preparedDDL := range []bool{false, true} {
					s := fixture.Exec("CREATE FUNCTION fe_slice_bound() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer[]; BEGIN "+loop+" RETURN 42; END$$", 0)
					s.Prepared = preparedDDL
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					q := "SELECT fe_slice_bound() AS v"
					want := plpgsqlRuntimeDiagnostic("2202E", "slice dimension ("+tc.value+") is out of the valid range 0..2", "fe_slice_bound()", "FOREACH over array")
					plpgsqlError(t, p, c, q, true, want)
					userOperatorExec(t, p, b, c, "DROP FUNCTION fe_slice_bound()")
					// Admission of the maximum is distinct from its reached rank
					// error; unreachable source expressions remain unevaluated.
					s = fixture.Exec("CREATE FUNCTION fe_slice_bound() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer[]; BEGIN IF false THEN "+loop+" END IF; RETURN 42; END$$", 0)
					s.Prepared = preparedDDL
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					plpgsqlValue(t, p, b, c, q, "INT4", int64(42))
					foreachHeld(t, p, b, c, q, "INT4", [][]any{nil, nil}, []any{int64(42), int64(42)})
					userOperatorExec(t, p, b, c, "DROP FUNCTION fe_slice_bound()")
				}
			})
		})
	}
	for _, token := range []string{"-1", "1.0", "1e0", "2147483648", "2_147_483_648", "0x8000_0000", "0o20_000_000_000", "0b1000_0000_0000_0000_0000_0000_0000_0000"} {
		t.Run("invalid/"+token, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "CREATE FUNCTION fe_slice_bad() RETURNS integer LANGUAGE plpgsql AS $$\n-- é literal admission\nDECLARE x integer[];\nBEGIN\nIF false THEN FOREACH x SLICE " + token + " IN ARRAY '{{1,2},{3,4}}'::integer[] LOOP END LOOP; END IF;\nRETURN 0; END$$"
				badToken := token
				if token == "-1" {
					badToken = "-"
				}
				want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+badToken+`"`)
				want.Position = "151"
				plpgsqlError(t, p, c, q, false, want)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='fe_slice_bad'", "INT8", int64(0))
				userOperatorExec(t, p, b, c, "CREATE FUNCTION fe_slice_bad() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer[]; n integer:=0; BEGIN FOREACH x SLICE 1 IN ARRAY '{{1,2},{3,4}}'::integer[] LOOP n:=n+1; END LOOP; RETURN n; END$$")
				plpgsqlValue(t, p, b, c, "SELECT fe_slice_bad() AS v", "INT4", int64(2))
			})
		})
	}
}
