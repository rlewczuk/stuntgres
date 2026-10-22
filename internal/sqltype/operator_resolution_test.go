package sqltype

import (
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestUserOperatorResolutionPolymorphicConsistency(t *testing.T) {
	types := make(map[ID]*Descriptor)
	for _, d := range Builtins() {
		types[d.ID()] = d
	}
	array := types[1007]
	domain, err := Domain(90000, "array_domain", "public.array_domain", array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := Domain(90001, "leaf_domain", "public.leaf_domain", types[Int4].Usage())
	if err != nil {
		t.Fatal(err)
	}
	leafArray, err := Array(90002, "_leaf_domain", leaf)
	if err != nil {
		t.Fatal(err)
	}
	appendSig := OperatorInputs{5078, 5077}
	prependSig := OperatorInputs{5077, 5078}
	concatSig := OperatorInputs{5078, 5078}
	generic := [3]OperatorInputs{appendSig, prependSig, concatSig}
	for _, tc := range []struct {
		name        string
		left, right *Descriptor
		want        OperatorInputs
	}{
		{"known_left", array, nil, concatSig},
		{"known_right", nil, array, concatSig},
		{"domain_left", domain, nil, concatSig},
		{"domain_right", nil, domain, concatSig},
		{"typed_concat", array, array, concatSig},
		{"append", array, types[Int4], appendSig},
		{"prepend", types[Int4], array, prependSig},
		{"append_promotion", array, types[Int8], appendSig},
		{"prepend_promotion", types[Int8], array, prependSig},
		{"concat_promotion", array, types[1016], concatSig},
		{"concat_promotion_reverse", types[1016], array, concatSig},
		{"domain_leaf_append", leafArray, leaf, appendSig},
		{"domain_leaf_prepend", leaf, leafArray, prependSig},
	} {
		for a := range 3 {
			for b := range 3 {
				if a == b {
					continue
				}
				c := 3 - a - b
				for slot := range 4 {
					t.Run(fmt.Sprintf("%s/%d%d%d/scalar%d", tc.name, a, b, c, slot), func(t *testing.T) {
						var candidates [4]OperatorInputs
						order := [3]int{a, b, c}
						n := 0
						want := -1
						for i := range candidates {
							if i == slot {
								candidates[i] = OperatorInputs{Int4, Int4}
								continue
							}
							candidates[i] = generic[order[n]]
							n++
							if candidates[i] == tc.want {
								want = i
							}
						}
						got, err := ResolveOperator(candidates[:], tc.left, tc.right, false)
						if err != nil || got != want {
							t.Fatalf("index=%d error=%v want=%d signature=%v", got, err, want, tc.want)
						}
						if candidates[got] != tc.want {
							t.Fatalf("signature=%v want=%v", candidates[got], tc.want)
						}
					})
				}
			}
		}
	}
	for _, tc := range []struct {
		name        string
		candidates  []OperatorInputs
		left, right *Descriptor
		code        string
	}{
		{"unknown_ambiguous", generic[:], nil, nil, "42725"},
		{"incompatible_arrays", []OperatorInputs{concatSig}, array, types[1009], "42883"},
		{"incompatible_element", []OperatorInputs{appendSig}, array, types[Text], "42883"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveOperator(tc.candidates, tc.left, tc.right, false)
			var diagnostic *sqlstate.Error
			message := "operator does not exist"
			if tc.code == "42725" {
				message = "operator is not unique"
			}
			if got != -1 || !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != message {
				t.Fatalf("index=%d error=%v", got, err)
			}
		})
	}
	for _, signature := range generic {
		if got, err := ResolveOperator([]OperatorInputs{signature}, nil, nil, false); got != 0 || err != nil {
			t.Fatalf("unknown singleton %v: %d %v", signature, got, err)
		}
	}
}

func TestOperatorGenericConsistency(t *testing.T) {
	types := make(map[ID]*Descriptor)
	for _, d := range Builtins() {
		types[d.ID()] = d
	}
	domain := func(id ID, base *Descriptor) *Descriptor {
		d, err := Domain(id, fmt.Sprintf("d%d", id), fmt.Sprintf("public.d%d", id), base.Usage())
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	enum, err := Enum(90010, "e", "public.e", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	otherEnum, err := Enum(90011, "f", "public.f", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	firstRecord, err := Composite(90012, "r", "public.r", 90013, []CompositeMember{{Attribute: 1, Name: "v", Usage: types[Int4].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	secondRecord, err := Composite(90014, "s", "public.s", 90015, []CompositeMember{{Attribute: 1, Name: "v", Usage: types[Text].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	arrayDomain := domain(90016, types[1007])
	elementDomain := domain(90017, types[Int4])
	rangeDomain := domain(90018, types[Int4Range])
	multiDomain := domain(90019, types[Int4Multirange])
	enumDomain := domain(90020, enum)
	lookupArray := types[types[Regoperator].AssociatedArray()] // Genuine lookup-only alias array.
	for _, tc := range []struct {
		name        string
		left, right *Descriptor
		signature   OperatorInputs
		want        bool
	}{
		{"array_same", types[1007], types[1007], OperatorInputs{2277, 2277}, true},
		{"array_outer_domain", arrayDomain, types[1007], OperatorInputs{2277, 2277}, true},
		{"array_no_promotion", types[1007], types[1016], OperatorInputs{2277, 2277}, false},
		{"enum_same", enum, enum, OperatorInputs{3500, 3500}, true},
		{"enum_distinct", enum, otherEnum, OperatorInputs{3500, 3500}, false},
		{"enum_domain", enumDomain, enum, OperatorInputs{3500, 3500}, false},
		{"enum_unknown", nil, nil, OperatorInputs{3500, 3500}, true},
		{"range_element", types[Int4Range], types[Int4], OperatorInputs{3831, 2283}, true},
		{"range_smallint", types[Int4Range], types[Int2], OperatorInputs{3831, 2283}, false},
		{"range_bigint", types[Int4Range], types[Int8], OperatorInputs{3831, 2283}, false},
		{"range_array", types[Int4Range], types[1007], OperatorInputs{3831, 2283}, false},
		{"range_logical_domain", types[Int4Range], elementDomain, OperatorInputs{3831, 2283}, false},
		{"range_outer_domain", rangeDomain, types[Int4], OperatorInputs{3831, 2283}, true},
		{"range_same", types[Int4Range], rangeDomain, OperatorInputs{3831, 3831}, true},
		{"range_distinct", types[Int4Range], types[Int8Range], OperatorInputs{3831, 3831}, false},
		{"multi_element", types[Int4Multirange], types[Int4], OperatorInputs{4537, 2283}, true},
		{"multi_wrong_element", types[Int4Multirange], types[Int8], OperatorInputs{4537, 2283}, false},
		{"range_multi", types[Int4Range], types[Int4Multirange], OperatorInputs{3831, 4537}, true},
		{"range_multi_domain", rangeDomain, multiDomain, OperatorInputs{3831, 4537}, true},
		{"range_multi_wrong", types[Int8Range], types[Int4Multirange], OperatorInputs{3831, 4537}, false},
		{"multi_same", types[Int4Multirange], multiDomain, OperatorInputs{4537, 4537}, true},
		{"multi_distinct", types[Int4Multirange], types[Int8Multirange], OperatorInputs{4537, 4537}, false},
		{"nonarray", types[Text], types[Int4], OperatorInputs{Text, 2776}, true},
		{"nonarray_domain", types[Text], elementDomain, OperatorInputs{Text, 2776}, true},
		{"nonarray_reject_array", types[Text], types[1007], OperatorInputs{Text, 2776}, false},
		{"nonarray_reject_domain_array", types[Text], arrayDomain, OperatorInputs{Text, 2776}, false},
		{"records_independent", firstRecord, secondRecord, OperatorInputs{2249, 2249}, true},
		{"compatible_numeric", types[Int4], types[Int8], OperatorInputs{5077, 5077}, true},
		{"compatible_category", types[Bool], types[Int4], OperatorInputs{5077, 5077}, false},
		{"compatible_enum", enum, enum, OperatorInputs{5077, 5077}, true},
		{"compatible_distinct_enum", enum, otherEnum, OperatorInputs{5077, 5077}, false},
		{"compatible_temporal", types[Time], types[Timestamp], OperatorInputs{5077, 5077}, false},
		{"compatible_same_domain", elementDomain, elementDomain, OperatorInputs{5077, 5077}, true},
		{"lookup_array_exact", lookupArray, lookupArray, OperatorInputs{2277, 2277}, true},
		{"lookup_leaf_identity", types[Regoperator], types[Regoperator], OperatorInputs{5077, 5077}, true},
		{"canonical_anyarray", types[2277], nil, OperatorInputs{2277, 2277}, false},
		{"temporal_multi", types[4533], nil, OperatorInputs{4537, 2283}, false},
		{"independent_families", types[Int4], types[Text], OperatorInputs{2283, 5077}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := operatorImplicitInputs(tc.left, tc.right, tc.signature, false); got != tc.want {
				t.Fatalf("viability=%t want=%t", got, tc.want)
			}
			reverse := OperatorInputs{tc.signature.Right, tc.signature.Left}
			if got := operatorImplicitInputs(tc.right, tc.left, reverse, false); got != tc.want {
				t.Fatalf("reverse viability=%t want=%t", got, tc.want)
			}
		})
	}
	if !operatorImplicitInputs(types[1007], types[Int4], OperatorInputs{0, 2283}, true) {
		t.Fatal("prefix must ignore left input")
	}
}

func TestOperatorBitUnknownPreference(t *testing.T) {
	types := testTypes()
	bitConcat := OperatorInputs{VarBit, VarBit}
	textConcat := OperatorInputs{Text, Text}
	nonarrayText := OperatorInputs{2776, Text}
	textNonarray := OperatorInputs{Text, 2776}
	signatures := [4]OperatorInputs{bitConcat, textConcat, nonarrayText, textNonarray}
	// The released preferred VARBIT category must win before unknown inputs'
	// string-category fallback. No literal text or NULL value participates here.
	for _, tc := range []struct {
		name        string
		left, right *Descriptor
		want        OperatorInputs
	}{
		{"bit_unknown", types[Bit], nil, bitConcat},
		{"unknown_bit", nil, types[Bit], bitConcat},
		{"varbit_unknown", types[VarBit], nil, bitConcat},
		{"unknown_varbit", nil, types[VarBit], bitConcat},
		{"bit_pair", types[Bit], types[Bit], bitConcat},
		{"varbit_pair", types[VarBit], types[VarBit], bitConcat},
		{"unknown_pair", nil, nil, textConcat},
		{"bit_text", types[Bit], types[Text], nonarrayText},
		{"text_bit", types[Text], types[Bit], textNonarray},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for start := range len(signatures) {
				var candidates [4]OperatorInputs
				for i := range candidates {
					candidates[i] = signatures[(start+i)%len(signatures)]
				}
				index, err := ResolveOperator(candidates[:], tc.left, tc.right, false)
				if err != nil || index < 0 || index >= len(candidates) || candidates[index] != tc.want {
					t.Fatalf("selection=%d error=%v want=%v candidates=%v", index, err, tc.want, candidates)
				}
			}
		})
	}
	// A real exact BIT signature remains decisive before preferred-type scoring.
	exact := append(signatures[:], OperatorInputs{Bit, Bit})
	index, err := ResolveOperator(exact, types[Bit], types[Bit], false)
	if err != nil || index != 4 {
		t.Fatal("preferred type displaced exact operator", index, err)
	}
	if equalityPreferred(Bit) || !equalityPreferred(VarBit) {
		t.Fatal("released bit-category preference")
	}
}
