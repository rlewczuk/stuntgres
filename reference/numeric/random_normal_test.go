package reference_test

import (
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomSeedEndpoints(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			for _, seed := range []float64{-1, 1, 0, math.Copysign(0, -1)} {
				randomReseed(t, p, c, b, seed)
				a := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
				randomReseed(t, p, c, b, seed)
				if randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") != a {
					t.Fatal("seed endpoint")
				}
			}
			randomReseed(t, p, c, b, 0)
			zero := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			randomReseed(t, p, c, b, math.Copysign(0, -1))
			if randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") != zero {
				t.Fatal("signed zero")
			}
		})
	}
}
func TestReferenceRandomNormalSpecials(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			randomReseed(t, p, c, b, .25)
			positive := randomScalar(t, p, c, b, "SELECT random_normal(0,2)", "random_normal", "FLOAT8").(float64)
			randomReseed(t, p, c, b, .25)
			if randomScalar(t, p, c, b, "SELECT random_normal(0,-2)", "random_normal", "FLOAT8").(float64) != -positive {
				t.Fatal("negative deviation")
			}
			for _, q := range []string{"SELECT random_normal('NaN',1)", "SELECT random_normal(0,'NaN')"} {
				if !math.IsNaN(randomScalar(t, p, c, b, q, "random_normal", "FLOAT8").(float64)) {
					t.Fatal(q)
				}
			}
			for _, tc := range []struct {
				q    string
				sign int
			}{{"SELECT random_normal('Infinity',1)", 1}, {"SELECT random_normal('-Infinity',1)", -1}} {
				if !math.IsInf(randomScalar(t, p, c, b, tc.q, "random_normal", "FLOAT8").(float64), tc.sign) {
					t.Fatal(tc.q)
				}
			}
		})
	}
	for _, tc := range []struct{ q, name, typ string }{{"SELECT random(NULL,1)", "random", "INT4"}, {"SELECT random(1,NULL)", "random", "INT4"}, {"SELECT random_normal(0,NULL)", "random_normal", "FLOAT8"}, {"SELECT random(COALESCE('NaN',1.0),NULL)", "random", "NUMERIC"}} {
		step := fixture.Select(tc.q, []string{tc.name}, []string{tc.typ}, [][]any{{nil}})
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
}
func TestReferenceRandomVoidStorageBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				step := fixture.ImplicitError("CREATE TABLE bad(v void)", fixture.Diagnostic{Code: "42P16", Message: `column "v" has pseudo-type void`})
				step.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
			} else {
				if _, e := b.DB.ExecContext(p.Ctx, "CREATE TABLE bad(v void)"); e == nil {
					t.Fatal("VOID storage admitted")
				}
			}
		})
	}
}
