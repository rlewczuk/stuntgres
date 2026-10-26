package proc

import (
	"slices"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestRoutineProcedureInvocationSpecialization(t *testing.T) {
	u := pseudoUsages()
	for _, tc := range []struct {
		name    string
		formals []Formal
	}{
		{"sole", []Formal{{Mode: "i", Name: "x", Usage: u[2283]}, {Mode: "o", Name: "answer", Usage: u[2283]}}},
		{"multiple", []Formal{{Mode: "i", Name: "x", Usage: u[2283]}, {Mode: "o", Name: "first", Usage: u[2283]}, {Mode: "o", Name: "second", Usage: u[2283]}}},
		{"inout", []Formal{{Mode: "b", Name: "a", Usage: u[2283]}, {Mode: "o", Name: "b", Usage: u[2283]}}},
		{"array", []Formal{{Mode: "i", Name: "x", Usage: u[2277]}, {Mode: "o", Name: "answer", Usage: u[2277]}}},
		{"array_multiple", []Formal{{Mode: "i", Name: "x", Usage: u[2277]}, {Mode: "o", Name: "first", Usage: u[2277]}, {Mode: "o", Name: "second", Usage: u[2277]}}},
		{"array_inout", []Formal{{Mode: "b", Name: "a", Usage: u[2277]}, {Mode: "o", Name: "b", Usage: u[2277]}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Definition{Procedure: true}
			if err := d.SetFormals(tc.formals); err != nil {
				t.Fatal(err)
			}
			tuple, err := d.OutputTuple()
			if err != nil {
				t.Fatal(err)
			}
			d.Result = tuple.Usage()
			original := slices.Clone(d.Formals)
			ids := []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Int4}
			if d.Inputs[0].Type.ID() == 2277 {
				ids = []sqltype.ID{1007, 1009, 1007}
			}
			var first *Definition
			for _, id := range ids {
				result, err := d.ResolveResult(func(formal sqltype.Usage) sqltype.Usage { return u[id] })
				if err != nil {
					t.Fatal(err)
				}
				signature := InvocationSignature{Inputs: []sqltype.Usage{u[id]}, Result: result}
				private, err := d.Specialize(signature)
				if err != nil {
					t.Fatal(err)
				}
				if private == d || &private.Formals[0] == &d.Formals[0] || &private.Inputs[0] == &signature.Inputs[0] || private.Result != result || !private.MatchesOutputTuple(result.Type) {
					t.Fatal("specialization lost private backing or exact tuple")
				}
				if !slices.Equal(private.InputFormals, d.InputFormals) || !slices.Equal(private.OutputFormals, d.OutputFormals) || !slices.Equal(private.FormalInputs, d.FormalInputs) {
					t.Fatal("specialization changed projections")
				}
				for i, index := range d.InputFormals {
					if private.Formals[index].Usage != signature.Inputs[i] {
						t.Fatal("input projection", i)
					}
				}
				for i, index := range d.OutputFormals {
					member := result.Type.CompositeMember(i)
					if private.Formals[index].Usage != member.Usage || member.Name != tuple.CompositeMember(i).Name || member.Attribute != tuple.CompositeMember(i).Attribute || private.Formals[index].Name != d.Formals[index].Name {
						t.Fatal("output member projection", i)
					}
				}
				signature.Inputs[0] = u[sqltype.Bytea]
				if private.Inputs[0] != u[id] || d.Result.Type != tuple || !slices.Equal(d.Formals, original) {
					t.Fatal("caller or published storage mutated")
				}
				if first == nil {
					first = private
				} else if first.Inputs[0] != u[ids[0]] || first.Result.Type.CompositeMember(0).Usage != u[ids[0]] {
					t.Fatal("prior activation mutated")
				}
			}
			result, err := d.ResolveResult(func(sqltype.Usage) sqltype.Usage { return u[ids[0]] })
			if err != nil {
				t.Fatal(err)
			}
			members := make([]sqltype.CompositeMember, result.Type.CompositeLen())
			for i := range members {
				members[i] = result.Type.CompositeMember(i)
			}
			missingLabel := slices.Clone(members)
			missingLabel[0].Name = ""
			if _, err := sqltype.RecordShape(missingLabel); err == nil {
				t.Fatal("missing output label constructed")
			}
			if (InvocationSignature{Result: result}).ValidFor(d) || (InvocationSignature{Inputs: []sqltype.Usage{u[ids[0]], u[ids[0]]}, Result: result}).ValidFor(d) {
				t.Fatal("wrong input arity accepted")
			}
			for _, mutate := range []func([]sqltype.CompositeMember) []sqltype.CompositeMember{
				func(m []sqltype.CompositeMember) []sqltype.CompositeMember { m[0].Name = "wrong"; return m },
				func(m []sqltype.CompositeMember) []sqltype.CompositeMember {
					for i := range m {
						m[i].Attribute = int32(i + 8)
					}
					return m
				},
				func(m []sqltype.CompositeMember) []sqltype.CompositeMember {
					if len(m) > 1 {
						m[0].Name, m[1].Name = m[1].Name, m[0].Name
					} else {
						m[0].Name = "missing"
					}
					return m
				},
				func(m []sqltype.CompositeMember) []sqltype.CompositeMember {
					return append(m, sqltype.CompositeMember{Attribute: int32(len(m) + 1), Name: "extra", Usage: u[ids[0]]})
				},
				func(m []sqltype.CompositeMember) []sqltype.CompositeMember { return m[:0] },
				func(m []sqltype.CompositeMember) []sqltype.CompositeMember { m[0].Usage = u[sqltype.Bytea]; return m },
				func(m []sqltype.CompositeMember) []sqltype.CompositeMember {
					foreign := *m[0].Usage.Type
					for i := range m {
						if m[i].Usage.Type == members[0].Usage.Type {
							m[i].Usage.Type = &foreign
						}
					}
					return m
				},
			} {
				changed := mutate(slices.Clone(members))
				if len(changed) == 0 {
					if (InvocationSignature{Inputs: []sqltype.Usage{u[ids[0]]}, Result: sqltype.DeferredRecord().Usage()}).ValidFor(d) {
						t.Fatal("missing tuple accepted")
					}
					continue
				}
				forged, err := sqltype.RecordShape(changed)
				if err != nil {
					t.Fatal(err)
				}
				bad := InvocationSignature{Inputs: []sqltype.Usage{u[ids[0]]}, Result: forged.Usage()}
				if bad.ValidFor(d) {
					t.Fatal("invalid output contract accepted")
				}
				if private, err := d.Specialize(bad); err == nil || private != nil {
					t.Fatal("invalid signature specialized")
				}
			}
		})
	}
	fixed := &Definition{Procedure: true}
	if err := fixed.SetFormals([]Formal{{Mode: "o", Name: "answer", Usage: u[sqltype.Int4]}}); err != nil {
		t.Fatal(err)
	}
	tuple, err := fixed.OutputTuple()
	if err != nil {
		t.Fatal(err)
	}
	fixed.Result = tuple.Usage()
	unchanged, err := fixed.Specialize(InvocationSignature{Result: fixed.Result})
	if err != nil || unchanged != fixed {
		t.Fatal("unchanged specialization copied declaration", err)
	}
}
