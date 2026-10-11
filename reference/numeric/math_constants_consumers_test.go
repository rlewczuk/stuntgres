package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathConstantsConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, referenceArithmeticExecutor{backend.DB, t}, backend.Recorder, s)
		}
		run(fixture.Exec("CREATE TABLE constants_source(id INT,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO constants_source VALUES(1,1),(2,0),(3,NULL)", 3))
		for _, name := range referenceMathConstantsUnaryNames {
			input, want := referenceMathConstantsParameterBaseline(name)
			q := "SELECT " + name + "(d) AS result FROM constants_source WHERE d="
			if input == 1 {
				q += "1"
			} else {
				q += "0"
			}
			referenceMathTrigStep(t, p, backend, fixture.Select(q, []string{"result"}, []string{"FLOAT8"}, [][]any{{want}}))
		}
		// Selected calls are projection roots, not WHERE target_or leaves.
		// Filter/order their computed derived column without broadening grammar.
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT id,c FROM (SELECT id,cbrt(d) AS c FROM constants_source) AS computed WHERE c IS NOT NULL ORDER BY c", []string{"id", "c"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(2), float64(0)}, {int64(1), float64(1)}}))
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT pi() AS p,count(*) AS n FROM constants_source GROUP BY pi()", []string{"p", "n"}, []string{"FLOAT8", "INT8"}, [][]any{{3.141592653589793, int64(3)}}))
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT cbrt(d) AS c,count(*) AS n FROM constants_source GROUP BY cbrt(d) ORDER BY c NULLS LAST", []string{"c", "n"}, []string{"FLOAT8", "INT8"}, [][]any{{float64(0), int64(1)}, {float64(1), int64(1)}, {nil, int64(1)}}))
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT pi() AS p FROM constants_source WHERE FALSE", []string{"p"}, []string{"FLOAT8"}, nil))
		// Parameter-free immutable pi is preserved through genuine stored consumers.
		run(fixture.Exec("CREATE TABLE constants_generated(id INT,d DOUBLE PRECISION,p DOUBLE PRECISION GENERATED ALWAYS AS (pi()) STORED,c DOUBLE PRECISION GENERATED ALWAYS AS (cbrt(d)) STORED,CONSTRAINT constants_positive CHECK (pi()>0))", 0))
		run(fixture.Exec("CREATE INDEX constants_pi_index ON constants_generated(pi())", 0))
		run(fixture.Exec("INSERT INTO constants_generated(id,d) VALUES(1,1),(2,NULL)", 2))
		run(fixture.Exec("CREATE VIEW constants_consumer_view AS SELECT id,p,c,pi() AS literal FROM constants_generated", 0))
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT id,p,c,literal FROM constants_consumer_view ORDER BY id", []string{"id", "p", "c", "literal"}, []string{"INT4", "FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{int64(1), 3.141592653589793, float64(1), 3.141592653589793}, {int64(2), 3.141592653589793, nil, 3.141592653589793}}))
		// Runtime conditional demand: failing row is not reached in the CASE arm.
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT CASE WHEN d=0 THEN pi() ELSE gamma(d) END AS result FROM constants_source WHERE id=2", []string{"result"}, []string{"FLOAT8"}, [][]any{{3.141592653589793}}))
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT gamma(d) AS result FROM constants_source WHERE id=3", []string{"result"}, []string{"FLOAT8"}, [][]any{{nil}}))
		run(fixture.Exec("CREATE TABLE constants_target(id INT,d DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO constants_target VALUES(99,7)", 1))
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"INSERT INTO constants_target SELECT id,gamma(d) FROM constants_source ORDER BY id", "UPDATE constants_source SET d=gamma(d)"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "value out of range: overflow"})
				s.ErrorStage = "execute"
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				run(fixture.Select("SELECT id,d FROM constants_target ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(99), float64(7)}}))
				run(fixture.Select("SELECT id,d FROM constants_source ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(1)}, {int64(2), float64(0)}, {int64(3), nil}}))
			}
		}
		run(fixture.Exec("UPDATE constants_source SET d=1 WHERE id=2", 1))
		run(fixture.Prepared(fixture.Exec("INSERT INTO constants_target SELECT id,gamma(d) FROM constants_source ORDER BY id", 3)))
		run(fixture.Select("SELECT id,d FROM constants_target ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(1)}, {int64(2), float64(1)}, {int64(3), nil}, {int64(99), float64(7)}}))
	})
}
