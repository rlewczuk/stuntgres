package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceACLItemMetadata(t *testing.T) {
	aclitemScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.acl_metadata (id INT,a aclitem NOT NULL,aa aclitem[],n aclitem)", 0))
		run(fixture.Exec("INSERT INTO public.acl_metadata VALUES (1,'=r*/postgres','[0:1]={=r*/postgres,NULL}',NULL)", 1))
		for _, tc := range []struct {
			where string
			rows  [][]any
		}{
			{"", [][]any{{"=r*/postgres", "[0:1]={=r*/postgres,NULL}", nil}}},
			{" WHERE id=99", [][]any{}},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT a AS scalar_acl,aa AS array_acl,n AS null_acl FROM public.acl_metadata"+tc.where, []string{"scalar_acl", "array_acl", "null_acl"}, []string{"ACLITEM", "_ACLITEM", "ACLITEM"}, tc.rows)
				s.Prepared = prepared
				var retained []*sql.ColumnType
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					retained = aclitemMetadata(t, b.PG, rows, s.Columns, s.Types, []bool{false, true, true}, false)
				}
				run(s)
				fixture.ScalarAssertDescription(t, retained, s.Columns, s.Types)
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT NULL::aclitem AS a,NULL::aclitem[] AS aa", []string{"a", "aa"}, []string{"ACLITEM", "_ACLITEM"}, [][]any{{nil, nil}})
			s.Prepared = prepared
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { aclitemMetadata(t, b.PG, rows, s.Columns, s.Types, nil, true) }
			run(s)
		}
	})
}
