package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryPositionFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			s := fixture.Select("SELECT box '(1,1),(0,0)' << box '(2,2),(1,1)' AS strict,box '(1,1),(0,0)' <^ box '(2,2),(1,1)' AS touching,polygon '((0,0))' << polygon '((0.0000005,0))' AS raw,point '(0,0)' << point '(0.0000005,0)' AS fuzzy", []string{"strict", "touching", "raw", "fuzzy"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true, false}})
			run(s)
			run(fixture.Prepared(s))
			s = fixture.Select("SELECT box '(1,1),(0,0)' <<| box '(2,2),(1,1)' AS strict,box '(1,1),(0,0)' <^ box '(2,2),(1,1)' AS touching,point '(1,1)' <^ point '(1,1)' AS point_alias,polygon '((0,0))' <<| polygon '((0,0.0000005))' AS raw,point '(0,0)' <<| point '(0,0.0000005)' AS fuzzy,circle '<(0,0),1>' <<| circle '<(0,2),1>' AS tangent", []string{"strict", "touching", "point_alias", "raw", "fuzzy", "tangent"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true, false, false}})
			run(s)
			run(fixture.Prepared(s))
			geometryRelationWitness(t, p, backend, c, geometryRelationCase{"<<", "box", "box", "(1,1),(0,0)", "(4,4),(3,3)", "BOOL", true})
			geometryRelationWitness(t, p, backend, c, geometryRelationCase{"<<|", "polygon", "polygon", "((0,0))", "((3,3))", "BOOL", true})
			for _, prepared := range []bool{false, true} {
				run(boxError("SELECT NULL::point &< NULL::point", "42883", "operator does not exist: point &< point", "20", geometryPositionHint, prepared, false))
				run(boxError("SELECT NULL::point &<| NULL::point", "42883", "operator does not exist: point &<| point", "20", geometryPositionHint, prepared, false))
			}
			geometryPositionConsumerCycle(t, p, backend, c)
		})
	}
}
