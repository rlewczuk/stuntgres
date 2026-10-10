package sqltype

import (
	"context"
	"testing"
)

func TestRegisteredCollationIdentityAndExecution(t *testing.T) {
	text := testTypes()[Text]
	for _, tc := range []struct {
		provider    uint32
		input, want string
	}{{950, "é", "é"}, {811, "ß", "ß"}, {6411, "ß", "SS"}} {
		ref, err := RegisteredCollation(90000+tc.provider, tc.provider)
		if err != nil || !ref.Valid() {
			t.Fatal(ref, err)
		}
		usage := text.Usage()
		usage.Collation = ref
		if !usage.Valid() {
			t.Fatal(usage)
		}
		domain, err := Domain(ID(200000+tc.provider), "registered_domain", "REGISTERED_DOMAIN", usage)
		if err != nil || domain.Usage().Collation != ref || !domain.Usage().Valid() || domain.Usage().BaseUsage().Collation != ref {
			t.Fatal("domain lost provider authority", domain, err)
		}
		array, err := Array(ID(300000+tc.provider), "_registered_domain", domain)
		if err != nil || array.Usage().Collation != ref || !array.Usage().Valid() {
			t.Fatal("domain array lost provider authority", array, err)
		}
		ordering, err := BindOrdering(usage)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := ordering.Compare(context.Background(), String(text, "Z"), String(text, "a"), InputEnvironment{}); err != nil || n >= 0 {
			t.Fatal(n, err)
		}
		plan, err := BindString("upper", usage, text.Usage(), []Usage{usage}, [4]bool{true}, false)
		if err != nil {
			t.Fatal(err)
		}
		value, err := plan.Evaluate(context.Background(), [4]Value{String(text, tc.input)}, InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.Text()
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
		other, err := RegisteredCollation(ref.ID+1, tc.provider)
		if err != nil || other == ref {
			t.Fatal("provider equivalence erased identity", other, ref, err)
		}
		forged := ref
		forged.ID++
		if forged.Valid() {
			t.Fatal("capability retargeted by OID", forged)
		}
		if (CollationRef{ID: ref.ID, Derivation: Default}).Valid() {
			t.Fatal("arbitrary OID admitted without capability")
		}
	}
	for _, tc := range [][2]uint32{{0, 950}, {90000, 0}, {90000, 963}, {950, 6411}, {811, 950}, {6411, 811}, {962, 950}, {963, 950}} {
		if _, err := RegisteredCollation(tc[0], tc[1]); err == nil {
			t.Fatal("invalid provider admitted", tc)
		}
	}
}
