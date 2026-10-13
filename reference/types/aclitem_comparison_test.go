package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceACLItemComparison(t *testing.T) {
	aclitemScenario(t, []string{"acl_equal_a", "acl_equal_b"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cases := []struct {
			left, right string
			want        any
		}{
			{"acl_equal_a=wr**/acl_equal_b", "acl_equal_a=r*w/acl_equal_b", true},
			{"acl_equal_a=r*w/acl_equal_b", "acl_equal_a=rw*/acl_equal_b", false},
			{"acl_equal_a=r/acl_equal_b", "acl_equal_b=r/acl_equal_b", false},
			{"acl_equal_a=r/acl_equal_b", "acl_equal_a=r/acl_equal_a", false},
			{"acl_equal_a=r/acl_equal_b", "acl_equal_a=w/acl_equal_b", false},
			{"acl_equal_a=r/acl_equal_b", "acl_equal_a=r*/acl_equal_b", false},
			{"=r*/postgres", "=r*/postgres", true},
			{"=r/postgres", "postgres=r/postgres", false},
			{"postgres=/postgres", "postgres=*/postgres", true},
			{"postgres=/postgres", "postgres=r/postgres", false},
		}
		for _, letter := range "arwdDxtXUCTcsAm" {
			base := "acl_equal_a=" + string(letter) + "/acl_equal_b"
			cases = append(cases, struct {
				left, right string
				want        any
			}{base, base, true}, struct {
				left, right string
				want        any
			}{base, "acl_equal_a=/acl_equal_b", false}, struct {
				left, right string
				want        any
			}{base, "acl_equal_a=" + string(letter) + "*/acl_equal_b", false})
		}
		for _, prepared := range []bool{false, true} {
			for _, tc := range cases {
				s := fixture.Select("SELECT $1::aclitem = $2::aclitem AS equal_acl", []string{"equal_acl"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.left, tc.right)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			for _, pair := range [][2]any{{nil, nil}, {nil, "=r/postgres"}, {"=r/postgres", nil}} {
				s := fixture.Select("SELECT $1::aclitem = $2::aclitem AS equal_acl", []string{"equal_acl"}, []string{"BOOL"}, [][]any{{nil}}, pair[0], pair[1])
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		arrays := []struct {
			left, right any
			equal       any
		}{
			{"{acl_equal_a=wr**/acl_equal_b,NULL}", "{acl_equal_a=r*w/acl_equal_b,NULL}", true},
			{"{acl_equal_a=r*w/acl_equal_b,NULL}", "{acl_equal_a=rw*/acl_equal_b,NULL}", false},
			{"{=r/postgres,postgres=/postgres}", "{postgres=/postgres,=r/postgres}", false},
			{"[0:0]={=r/postgres}", "{=r/postgres}", false},
			{"{{=r/postgres,=r/postgres}}", "{{=r/postgres},{=r/postgres}}", false},
			{"{NULL}", "{NULL}", true}, {"{NULL}", "{=r/postgres}", false},
			{"{}", "{}", true}, {nil, nil, nil}, {nil, "{}", nil}, {"{}", nil, nil},
		}
		for _, prepared := range []bool{false, true} {
			for _, tc := range arrays {
				for _, op := range []string{"=", "<>", "!="} {
					want := tc.equal
					if equal, ok := want.(bool); ok && op != "=" {
						want = !equal
					}
					s := fixture.Select("SELECT $1::aclitem[] "+op+" $2::aclitem[] AS equal_acl", []string{"equal_acl"}, []string{"BOOL"}, [][]any{{want}}, tc.left, tc.right)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			}
		}
	})
}
