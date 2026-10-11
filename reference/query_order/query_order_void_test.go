package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderVoidRegression(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE order_void_t(id INT)", 0))
	p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
	for _, population := range []string{"empty", "one", "many"} {
		if population == "one" {
			p.Step(t, fixture.Exec("INSERT INTO order_void_t VALUES(1)", 1))
		}
		if population == "many" {
			p.Step(t, fixture.Exec("INSERT INTO order_void_t VALUES(2),(3)", 2))
		}
		for _, tc := range []struct{ q, position string }{
			{"SELECT id FROM order_void_t ORDER BY setseed(0)", "38"},
			{"SELECT setseed(0) AS v FROM order_void_t ORDER BY v", "51"},
			{"SELECT setseed(0) FROM order_void_t ORDER BY 1", "46"},
			{"SELECT nextval('s') FROM order_void_t ORDER BY setseed(0)", "48"},
		} {
			t.Run(population+"/"+tc.q, func(t *testing.T) {
				p.Step(t, fixture.Exec("ALTER SEQUENCE s RESTART WITH 1", 0))
				step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42883", Message: "could not identify an ordering operator for type void", Hint: "Use an explicit ordering operator or modify the query.", Position: tc.position})
				step.SelectRows = true
				step.ErrorStage = "execute"
				p.Step(t, step)
				step.ErrorStage = "prepare"
				p.Step(t, fixture.Prepared(step))
				p.Step(t, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
			})
		}
	}
}
