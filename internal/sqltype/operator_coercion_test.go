package sqltype

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCastDefinitionOperatorCoercion(t *testing.T) {
	types := testTypes()
	enum, err := Enum(90010, "cast_operand", "public.cast_operand", []string{"red"})
	if err != nil {
		t.Fatal(err)
	}
	// These pure feasibility predicates represent direct admitted edges. No
	// binding, value evaluation or routine invocation is performed by ranking.
	integerOnly := func(source *Descriptor, target ID) bool {
		return source.ID() == target || source == enum && target == Int4
	}
	integerAndBool := func(source *Descriptor, target ID) bool {
		return integerOnly(source, target) || source == enum && target == Bool
	}
	candidates := []OperatorInputs{{Int4, Int4}, {Bool, Bool}}
	if index, err := ResolveOperatorWithCoercion(candidates, enum, enum, false, integerOnly); err != nil || index != 0 {
		t.Fatal("direct declared implicit edge was not viable", index, err)
	}
	if index, err := ResolveOperator(candidates, enum, enum, false); err == nil || index != -1 {
		t.Fatal("fixed resolver unexpectedly admitted user edge", index, err)
	}
	for _, right := range []*Descriptor{enum, nil} {
		index, err := ResolveOperatorWithCoercion(candidates, enum, right, false, integerAndBool)
		var diagnostic *sqlstate.Error
		if index != -1 || !errors.As(err, &diagnostic) || diagnostic.Code != "42725" {
			t.Fatal("competing direct edges did not remain ambiguous", index, err)
		}
	}
	if index, err := ResolveOperatorWithCoercion(candidates, nil, nil, false, integerAndBool); err == nil || index != -1 {
		t.Fatal("both unknown positions lost category ambiguity", index, err)
	}
	domain, err := Domain(90011, "cast_domain", "public.cast_domain", types[Int4].Usage())
	if err != nil {
		t.Fatal(err)
	}
	domainCandidates := []OperatorInputs{{Int4, Int4}, {domain.ID(), domain.ID()}}
	if index, err := ResolveOperatorWithCoercion(domainCandidates, domain, nil, false, integerAndBool); err != nil || index != 1 {
		t.Fatal("domain exact signature lost to base conversion", index, err)
	}
	exact := append(candidates, OperatorInputs{enum.ID(), enum.ID()})
	if index, err := ResolveOperatorWithCoercion(exact, enum, enum, false, integerAndBool); err != nil || index != 2 {
		t.Fatal("implicit conversion displaced exact signature", index, err)
	}
	if index, err := ResolveOperatorWithCoercion([]OperatorInputs{{0, Int4}}, nil, enum, true, integerOnly); err != nil || index != 0 {
		t.Fatal("prefix conversion not admitted", index, err)
	}
	// Polymorphic shape and exact element consistency cannot be widened by a
	// concrete conversion predicate, even one admitting every concrete pair.
	allConcrete := func(*Descriptor, ID) bool { return true }
	for _, tc := range []struct {
		left, right *Descriptor
		signature   OperatorInputs
	}{
		{types[Int4], types[Int4], OperatorInputs{2277, 2277}},
		{types[1007], types[1016], OperatorInputs{2277, 2277}},
		{types[1007], types[Int8], OperatorInputs{2277, 2283}},
		{enum, types[Int4], OperatorInputs{5077, 5077}},
	} {
		if index, err := ResolveOperatorWithCoercion([]OperatorInputs{tc.signature}, tc.left, tc.right, false, allConcrete); err == nil || index != -1 {
			t.Fatal("concrete feasibility bypassed polymorphic identity", index, err)
		}
	}
	// Compatible polymorphism must also honor the supplied direction instead
	// of silently falling back to the opposite fixed BIT -> VARBIT edge.
	reverseBits := func(source *Descriptor, target ID) bool {
		return source.ID() == target || source.ID() == VarBit && target == Bit
	}
	if !operatorImplicitInputsWithCoercion(types[Bit], types[VarBit], OperatorInputs{5077, 5077}, false, reverseBits) {
		t.Fatal("compatible polymorphism ignored supplied common conversion")
	}
	if chosen, ok := commonTypeCandidateWithCoercion(types[VarBit], types[Bit], reverseBits); !ok || chosen != types[Bit] {
		t.Fatal("common choice used fixed rather than supplied direction", chosen, ok)
	}
}
