package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayTransformACLMutation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			// Match the existing object-ACL fixture contract, not observed output:
			// native authenticates as postgres; the pinned oracle as stuntgres.
			owner, want := "postgres", "=r/postgres"
			if b.PG {
				owner, want = "stuntgres", "=r/stuntgres"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_user AS actor", []string{"actor"}, []string{"NAME"}, [][]any{{owner}}))
			input := "{=r/" + owner + "}"
			for _, tc := range []struct {
				name, expression string
				args             []any
			}{
				{"literal", "array_to_string('" + input + "'::aclitem[], ',')", nil},
				{"parameter", "array_to_string($1::aclitem[], ',')", []any{input}},
			} {
				for _, prepared := range []bool{false, true} {
					route := "direct"
					if prepared {
						route = "prepared"
					}
					t.Run(tc.name+"_"+route, func(t *testing.T) {
						table := "at_acl_string_" + tc.name + "_" + route
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE "+table+"(s TEXT)", 0))
						// SELECT authority is a positive control, not a substitute for
						// the distinct INSERT VALUES evaluation environment below.
						selectStep := arrayTransformSelect(b, "SELECT "+tc.expression+" AS s", []string{"s"}, []string{"TEXT"}, [][]any{{want}}, tc.args...)
						selectStep.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, selectStep)
						insert := fixture.Exec("INSERT INTO "+table+" VALUES ("+tc.expression+")", 1, tc.args...)
						insert.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, insert)
						// Fixed TEXT proves scalar ACL spelling was stored, with no
						// braces or normalization concealing the backend grantor.
						arrayTransformBoth(t, p, b, c, arrayTransformSelect(b, "SELECT s FROM "+table, []string{"s"}, []string{"TEXT"}, [][]any{{want}}))
					})
				}
			}
		})
	}
}
