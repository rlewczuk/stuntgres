package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomOrderedRowsAndAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE source(id INT,lo INT,hi INT)", 0), fixture.Exec("INSERT INTO source VALUES(1,-7,3),(2,10,20),(3,30,40)", 3), fixture.Exec("CREATE TABLE dest(v INT)", 0), fixture.Exec("INSERT INTO dest VALUES(99)", 1)} {
		p.Step(t, s)
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			q := "SELECT random(lo,hi) FROM source ORDER BY id"
			randomReseed(t, p, c, b, .25)
			values := randomRead(t, p.Ctx, c, b, q, []string{"random"}, []string{"INT4"}, nil)
			if len(values) != 3 {
				t.Fatal(values)
			}
			randomReseed(t, p, c, b, .25)
			for i, bounds := range [][2]int64{{-7, 3}, {10, 20}, {30, 40}} {
				n := values[i][0].(int64)
				if n < bounds[0] || n > bounds[1] {
					t.Fatal("row bounds", i, n)
				}
				if !b.PG {
					draw := randomScalar(t, p, c, b, "SELECT random(lo,hi) FROM source WHERE id=$1", "random", "INT4", i+1)
					if draw != n {
						t.Fatal("native ordered demand", i, draw, n)
					}
				}
			}
			randomReseed(t, p, c, b, .25)
			again := randomRead(t, p.Ctx, c, b, q, []string{"random"}, []string{"INT4"}, nil)
			if !reflect.DeepEqual(values, again) {
				t.Fatal("own ordered reseed")
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT random(count(*),count(*)) FROM source", []string{"random"}, []string{"INT8"}, [][]any{{int64(3)}}))
			randomReseed(t, p, c, b, .25)
			baseline := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			randomReseed(t, p, c, b, .25)
			prepared, e := c.PrepareContext(p.Ctx, "SELECT setseed(2) FROM source")
			if e != nil {
				t.Fatal("prepare invoked seed", e)
			}
			if e := prepared.Close(); e != nil {
				t.Fatal(e)
			}
			if randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8") != baseline {
				t.Fatal("prepare consumed draw")
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO source VALUES(4,2,1)", 1))
			var continuation any
			if !b.PG {
				randomReseed(t, p, c, b, .25)
				for i := range 3 {
					randomScalar(t, p, c, b, "SELECT random(lo,hi) FROM source WHERE id=$1", "random", "INT4", i+1)
				}
				continuation = randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			}
			for replay := range 2 {
				randomReseed(t, p, c, b, .25)
				step := fixture.ImplicitError("INSERT INTO dest SELECT random(lo,hi) FROM source ORDER BY id", fixture.Diagnostic{Code: "22023", Message: "lower bound must be less than or equal to upper bound"})
				step.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM dest", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
				next := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
				// P uses its own failed-execution replay: row/projection demand is not portable.
				if b.PG && replay == 0 {
					continuation = next
				} else if next != continuation {
					t.Fatal("failure retained/replayed consumption", next, continuation)
				}
			}
		})
	}
}
