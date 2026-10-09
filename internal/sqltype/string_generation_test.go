package sqltype

import "testing"

func TestStringGenerationOutputDependence(t *testing.T) {
	types := mathTypes()
	text := types[Text].Usage()
	for _, tc := range []struct {
		id        ID
		dependent bool
	}{
		{Bool, false}, {Int2, false}, {Int4, false}, {Int8, false},
		{Float4, false}, {Float8, false}, {Numeric, false}, {Oid, false},
		{UUID, false}, {Time, false}, {Timetz, false}, {Char, false},
		{Text, false}, {BPChar, false}, {VarChar, false}, {Name, false},
		{Bytea, false}, {Date, true}, {Timestamp, true}, {Timestamptz, true},
		{Interval, true}, {Regclass, true},
	} {
		t.Run(types[tc.id].Name(), func(t *testing.T) {
			scalars := []*Descriptor{types[tc.id]}
			if tc.id != Char {
				domain, err := Domain(90001, "output_domain", "output_domain", types[tc.id].Usage())
				if err != nil {
					t.Fatal(err)
				}
				scalars = append(scalars, domain)
			}
			for _, scalar := range scalars {
				for reverse := range 2 {
					args := []Usage{text, scalar.Usage()}
					textual := [4]bool{true, StringTextInput(tc.id)}
					if reverse == 1 {
						args[0], args[1] = args[1], args[0]
						textual[0], textual[1] = textual[1], textual[0]
					}
					plan, err := BindString("||", text, text, args, textual, false)
					if err != nil {
						t.Fatal(err)
					}
					if got := plan.SessionDependent(); got != tc.dependent {
						t.Fatalf("domain=%v reverse=%d dependence=%v, want %v", scalar.IsDomain(), reverse, got, tc.dependent)
					}
				}
			}
		})
	}
}

func TestGenerationCoercionIODependence(t *testing.T) {
	types := mathTypes()
	text := types[Text].Usage()
	for _, tc := range []struct {
		id        ID
		dependent bool
	}{
		{Bytea, false}, {Int4, false}, {Interval, true},
		{Date, true}, {Timestamp, true}, {Timestamptz, true},
	} {
		t.Run(types[tc.id].Name(), func(t *testing.T) {
			domain, err := Domain(90002, "io_domain", "io_domain", types[tc.id].Usage())
			if err != nil {
				t.Fatal(err)
			}
			for reverse := range 2 {
				source, target := domain.Usage(), text
				if reverse == 1 {
					source, target = target, source
				}
				plan, err := PlanCoercion(source, target, Explicit)
				if err != nil {
					t.Fatal(err)
				}
				if got := plan.SessionDependent(); got != tc.dependent {
					t.Fatalf("reverse=%d dependence=%v, want %v", reverse, got, tc.dependent)
				}
			}
			plan, err := PlanCoercion(domain.Usage(), domain.Usage(), Assignment)
			if err != nil {
				t.Fatal(err)
			}
			if plan.SessionDependent() {
				t.Fatal("same-domain assignment must not invoke I/O")
			}
		})
	}
}
