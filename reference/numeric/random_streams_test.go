package reference_test

import (
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomStreams(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			for _, numeric := range []string{"SELECT random(0.000,0.999)", "SELECT random(100000000000000000000.000,100000000000000000000.002)"} {
				series := func() []any {
					return []any{randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8"), randomScalar(t, p, c, b, "SELECT random(-7,3)", "random", "INT4"), randomScalar(t, p, c, b, "SELECT random(2147483648,2147483650)", "random", "INT8"), randomScalar(t, p, c, b, numeric, "random", "NUMERIC"), randomScalar(t, p, c, b, "SELECT random_normal()", "random_normal", "FLOAT8"), randomScalar(t, p, c, b, "SELECT random_normal(5)", "random_normal", "FLOAT8"), randomScalar(t, p, c, b, "SELECT random_normal(5,2)", "random_normal", "FLOAT8")}
				}
				randomReseed(t, p, c, b, .25)
				a := series()
				x := a[0].(float64)
				if math.IsNaN(x) || x < 0 || x >= 1 {
					t.Fatal(a)
				}
				n := a[1].(int64)
				if n < -7 || n > 3 {
					t.Fatal(a)
				}
				n = a[2].(int64)
				if n < 2147483648 || n > 2147483650 {
					t.Fatal(a)
				}
				randomReseed(t, p, c, b, .25)
				if !reflect.DeepEqual(a, series()) {
					t.Fatal("mixed reseed")
				}
			}
			for _, pair := range [][2]string{{"SELECT random_normal()", "SELECT random_normal(0,1)"}, {"SELECT random_normal(5)", "SELECT random_normal(5,1)"}} {
				randomReseed(t, p, c, b, .25)
				a := randomScalar(t, p, c, b, pair[0], "random_normal", "FLOAT8")
				randomReseed(t, p, c, b, .25)
				if randomScalar(t, p, c, b, pair[1], "random_normal", "FLOAT8") != a {
					t.Fatal("normal defaults")
				}
			}
		})
	}
}
func TestReferenceRandomStrictNullDemand(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE null_inputs(id INT,mean DOUBLE PRECISION)", 0))
	p.Step(t, fixture.Exec("INSERT INTO null_inputs VALUES(1,NULL),(2,1)", 2))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			randomReseed(t, p, c, b, .25)
			first := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			second := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			for _, q := range []string{"SELECT random_normal(NULL,random())", "SELECT random_normal(NULL::int4,random())"} {
				randomReseed(t, p, c, b, .25)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(q, []string{"random_normal"}, []string{"FLOAT8"}, [][]any{{nil}}))
				if randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") != first {
					t.Fatal("literal NULL sibling")
				}
			}
			randomReseed(t, p, c, b, .25)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT random_normal(mean,random()) FROM null_inputs WHERE id=1", []string{"random_normal"}, []string{"FLOAT8"}, [][]any{{nil}}))
			if randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") != second {
				t.Fatal("direct row NULL sibling")
			}
			q := "SELECT random_normal(mean,random()) FROM null_inputs WHERE id=$1"
			stmt, e := c.PrepareContext(p.Ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, prepared := range []bool{false, true} {
				for range 2 {
					randomReseed(t, p, c, b, .25)
					var executor fixture.Executor = c
					if prepared {
						executor = held
					}
					fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.Select(q, []string{"random_normal"}, []string{"FLOAT8"}, [][]any{{nil}}, 1))
					if randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") != second {
						t.Fatal("runtime NULL sibling")
					}
				}
			}
		})
	}
}
