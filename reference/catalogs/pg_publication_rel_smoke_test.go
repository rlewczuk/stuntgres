package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGPublicationRelFeatureSmoke(t *testing.T) {
	publicationScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
		c:=publicationConn(t,p,b)
		run:=func(s ...fixture.Step){publicationRun(t,p,b,c,s...)}
		publicationRelPayloadSetup(t,p,b,c)
		before:=publicationRelRead(t,p,b,c,publicationRelRaw,1,false)
		if before[0][3]==nil || before[0][4]!="1 3" { t.Fatalf("missing real filter/vector: %v",before) }
		state:=publicationRelState([]any{"publication_rt_p","publication_rt_t","1 3","(v > 0)"})
		run(state,fixture.Prepared(state),publicationRelSentinels(),fixture.Exec("BEGIN",0),fixture.Exec("ALTER PUBLICATION publication_rt_p DROP TABLE publication_rt_t",0),publicationRelState(),fixture.Exec("ROLLBACK",0),state)
		publicationRelSame(t,publicationRelRead(t,p,b,c,publicationRelRaw,1,true),before)
		run(publicationDiagnostic("ALTER PUBLICATION publication_rt_p ADD TABLE publication_rt_t","42710",`relation "publication_rt_t" is already member of publication "publication_rt_p"`,"",""),state,publicationRelSentinels())
		publicationRelSame(t,publicationRelRead(t,p,b,c,publicationRelRaw,1,false),before)
		publicationRelRoundtrip(t,p,b)
		run(state,publicationRelSentinels())
	})
}
