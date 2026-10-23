package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCreateAggregatePolymorphicSupport(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			createAggregateRun(t, p, b, c,
				fixture.Exec("CREATE AGGREGATE ca_collect(integer) (SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}')", 0),
				fixture.Exec("CREATE AGGREGATE ca_collect_unseeded(integer) (SFUNC=pg_catalog.array_append,STYPE=integer[])", 0))
			for _, tc := range []struct{ q, want string }{
				{"SELECT ca_collect(x ORDER BY id)::text AS v FROM(VALUES(1,1),(2,NULL::int),(3,3)) s(id,x)", "{1,NULL,3}"},
				{"SELECT ca_collect(x)::text AS v FROM(VALUES(1)) s(x) WHERE FALSE", "{}"},
				{"SELECT ca_collect_unseeded(x ORDER BY id)::text AS v FROM(VALUES(1,NULL::int),(2,2)) s(id,x)", "{NULL,2}"},
				{"SELECT ca_collect_unseeded(x)::text AS v FROM(VALUES(NULL::int),(NULL::int)) s(x)", "{NULL,NULL}"},
			} {
				createAggregateBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}))
			}
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT ca_collect_unseeded(x)::text AS v FROM(VALUES(1)) s(x) WHERE FALSE", []string{"v"}, []string{"TEXT"}, [][]any{{nil}}))
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT a.aggtransfn=378::oid AS support,a.aggtranstype=1007::oid AS state_type,p.prorettype=1007::oid AS result_type,a.agginitval FROM pg_proc p JOIN pg_aggregate a ON a.aggfnoid=p.oid WHERE p.proname='ca_collect'", []string{"support", "state_type", "result_type", "agginitval"}, []string{"BOOL", "BOOL", "BOOL", "TEXT"}, [][]any{{true, true, true, "{}"}}))
		})
	}
}
