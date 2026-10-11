package reference_test

import (
	"database/sql"
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomRangesAndMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			q := "SELECT random()"
			stmt, e := c.PrepareContext(p.Ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, executor := range []fixture.Executor{c, held} {
				x := randomScalar(t, p, executor, b, q, "random", "FLOAT8").(float64)
				if math.IsNaN(x) || x < 0 || x >= 1 {
					t.Fatal(x)
				}
			}
			for _, tc := range []struct{ q, typ string }{{"SELECT random(-2147483648,2147483647)", "INT4"}, {"SELECT random(-9223372036854775808,9223372036854775807)", "INT8"}} {
				if _, ok := randomScalar(t, p, c, b, tc.q, "random", tc.typ).(int64); !ok {
					t.Fatal("signed range")
				}
			}
			for range 32 {
				n := randomScalar(t, p, c, b, "SELECT random(-7,3)", "random", "INT4").(int64)
				if n < -7 || n > 3 {
					t.Fatal(n)
				}
			}
			var n int64
			values := randomRead(t, p.Ctx, c, b, "SELECT random(7,2147483648)", []string{"random"}, []string{"INT8"}, func(t *testing.T, rows *sql.Rows, rowIndex int) {
				if err := rows.Scan(&n); err != nil {
					t.Fatal("mixed overload int64 scan", err)
				}
				if rowIndex != 0 {
					t.Fatal("mixed overload extra row", rowIndex)
				}
			})
			if len(values) != 1 || values[0][0] != n {
				t.Fatal("mixed overload typed/raw mismatch", values, n)
			}
			if n < 7 || n > 2147483648 {
				t.Fatal("mixed overload", n)
			}
		})
	}
}
func TestReferenceRandomVoidExecAndPreparedStream(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, e := b.DB.Conn(p.Ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			randomReseed(t, p, c, b, .25)
			first := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			seedQ := "SELECT setseed($1)"
			seed, e := c.PrepareContext(p.Ctx, seedQ)
			if e != nil {
				t.Fatal(e)
			}
			defer seed.Close()
			drawQ := "SELECT random()"
			draw, e := c.PrepareContext(p.Ctx, drawQ)
			if e != nil {
				t.Fatal(e)
			}
			defer draw.Close()
			seedHeld := fixture.HeldStatement{Executor: c, Statement: seed, SQL: seedQ}
			drawHeld := fixture.HeldStatement{Executor: c, Statement: draw, SQL: drawQ}
			for range 2 {
				fixture.RunStep(t, p.Ctx, seedHeld, b.Recorder, fixture.Exec(seedQ, 1, .25))
				fixture.RunStep(t, p.Ctx, drawHeld, b.Recorder, fixture.Select(drawQ, []string{"random"}, []string{"FLOAT8"}, [][]any{{first}}))
			}
		})
	}
}
