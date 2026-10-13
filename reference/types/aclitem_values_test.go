package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceACLItemValues(t *testing.T) {
	roles := []string{"acl_values_a", "acl_values_b", "ACL_VALUES_CASE", "acl_values_case", "user", "group", "1234567", "acl value space", `acl"value`, `acl\value`, "acl_é", strings.Repeat("n", 63), strings.Repeat("é", 31)}
	aclitemScenario(t, roles, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		cases := []struct{ input, output string }{
			{"postgres=wrraa**/postgres", "postgres=a*rw/postgres"},
			{"postgres=*r**/postgres", "postgres=r*/postgres"},
			{"=r*/postgres", "=r*/postgres"},
			{"postgres=/postgres", "postgres=/postgres"},
			{"=/postgres", "=/postgres"},
			{`""=r/acl_values_b`, "=r/acl_values_b"},
			{"acl_values_a=rw*/acl_values_b", "acl_values_a=rw*/acl_values_b"},
			{"user acl_values_a=rw/acl_values_b", "acl_values_a=rw/acl_values_b"},
			{"group acl_values_a=r/acl_values_b", "acl_values_a=r/acl_values_b"},
			{"user=r/group", "user=r/group"},
			{"group=r/user", "group=r/user"},
			{"1234567=r/postgres", "1234567=r/postgres"},
			{"ACL_VALUES_CASE=r/acl_values_case", "ACL_VALUES_CASE=r/acl_values_case"},
			{`"acl value space"=r/"acl""value"`, `"acl value space"=r/"acl""value"`},
			{`"acl\value"=r/"acl_é"`, `"acl\value"=r/"acl_é"`},
			{" \tacl_values_a \n=rw/ acl_values_b \r\n", "acl_values_a=rw/acl_values_b"},
			{`acl_"values"_a=r/acl_values_b`, "acl_values_a=r/acl_values_b"},
			{`postgres=r/"postgres`, "postgres=r/postgres"},
			{strings.Repeat("n", 63) + "=r/postgres", strings.Repeat("n", 63) + "=r/postgres"},
			{strings.Repeat("é", 31) + "=r/postgres", `"` + strings.Repeat("é", 31) + `"=r/postgres`},
			{"postgres=arwdDxtXUCTcsAm/postgres", "postgres=arwdDxtXUCTcsAm/postgres"},
			{"postgres=mAscTCUXtxDdwrraa/postgres", "postgres=arwdDxtXUCTcsAm/postgres"},
			{"postgres=a*r*w*d*D*x*t*X*U*C*T*c*s*A*m*/postgres", "postgres=a*r*w*d*D*x*t*X*U*C*T*c*s*A*m*/postgres"},
		}
		for _, letter := range "arwdDxtXUCTcsAm" {
			value := "postgres=" + string(letter) + "/postgres"
			cases = append(cases, struct{ input, output string }{value, value})
			value = "postgres=" + string(letter) + "*/postgres"
			cases = append(cases, struct{ input, output string }{value, value})
		}
		for i, tc := range cases {
			t.Run("scalar_"+fixture.Position(i+1), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := aclitemSelect("SELECT "+fixture.ArrayReferenceQuote(tc.input)+"::aclitem AS a", [][]any{{tc.output}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		run(fixture.Exec("SET quote_all_identifiers = on", 0))
		run(aclitemSelect("SELECT 'ACL_VALUES_CASE=r/user'::aclitem AS a", [][]any{{"ACL_VALUES_CASE=r/user"}}))
		run(fixture.Exec("SET quote_all_identifiers = off", 0))
		run(fixture.Exec("CREATE TABLE public.acl_roundtrip (id INT, a aclitem, aa aclitem[])", 0))
		arrayCases := []struct {
			input  any
			output any
		}{
			{nil, nil}, {"{}", "{}"}, {"{NULL}", "{NULL}"},
			{"{=r*/postgres,postgres=/postgres,NULL}", "{=r*/postgres,postgres=/postgres,NULL}"},
			{`[-1:0][3:4]={{acl_values_a=wr*/acl_values_b,NULL},{=r/postgres,postgres=/postgres}}`, `[-1:0][3:4]={{acl_values_a=r*w/acl_values_b,NULL},{=r/postgres,postgres=/postgres}}`},
			{aclitemArray(`"acl value space"=r/"acl""value"`, `"acl\value"=r/"acl_é"`), aclitemArray(`"acl value space"=r/"acl""value"`, `"acl\value"=r/"acl_é"`)},
		}
		want := make([][]any, 0, len(arrayCases))
		for i, tc := range arrayCases {
			s := fixture.Exec("INSERT INTO public.acl_roundtrip VALUES ($1, $2::aclitem, $3::aclitem[])", 1, i+1, "=r*/postgres", tc.input)
			s.Prepared = i%2 != 0
			run(s)
			want = append(want, []any{int64(i + 1), "=r*/postgres", tc.output})
		}
		run(fixture.Exec("INSERT INTO public.acl_roundtrip VALUES (7,NULL,NULL)", 1))
		want = append(want, []any{int64(7), nil, nil})
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT id,a,aa FROM public.acl_roundtrip ORDER BY id", []string{"id", "a", "aa"}, []string{"INT4", "ACLITEM", "_ACLITEM"}, want)
			s.Prepared = prepared
			run(s)
		}
	})
}
