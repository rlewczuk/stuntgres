package sqltype

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestArrayCompareTypeCompatibility(t *testing.T) {
	types := testTypes()
	enum, err := Enum(45000, "ac_enum", "public.ac_enum", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := Domain(45001, "ac_domain", "public.ac_domain", enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	array, err := Array(45002, "_ac_integer", types[Int4])
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		left, right *Descriptor
		valid       bool
	}{
		{"integer_width", types[Int4], types[Int8], true},
		{"numeric_width", types[Int8], types[Numeric], true},
		{"text_without_input_collation", types[Text], types[Text], true},
		{"money", types[Money], types[Money], true},
		{"network", types[Cidr], types[Inet], true},
		{"enum_identity", enum, enum, true},
		{"enum_domain", domain, enum, false},
		{"enum_domain_identity", domain, domain, false},
		{"json", types[JSON], types[JSON], false},
		{"ordinary_array", array, array, true},
		{"incompatible", types[Int4], types[Bool], false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckComparisonTypes(tc.left, tc.right)
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Message != "incompatible comparison operands" {
					t.Fatal(err)
				}
			}
		})
	}
	// Type admission must not fabricate a collation capability. Final binding
	// still requires the genuine collatable input authority.
	if _, err := BindComparison(types[Text], types[Text], CollationRef{}, false); err == nil {
		t.Fatal("final predicate admitted missing input collation")
	}
	for _, pair := range [][2]*Descriptor{{nil, types[Int4]}, {types[Int4], nil}} {
		err := CheckComparisonTypes(pair[0], pair[1])
		var diagnostic *sqlstate.Error
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal("invalid analysis descriptor must retain ordinary invariant error", err)
		}
	}
}
