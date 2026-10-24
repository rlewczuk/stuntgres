package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Declaration-sensitive integer-FOR list admission, independently specified from
// REL_18_6 for_variable/read_into_scalar_list and observed on the pinned oracle.
func TestReferencePLpgSQLIntegerForTargetAdmission(t *testing.T) {
	for _, tc := range []struct{ name, declaration, targets, code, message, token string }{
		{"unknown_first", "", "a,b", "42601", `"a" is not a known variable`, "a,b"},
		{"unknown_second", "a integer;", "a,b", "42601", `"b" is not a known variable`, "b IN"},
		{"constant_first", "a CONSTANT integer:=1; b integer;", "a,b", "22005", `variable "a" is declared CONSTANT`, "a,b"},
		{"constant_second", "a integer; b CONSTANT integer:=1;", "a,b", "22005", `variable "b" is declared CONSTANT`, "b IN"},
		{"record_second", "a integer; r record;", "a,r", "42601", `"r" is not a scalar variable`, "r IN"},
		{"record_first", "r record; b integer;", "r,b", "42601", `syntax error at or near ","`, ",b"},
		{"array_member", "a integer; b integer[];", "a,b", "42601", "integer FOR loop must have only one target variable", "a,b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					declaration := ""
					if tc.declaration != "" {
						declaration = "DECLARE " + tc.declaration + "\n"
					}
					body := "\n-- é target admission coordinates\n" + declaration + "BEGIN\n FOR " + tc.targets + " IN 1..3 LOOP END LOOP;\nEND"
					for _, prefix := range []string{"CREATE FUNCTION if_target_bad() RETURNS void LANGUAGE plpgsql AS $$", "DO $$"} {
						q := prefix + body + "$$"
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						at := strings.Index(q, tc.token)
						if at < 0 {
							t.Fatal("missing diagnostic token", tc.token)
						}
						want.Position = fixture.Position(utf8.RuneCountInString(q[:at]) + 1)
						plpgsqlError(t, p, c, q, false, want)
					}
					plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='if_target_bad'", "INT8", int64(0))
					userOperatorExec(t, p, b, c, "CREATE FUNCTION if_target_bad() RETURNS integer LANGUAGE plpgsql AS $$DECLARE s integer:=0; BEGIN FOR i IN 1..3 LOOP s:=s+i; END LOOP; RETURN s; END$$")
					plpgsqlValue(t, p, b, c, "SELECT if_target_bad() AS v", "INT4", int64(6))
				})
			}
		})
	}
}
