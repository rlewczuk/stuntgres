package proc

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func TestPseudoInvocationContainerAuthority(t *testing.T) {
	u, foreign := pseudoUsages(), pseudoUsages()
	copiedArray, copiedRange, copiedMulti := *u[1007].Type, *u[3904].Type, *u[4451].Type
	domain, err := sqltype.Domain(98001, "authority_domain", "public.authority_domain", u[sqltype.Int4])
	if err != nil {
		t.Fatal(err)
	}
	enum, err := sqltype.Enum(98002, "authority_enum", "public.authority_enum", []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range []struct {
		name string
		scalar, array, ranged, multi sqltype.ID
	}{
		{"simple", 2283, 2277, 3831, 4537},
		{"common", 5077, 5078, 5080, 4538},
	} {
		t.Run(family.name, func(t *testing.T) {
			for _, tc := range []struct {
				name string
				formals []sqltype.ID
				inputs []sqltype.Usage
				resultFormal sqltype.ID
				result sqltype.Usage
				valid bool
			}{
				{"scalar", []sqltype.ID{family.scalar}, []sqltype.Usage{u[sqltype.Int4]}, family.scalar, u[sqltype.Int4], true},
				{"domain", []sqltype.ID{family.scalar}, []sqltype.Usage{domain.Usage()}, family.scalar, domain.Usage(), true},
				{"enum", []sqltype.ID{family.scalar}, []sqltype.Usage{enum.Usage()}, family.scalar, enum.Usage(), true},
				{"array", []sqltype.ID{family.array, family.scalar}, []sqltype.Usage{u[1007], u[sqltype.Int4]}, family.array, u[1007], true},
				{"range-scalar", []sqltype.ID{family.ranged, family.scalar}, []sqltype.Usage{u[3904], u[sqltype.Int4]}, family.multi, u[4451], true},
				{"scalar-range", []sqltype.ID{family.scalar, family.ranged}, []sqltype.Usage{u[sqltype.Int4], u[3904]}, family.multi, u[4451], true},
				{"multirange", []sqltype.ID{family.multi}, []sqltype.Usage{u[4451]}, family.ranged, u[3904], true},
				{"copied-array-result", []sqltype.ID{family.array}, []sqltype.Usage{u[1007]}, family.array, copiedArray.Usage(), false},
				{"mismatched-array-inputs", []sqltype.ID{family.array, family.array}, []sqltype.Usage{u[1007], copiedArray.Usage()}, family.scalar, u[sqltype.Int4], false},
				{"foreign-range-subtype", []sqltype.ID{family.ranged, family.scalar}, []sqltype.Usage{u[3904], foreign[sqltype.Int4]}, family.ranged, u[3904], false},
				{"foreign-scalar-before-range", []sqltype.ID{family.scalar, family.ranged}, []sqltype.Usage{foreign[sqltype.Int4], u[3904]}, family.scalar, foreign[sqltype.Int4], false},
				{"foreign-multirange-subtype", []sqltype.ID{family.multi}, []sqltype.Usage{u[4451]}, family.scalar, foreign[sqltype.Int4], false},
				{"copied-range-companion", []sqltype.ID{family.ranged}, []sqltype.Usage{copiedRange.Usage()}, family.multi, u[4451], false},
				{"copied-multirange-companion", []sqltype.ID{family.ranged}, []sqltype.Usage{u[3904]}, family.multi, copiedMulti.Usage(), false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					d := &Definition{Result: u[tc.resultFormal]}
					formals := make([]Formal, len(tc.formals))
					for i, id := range tc.formals {
						formals[i] = Formal{Mode: "i", Usage: u[id]}
					}
					if err := d.SetFormals(formals); err != nil {
						t.Fatal(err)
					}
					s := InvocationSignature{Inputs: tc.inputs, Result: tc.result}
					if s.ValidFor(d) != tc.valid {
						t.Fatalf("ValidFor = %v, want %v", s.ValidFor(d), tc.valid)
					}
					private, err := d.Specialize(s)
					if !tc.valid {
						if err == nil || private != nil {
							t.Fatal("foreign container specialized")
						}
						return
					}
					if err != nil || private == d || private.Result != tc.result {
						t.Fatal("lawful private specialization rejected", err)
					}
					for i, usage := range tc.inputs {
						if private.Inputs[i] != usage || private.Formals[i].Usage != usage || d.Formals[i].Usage != u[tc.formals[i]] {
							t.Fatal("specialization lost input authority or mutated declaration")
						}
					}
				})
			}
		})
	}
}

func TestPseudoDerivedContainerAuthority(t *testing.T) {
	u := pseudoUsages()
	copied := *u[1007].Type
	for _, array := range []sqltype.ID{2277, 5078} {
		d := &Definition{}
		if err := d.SetFormals([]Formal{
			{Mode: "i", Usage: u[array]},
			{Mode: "o", Name: "first", Usage: u[array]},
			{Mode: "o", Name: "second", Usage: u[array]},
		}); err != nil {
			t.Fatal(err)
		}
		declared, err := d.OutputTuple()
		if err != nil {
			t.Fatal(err)
		}
		d.Result = declared.Usage()
		for _, actual := range []sqltype.Usage{u[1007], copied.Usage()} {
			result, err := sqltype.RecordShape([]sqltype.CompositeMember{
				{Attribute: 1, Name: "first", Usage: actual},
				{Attribute: 2, Name: "second", Usage: actual},
			})
			if err != nil {
				t.Fatal(err)
			}
			s := InvocationSignature{Inputs: []sqltype.Usage{u[1007]}, Result: result.Usage()}
			valid := actual == u[1007]
			if s.ValidFor(d) != valid {
				t.Fatal("derived array occurrence lost family authority", array)
			}
			private, err := d.Specialize(s)
			if (err == nil) != valid || !valid && private != nil {
				t.Fatal("derived array specialization lost family authority", array, err)
			}
		}
	}
}
