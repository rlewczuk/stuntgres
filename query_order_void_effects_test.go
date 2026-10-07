package stuntgres_test

import (
	"context"
	"stuntgres"
	"testing"
)

func TestQueryOrderVoidNoEffects(t *testing.T) {
	_, c := randomConnection(t)
	ctx := context.Background()
	for _, q := range []string{"CREATE TABLE order_void_t(id INT)", "INSERT INTO order_void_t VALUES(1),(2),(3)"} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	randomSeed(t, c, .25)
	randomOne(t, c, "SELECT random()")
	wantNext := randomOne(t, c, "SELECT random()")
	for _, tc := range []struct{ q, position string }{
		{"SELECT id FROM order_void_t ORDER BY setseed(0)", "38"},
		{"SELECT setseed(0) AS v FROM order_void_t ORDER BY v", "51"},
		{"SELECT setseed(0) FROM order_void_t ORDER BY 1", "46"},
	} {
		for _, prepared := range []bool{false, true} {
			randomSeed(t, c, .25)
			randomOne(t, c, "SELECT random()")
			var err error
			if prepared {
				stmt, e := c.PrepareContext(ctx, tc.q)
				err = e
				if stmt != nil {
					stmt.Close()
					t.Fatal("analysis failure exposed statement")
				}
			} else {
				rows, e := c.QueryContext(ctx, tc.q)
				err = e
				if rows != nil {
					rows.Close()
					t.Fatal("analysis failure exposed cursor")
				}
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42883", Message: "could not identify an ordering operator for type void", Hint: "Use an explicit ordering operator or modify the query.", Position: tc.position})
			if got := randomOne(t, c, "SELECT random()"); got != wantNext {
				t.Fatal("failed VOID analysis changed physical-session random continuation", got, wantNext)
			}
		}
	}
}
