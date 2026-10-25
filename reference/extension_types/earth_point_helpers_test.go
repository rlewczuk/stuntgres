package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func earthPointScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, backend, c)
		})
	}
}

func earthPointQuery(a, b string) string {
	return "SELECT " + a + " <@> " + b + " AS operator_miles,geo_distance(" + a + "," + b + ") AS function_miles"
}

func earthPointMiles(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, a, z string, want any, tolerance float64) {
	t.Helper()
	earthCoordinateNumbers(t, p, b, c, earthPointQuery(a, z), []string{"operator_miles", "function_miles"}, [][]any{{want, want}}, []float64{tolerance, tolerance})
}

func earthPointHeldRecovery(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{"SELECT $1::point <@> $2::point AS miles", "SELECT geo_distance($1::point,$2::point) AS miles"} {
		h := earthHeld(t, p, c, q)
		func() {
			defer h.Statement.Close()
			want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type point: "bad"`)
			want.Context = "unnamed portal parameter $1 = '...'"
			s := fixture.ErrorDiagnostic(q, want, "bad", "(1,60)")
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			for _, tc := range []struct {
				args []any
				want any
			}{
				{[]any{"(0,60)", "(1,60)"}, "34.54626"},
				{[]any{[]byte("(0,0)"), []byte("(1,0)")}, "69.09318"},
				{[]any{nil, "(1,0)"}, nil},
				{[]any{"(0,0)", nil}, nil},
				{[]any{nil, nil}, nil},
			} {
				rows, err := h.Statement.QueryContext(p.Ctx, tc.args...)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer rows.Close()
					meta, err := rows.ColumnTypes()
					if err != nil || len(meta) != 1 || meta[0].Name() != "miles" || meta[0].DatabaseTypeName() != "FLOAT8" {
						t.Fatalf("held FLOAT8 metadata: %v %v", meta, err)
					}
					if !rows.Next() {
						t.Fatal("missing held result", rows.Err())
					}
					var raw any
					if err := rows.Scan(&raw); err != nil {
						t.Fatal(err)
					}
					if tc.want == nil {
						if raw != nil {
							t.Fatalf("got %v, want NULL", raw)
						}
					} else if got, ok := raw.(float64); !ok || fmt.Sprintf("%.5f", got) != tc.want {
						t.Fatalf("held raw=%T(%v), want miles %s", raw, raw, tc.want)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("held extra row/error", rows.Err())
					}
				}()
			}
		}()
	}
}
