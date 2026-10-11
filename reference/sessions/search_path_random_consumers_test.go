package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSearchPathQualifiedRandomConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, tc := range []struct {
			q    string
			want any
		}{{"SELECT pg_catalog.random(7,7)", int64(7)}, {"SELECT pg_catalog.random_normal(7,0)", float64(7)}, {"SELECT pg_catalog.setseed(0)", ""}} {
			var got any
			if err := backend.DB.QueryRow(tc.q).Scan(&got); err != nil || fixture.Normalize(got) != tc.want {
				t.Fatal(tc.q, got, err)
			}
			held, err := backend.DB.Prepare(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			err = held.QueryRow().Scan(&got)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			if err != nil || fixture.Normalize(got) != tc.want {
				t.Fatal(tc.q, got, err)
			}
		}
		rows, err := backend.DB.Query("SELECT public.random()")
		if rows != nil {
			rows.Close()
			t.Fatal("missing random provider published rows")
		}
		want := fixture.ControlsReferenceDiagnostic("42883", "function public.random() does not exist")
		want.Position = "8"
		want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		fixture.AssertDiagnostic(t, err, &want)
	}
}
