package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLtreeComparisonFeatureSmoke(t *testing.T) {
	t.Run("operators_keys_errors",func(t *testing.T){
		ltreeComparisonScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
			ltreeInstall(t,p,b,c)
			ltreeComparisonTable(t,p,b,c)
			for _, s := range []fixture.Step{
				ltreeComparisonStep("SELECT "+ltreeComparisonProjection("'a.b'::ltree","'a-b'::ltree"),ltreeComparisonRow(-1)),
				ltreeComparisonStep("SELECT "+ltreeComparisonProjection("'a.b'::ltree","'a.b'::ltree"),ltreeComparisonRow(0)),
				ltreeComparisonIDs("SELECT id FROM lt_cmp ORDER BY v,id",4,3,2,6,1,5),
				ltreeComparisonIDs("SELECT id FROM lt_cmp ORDER BY v DESC,id",5,1,2,6,3,4),
				ltreeComparisonGroupStep(b),
				fixture.Exec("CREATE INDEX lt_cmp_btree ON lt_cmp(v)",0),
				fixture.Exec("CREATE INDEX lt_cmp_hash ON lt_cmp USING hash(v)",0),
				ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v='a.b'::ltree ORDER BY id",2,6),
				ltreeComparisonIDs("SELECT id FROM lt_cmp WHERE v<'a-b'::ltree ORDER BY id",2,3,4,6),
			} { ltreeComparisonRun(t,p,b,c,s) }
			ltreeComparisonUniqueChecks(t,p,b,c)
			q := "SELECT $1::ltree = $2::ltree AS eq"
			stmt,err := c.PrepareContext(p.Ctx,q)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
			want := fixture.ControlsReferenceDiagnostic("42601","ltree syntax error at character 3")
			want.Context = "unnamed portal parameter $1 = '...'"
			s := fixture.ErrorDiagnostic(q,want,"a..b","a")
			s.SelectRows,s.ErrorStage = true,"execute"
			ltreeComparisonRun(t,p,b,held,s)
			ltreeComparisonRun(t,p,b,held,fixture.Select(q,[]string{"eq"},[]string{"BOOL"},[][]any{{true}},"a.b",[]byte("a.b")))
		})
	})
	t.Run("stored_lifetime",func(t *testing.T){ ltreeComparisonLifecycle(t,"same") })
}
