package reference_test

import "testing"

func TestReferenceSearchPathQualifiedRandomConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, tc := range []struct {
			q    string
			want any
		}{{"SELECT pg_catalog.random(7,7)", int64(7)}, {"SELECT pg_catalog.random_normal(7,0)", float64(7)}, {"SELECT pg_catalog.setseed(0)", ""}} {
			var got any
			if err := backend.db.QueryRow(tc.q).Scan(&got); err != nil || referenceNormalize(got) != tc.want {
				t.Fatal(tc.q, got, err)
			}
			held, err := backend.db.Prepare(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			err = held.QueryRow().Scan(&got)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			if err != nil || referenceNormalize(got) != tc.want {
				t.Fatal(tc.q, got, err)
			}
		}
		rows, err := backend.db.Query("SELECT public.random()")
		if rows != nil {
			rows.Close()
			t.Fatal("missing random provider published rows")
		}
		want := controlsReferenceDiagnostic("42883", "function public.random() does not exist")
		want.Position = "8"
		want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		referenceAssertDiagnostic(t, err, &want)
	}
}
