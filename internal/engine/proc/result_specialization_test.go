package proc

import (
	"testing"

	"stuntgres/internal/sqltype"
)

func TestPseudoResultSpecialization(t *testing.T) {
	u := pseudoUsages()
	d := &Definition{}
	if err := d.SetFormals([]Formal{{Mode: "b", Name: "first", Usage: u[2283]}, {Mode: "o", Name: "second", Usage: u[2283]}, {Mode: "o", Name: "label", Usage: u[sqltype.Text]}}); err != nil {
		t.Fatal(err)
	}
	tuple, err := d.OutputTuple()
	if err != nil {
		t.Fatal(err)
	}
	d.Result = tuple.Usage()
	if !d.Polymorphic() || !d.MatchesOutputTuple(tuple) || tuple.Concrete() || d.Result.Valid() {
		t.Fatal("declaration tuple gained executable authority")
	}
	var first *Definition
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Int4} {
		result, err := d.ResolveResult(func(formal sqltype.Usage) sqltype.Usage {
			if formal.Type.ID() == 2283 {
				return u[id]
			}
			return formal
		})
		if err != nil {
			t.Fatal(err)
		}
		s := InvocationSignature{Inputs: []sqltype.Usage{u[id]}, Result: result}
		private, err := d.Specialize(s)
		if err != nil {
			t.Fatal(err)
		}
		if private == d || !s.ValidFor(d) || !private.MatchesOutputTuple(result.Type) || !result.Valid() || private.Formals[0].Usage != u[id] || private.Formals[1].Usage != u[id] || private.Formals[2].Usage != u[sqltype.Text] {
			t.Fatal("incomplete private specialization", id)
		}
		s.Inputs[0] = u[sqltype.Bytea]
		if private.Inputs[0] != u[id] || d.Inputs[0] != u[2283] || d.Formals[0].Usage != u[2283] || d.Result.Type != tuple || !d.MatchesOutputTuple(tuple) {
			t.Fatal("borrowed or mutated declaration storage")
		}
		if first == nil {
			first = private
		} else if first.Formals[0].Usage != u[sqltype.Int4] || first.Result.Type.CompositeMember(0).Usage != u[sqltype.Int4] {
			t.Fatal("later specialization mutated earlier activation")
		}
	}
	members := []sqltype.CompositeMember{{Attribute: 1, Name: "first", Usage: u[sqltype.Int4]}, {Attribute: 2, Name: "second", Usage: u[sqltype.Int4]}, {Attribute: 3, Name: "label", Usage: u[sqltype.Text]}}
	for _, mutation := range []func([]sqltype.CompositeMember){
		func(m []sqltype.CompositeMember) { m[0].Name = "forged" },
		func(m []sqltype.CompositeMember) { m[0].Attribute = 8 },
		func(m []sqltype.CompositeMember) { m[1].Usage = u[sqltype.Text] },
		func(m []sqltype.CompositeMember) { m[2].Usage = pseudoUsages()[sqltype.Text] },
	} {
		changed := append([]sqltype.CompositeMember(nil), members...)
		mutation(changed)
		forged, err := sqltype.RecordShape(changed)
		if err != nil {
			t.Fatal(err)
		}
		s := InvocationSignature{Inputs: []sqltype.Usage{u[sqltype.Int4]}, Result: forged.Usage()}
		if s.ValidFor(d) {
			t.Fatal("forged output tuple accepted")
		}
		if _, err := d.Specialize(s); err == nil {
			t.Fatal("forged output tuple specialized")
		}
	}
	if (InvocationSignature{Inputs: []sqltype.Usage{u[sqltype.Int4]}, Result: sqltype.DeferredRecord().Usage()}).ValidFor(d) {
		t.Fatal("derived tuple erased to deferred RECORD")
	}
	concrete := &Definition{Inputs: []sqltype.Usage{u[sqltype.Int4]}, Result: u[sqltype.Text]}
	unchanged, err := concrete.Specialize(InvocationSignature{Inputs: concrete.Inputs, Result: concrete.Result})
	if err != nil || unchanged != concrete {
		t.Fatal("ordinary declaration unnecessarily copied", err)
	}
	unresolved, err := d.ResolveResult(func(formal sqltype.Usage) sqltype.Usage {
		if formal.Type.ID() == 2283 {
			return sqltype.Usage{}
		}
		return formal
	})
	if err != nil || unresolved.Type != nil {
		t.Fatal("unknown family lost indeterminate result", err)
	}
	outputOnly := &Definition{Result: u[sqltype.Int4], Formals: []Formal{{Mode: "o", Usage: u[2283]}}}
	if !outputOnly.Polymorphic() {
		t.Fatal("output-only type variable not detected")
	}
}

func TestPseudoDerivedOutputRestrictions(t *testing.T) {
	u := pseudoUsages()
	for _, tc := range []struct {
		input, output, actual sqltype.ID
	}{
		{2283, 3500, sqltype.Int4},
		{2283, 2776, 1007},
		{5077, 5079, 1007},
	} {
		d := &Definition{}
		if err := d.SetFormals([]Formal{
			{Mode: "i", Name: "x", Usage: u[tc.input]},
			{Mode: "o", Name: "a", Usage: u[tc.output]},
			{Mode: "o", Name: "b", Usage: u[sqltype.Int4]},
		}); err != nil {
			t.Fatal(err)
		}
		tuple, err := d.OutputTuple()
		if err != nil {
			t.Fatal(err)
		}
		d.Result = tuple.Usage()
		result, err := d.ResolveResult(func(formal sqltype.Usage) sqltype.Usage {
			if sqltype.PolymorphicFamily(formal.Type.ID()) != 0 {
				return u[tc.actual]
			}
			return formal
		})
		if err != nil {
			t.Fatal(err)
		}
		signature := InvocationSignature{Inputs: []sqltype.Usage{u[tc.actual]}, Result: result}
		private, err := d.Specialize(signature)
		if err != nil || !signature.ValidFor(d) || private.Formals[1].Usage != u[tc.actual] ||
			!private.MatchesOutputTuple(result.Type) || d.Formals[1].Usage != u[tc.output] {
			t.Fatal("derived output failed family projection", tc, err)
		}
		scalar := &Definition{Inputs: d.Inputs, Result: u[tc.output]}
		scalarSignature := InvocationSignature{Inputs: signature.Inputs, Result: u[tc.actual]}
		if scalarSignature.ValidFor(scalar) {
			t.Fatal("derived output policy leaked to scalar result", tc)
		}
		restricted := &Definition{Inputs: []sqltype.Usage{u[tc.output]}, Result: u[sqltype.Int4]}
		if (InvocationSignature{Inputs: signature.Inputs, Result: restricted.Result}).ValidFor(restricted) {
			t.Fatal("derived output policy leaked to actual input", tc)
		}
	}
}
