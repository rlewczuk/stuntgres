package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceSearchPathArraySourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	s := referenceExec("CREATE TABLE public.path_array_order(id INT,flag BOOL)", 0)
	s.nonContext = true
	p.step(t, s)
	for _, populated := range []bool{false, true} {
		if populated {
			s := referenceExec("INSERT INTO public.path_array_order VALUES(1,NULL)", 1)
			s.nonContext = true
			p.step(t, s)
		}
		for _, backend := range p.backends() {
			for _, tc := range []struct {
				q, label, typ string
				value         any
			}{
				{"SELECT id FROM public.path_array_order ORDER BY current_schemas(false)", "id", "INT4", int64(1)},
				{"SELECT current_schemas(false) AS paths FROM public.path_array_order ORDER BY paths", "paths", "_NAME", "{public}"},
				{"SELECT current_schemas(flag) FROM public.path_array_order ORDER BY 1", "current_schemas", "_NAME", nil},
			} {
				var s referenceStep
				if backend.pg {
					want := [][]any{}
					if populated {
						want = [][]any{{tc.value}}
					}
					s = referenceSelect(tc.q, []string{tc.label}, []string{tc.typ}, want)
				} else {
					want := controlsReferenceDiagnostic("42883", "could not identify an ordering operator for type name[]")
					want.Hint = "Use an explicit ordering operator or modify the query."
					want.Position = strconv.Itoa(strings.LastIndex(tc.q, "ORDER BY ") + len("ORDER BY ") + 1)
					s = referenceErrorDiagnostic(tc.q, want)
					s.selectRows = true
				}
				s.nonContext = true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				s.prepared = true
				if !backend.pg {
					s.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
		}
	}
}

func TestReferenceSearchPathBitwiseSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, s := range []referenceStep{
		referenceExec("CREATE TABLE public.path_bits(id INT,mask INT DEFAULT (7 OPERATOR(pg_catalog.&) 3))", 0),
		referenceExec("INSERT INTO public.path_bits(id) VALUES(7)", 1),
	} {
		s.nonContext = true
		p.step(t, s)
	}
	for _, backend := range p.backends() {
		for _, tc := range []struct {
			expression string
			want       int64
		}{
			{"id OPERATOR(pg_catalog.&) 3", 3}, {"id OPERATOR(pg_catalog.|) 8", 15},
			{"id OPERATOR(pg_catalog.#) 3", 4}, {"id OPERATOR(pg_catalog.<<) 1", 14},
			{"id OPERATOR(pg_catalog.>>) 1", 3}, {"OPERATOR(pg_catalog.~) id", -8},
			{"CAST(id OPERATOR(pg_catalog.&) 3 AS pg_catalog.int2)", 3}, {"mask", 3},
		} {
			q := "SELECT " + tc.expression + " FROM public.path_bits"
			var got int64
			if err := backend.db.QueryRow(q).Scan(&got); err != nil || got != tc.want {
				t.Fatal(q, got, tc.want, err)
			}
			held, err := backend.db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			err = held.QueryRow().Scan(&got)
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			if err != nil || got != tc.want {
				t.Fatal(q, got, tc.want, err)
			}
		}
		rows, err := backend.db.Query("SELECT id OPERATOR(public.&) 3 FROM public.path_bits")
		if rows != nil {
			rows.Close()
			t.Fatal("failed operator lookup published rows")
		}
		want := controlsReferenceDiagnostic("42883", "operator does not exist: integer public.& integer")
		want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		want.Position = "11"
		referenceAssertDiagnostic(t, err, &want)
	}
}
