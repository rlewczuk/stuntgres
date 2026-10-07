//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryOrderVoidNoEffects(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE order_void_t(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO order_void_t VALUES(1),(2),(3)", 3))
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"memory", p.memory}, {"postgres", p.postgres}} {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			seed := func() {
				var result any
				if err := c.QueryRowContext(p.ctx, "SELECT setseed(.25)").Scan(&result); err != nil {
					t.Fatal(err)
				}
			}
			draw := func() float64 {
				var result float64
				if err := c.QueryRowContext(p.ctx, "SELECT random()").Scan(&result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			seed()
			draw()
			wantNext := draw()
			for _, q := range []string{
				"SELECT id FROM order_void_t ORDER BY setseed(0)",
				"SELECT setseed(0) AS v FROM order_void_t ORDER BY v",
				"SELECT setseed(0) FROM order_void_t ORDER BY 1",
			} {
				for _, prepared := range []bool{false, true} {
					seed()
					draw()
					if prepared {
						stmt, err := c.PrepareContext(p.ctx, q)
						if stmt != nil {
							stmt.Close()
							t.Fatal("analysis failure exposed statement")
						}
						if err == nil {
							t.Fatal("VOID ordering unexpectedly prepared")
						}
					} else {
						rows, err := c.QueryContext(p.ctx, q)
						if rows != nil {
							rows.Close()
							t.Fatal("analysis failure exposed cursor")
						}
						if err == nil {
							t.Fatal("VOID ordering unexpectedly executed")
						}
					}
					// Each backend's seeded control is an independent no-effect
					// oracle, not a cross-backend random-number expectation.
					if got := draw(); got != wantNext {
						t.Fatal("failed analysis changed physical-session random continuation", got, wantNext)
					}
				}
			}
		})
	}
}
