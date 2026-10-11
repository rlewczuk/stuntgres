package reference_test

import (
	"database/sql"
	"sort"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryDistinctVolatileOutputOrder(t *testing.T) {
	fixture.QueryDistinctScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE random_keys(id INT)", 0))
		run(fixture.Exec("INSERT INTO random_keys VALUES(1),(2),(3),(4)", 4))
		seed := func(t *testing.T) {
			t.Helper()
			var value any
			if err := c.QueryRowContext(p.Ctx, "SELECT setseed(.25)").Scan(&value); err != nil {
				t.Fatal(err)
			}
		}
		draw := func(t *testing.T) float64 {
			t.Helper()
			var value float64
			if err := c.QueryRowContext(p.Ctx, "SELECT random()").Scan(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		// Each backend has its own random provider. Independently replay eight
		// scalar draws to specify four two-occurrence tuples and draw nine;
		// neither backend's output is used as the other backend's golden.
		seed(t)
		pairs := make([][]any, 4)
		for i := range pairs {
			pairs[i] = []any{draw(t), draw(t)}
		}
		continuation := draw(t)
		want := append([][]any(nil), pairs...)
		sort.Slice(want, func(i, j int) bool { return want[i][1].(float64) < want[j][1].(float64) })
		byFirst := append([][]any(nil), pairs...)
		sort.Slice(byFirst, func(i, j int) bool { return byFirst[i][0].(float64) < byFirst[j][0].(float64) })
		sameOrder := true
		for i := range want {
			sameOrder = sameOrder && byFirst[i][0] == want[i][0]
		}
		if sameOrder {
			t.Fatal("seeded witness must distinguish first and second output ordering")
		}
		for _, suffix := range []string{"y", "2"} {
			q := "SELECT DISTINCT random() AS x,random() AS y FROM random_keys ORDER BY " + suffix
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				for _, route := range []string{"direct", "fresh prepared", "held", "held repeat"} {
					t.Run(suffix+"/"+route, func(t *testing.T) {
						seed(t)
						s := fixture.Select(q, []string{"x", "y"}, []string{"FLOAT8", "FLOAT8"}, want)
						switch route {
						case "direct":
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						case "fresh prepared":
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
						default:
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, b.Recorder, s)
						}
						if next := draw(t); next != continuation {
							t.Fatal("ORDER reevaluated a visible random occurrence", next, continuation)
						}
					})
				}
			}()
		}
		// CREATE VIEW freezes the second alias as an output ordinal. The inner
		// explicit ORDER must survive its persistent producer, not be masked by
		// a new outer ORDER clause.
		seed(t)
		run(fixture.Exec("CREATE VIEW random_dv AS SELECT DISTINCT random() AS x,random() AS y FROM random_keys ORDER BY y", 0))
		if next := draw(t); next != pairs[0][0].(float64) {
			t.Fatal("view analysis evaluated projection", next, pairs[0][0])
		}
		q := "SELECT x,y FROM random_dv"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, held := range []bool{false, true, true} {
			seed(t)
			s := fixture.Select(q, []string{"x", "y"}, []string{"FLOAT8", "FLOAT8"}, want)
			if held {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, q}, b.Recorder, s)
			} else {
				fixture.QueryDistinctBoth(func(s fixture.Step) {
					seed(t)
					run(s)
					if next := draw(t); next != continuation {
						t.Fatal("persistent ORDER added a draw", next, continuation)
					}
				}, s)
				continue
			}
			if next := draw(t); next != continuation {
				t.Fatal("persistent ORDER added a draw", next, continuation)
			}
		}
	})
}
