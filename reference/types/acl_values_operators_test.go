package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceACLValuesOperators(t *testing.T) {
	aclValuesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, expr string
			want       any
		}{
			{"same", "makeaclitem(10,10,'SELECT,UPDATE',true)=makeaclitem(10,10,'UPDATE,SELECT,SELECT',true)", true},
			{"grantee", "makeaclitem(0,10,'SELECT',false)=makeaclitem(10,10,'SELECT',false)", false},
			{"grantor", "makeaclitem(10,0,'SELECT',false)=makeaclitem(10,10,'SELECT',false)", false},
			{"rights", "makeaclitem(10,10,'SELECT',false)=makeaclitem(10,10,'UPDATE',false)", false},
			{"options", "makeaclitem(10,10,'SELECT',false)=makeaclitem(10,10,'SELECT',true)", false},
			{"superset", "ARRAY[makeaclitem(10,10,'SELECT,UPDATE',true)] @> makeaclitem(10,10,'SELECT',true)", true},
			{"no_union", "ARRAY[makeaclitem(10,10,'SELECT',false),makeaclitem(10,10,'UPDATE',false)] @> makeaclitem(10,10,'SELECT,UPDATE',false)", false},
			{"options_absent", "ARRAY[makeaclitem(10,10,'SELECT,UPDATE',false)] @> makeaclitem(10,10,'SELECT',true)", false},
			{"grantee_exact", "ARRAY[makeaclitem(10,10,'SELECT',true)] @> makeaclitem(0,10,'SELECT',false)", false},
			{"grantor_exact", "ARRAY[makeaclitem(10,10,'SELECT',true)] @> makeaclitem(10,0,'SELECT',false)", false},
			{"zero", "ARRAY[makeaclitem(0,0,'SELECT',true)] @> makeaclitem(0,0,'SELECT',false)", true},
			{"null_left", "NULL::aclitem[] @> makeaclitem(0,0,'SELECT',false)", nil},
			{"null_right", "ARRAY[makeaclitem(0,0,'SELECT',false)] @> NULL::aclitem", nil},
			{"null_equal", "makeaclitem(0,0,'SELECT',false)=NULL::aclitem", nil},
			{"empty", "acldefault('c',0) @> makeaclitem(0,0,'SELECT',false)", false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT "+tc.expr+" AS a", []string{"a"}, []string{"BOOL"}, [][]any{{tc.want}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		owner := aclValuesOwner(b.PG)
		for _, tc := range []struct {
			left, right string
			want        bool
		}{
			{"{" + owner + "=r*w/" + owner + "}", owner + "=r*/" + owner, true},
			{"{" + owner + "=r*w/" + owner + "}", owner + "=rw*/" + owner, false},
			{"{" + owner + "=r/" + owner + "}", owner + "=/" + owner, true},
			{"{=" + "r/" + owner + "}", owner + "=/" + owner, false},
		} {
			s := fixture.Select("SELECT $1::aclitem[] @> $2::aclitem AS a", []string{"a"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.left, tc.right)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
	})
}
