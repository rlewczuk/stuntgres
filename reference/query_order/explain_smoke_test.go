package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExplainFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE explain_t(id integer,v text)", 0))
			run(fixture.Exec("INSERT INTO explain_t VALUES(1,'one'),(2,'two'),(3,'three')", 3))
			run(fixture.Exec("CREATE SEQUENCE explain_smoke_s START 1", 0))
			run(fixture.Exec("CREATE TABLE explain_smoke_d(id integer DEFAULT nextval('explain_smoke_s'))", 0))
			for _, prepared := range []bool{false, true} {
				explainScanStructure(t, b, explainQuery(t, p, b, "EXPLAIN SELECT id FROM explain_t WHERE id=2 ORDER BY id DESC", prepared))
				explainCosts(t, b, explainQuery(t, p, b, "EXPLAIN (COSTS OFF) SELECT id FROM explain_t WHERE id=2 ORDER BY id DESC", prepared), false)
				explainQuery(t, p, b, "EXPLAIN INSERT INTO explain_smoke_d DEFAULT VALUES", prepared)
				explainQuery(t, p, b, "EXPLAIN WITH changed AS(INSERT INTO explain_smoke_d VALUES(99) RETURNING id) SELECT * FROM changed", prepared)
				run(fixture.Select("SELECT id FROM explain_smoke_d ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
			}
			run(fixture.Select("SELECT nextval('explain_smoke_s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT id,v FROM explain_t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
		})
	}
}
