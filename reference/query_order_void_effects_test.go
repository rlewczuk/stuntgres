package reference_test

import (
	"testing"
)

func TestReferenceQueryOrderVoidNoEffects(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE order_void_t(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO order_void_t VALUES(1),(2),(3)", 3))
	for _, backend := range p.backends() {
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
				if backend.pg {
					bytes, ok := result.([]byte)
					if !ok || len(bytes) != 0 {
						t.Fatalf("pq VOID payload %T %#v", result, result)
					}
				} else if result != "" {
					t.Fatal("native VOID payload", result)
				}
				if err := c.QueryRowContext(p.ctx, "SELECT setseed($1)", .25).Scan(&result); err != nil {
					t.Fatal(err)
				}
				if backend.pg {
					bytes, ok := result.([]byte)
					if !ok || len(bytes) != 0 {
						t.Fatalf("pq bound VOID payload %T %#v", result, result)
					}
				} else if result != "" {
					t.Fatal("native bound VOID payload", result)
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
			for _, tc := range []struct{ q, position string }{
				{"SELECT id FROM order_void_t ORDER BY setseed(0)", "38"},
				{"SELECT setseed(0) AS v FROM order_void_t ORDER BY v", "51"},
				{"SELECT setseed(0) FROM order_void_t ORDER BY 1", "46"},
			} {
				for _, prepared := range []bool{false, true} {
					seed()
					draw()
					q := tc.q
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an ordering operator for type void", Hint: "Use an explicit ordering operator or modify the query.", Position: tc.position}
					if prepared {
						stmt, err := c.PrepareContext(p.ctx, q)
						if stmt != nil {
							stmt.Close()
							t.Fatal("analysis failure exposed statement")
						}
						if err == nil {
							t.Fatal("VOID ordering unexpectedly prepared")
						}
						referenceAssertDiagnostic(t, err, &want)
					} else {
						rows, err := c.QueryContext(p.ctx, q)
						if rows != nil {
							rows.Close()
							t.Fatal("analysis failure exposed cursor")
						}
						if err == nil {
							t.Fatal("VOID ordering unexpectedly executed")
						}
						referenceAssertDiagnostic(t, err, &want)
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
