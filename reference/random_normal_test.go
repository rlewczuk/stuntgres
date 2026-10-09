package reference_test

import (
	"math"
	"testing"
)

func TestReferenceRandomSeedEndpoints(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, e := b.db.Conn(p.ctx)
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
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, e := b.db.Conn(p.ctx)
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
		step := referenceSelect(tc.q, []string{tc.name}, []string{tc.typ}, [][]any{{nil}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
func TestReferenceRandomVoidStorageBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			if b.pg {
				step := referenceImplicitError("CREATE TABLE bad(v void)", referenceDiagnostic{Code: "42P16", Message: `column "v" has pseudo-type void`})
				step.errorStage = "execute"
				referenceRunStep(t, p.ctx, b.db, b.recorder, step)
			} else {
				if _, e := b.db.ExecContext(p.ctx, "CREATE TABLE bad(v void)"); e == nil {
					t.Fatal("VOID storage admitted")
				}
			}
		})
	}
}
