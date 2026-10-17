package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

type aclCodecFixture struct {
	authority ACLAuthority
	warnings  int
}

func (r *aclCodecFixture) ACLItemAuthority() *ACLAuthority { return &r.authority }
func (r *aclCodecFixture) ResolveACLRole(_ context.Context, name string) (uint32, error) {
	switch name {
	case "postgres":
		return 10, nil
	case "A":
		return 42, nil
	case "B":
		return 77, nil
	case `a "quote\é`:
		return 91, nil
	case strings.Repeat("n", 63):
		return 92, nil
	case strings.Repeat("é", 31):
		return 93, nil
	}
	return 0, failure("42704", `role "`+name+`" does not exist`)
}
func (r *aclCodecFixture) WarnACLGrantor(context.Context) { r.warnings++ }
func (r *aclCodecFixture) FormatACLRole(authority *ACLAuthority, id uint32) (string, error) {
	if authority != &r.authority {
		return "", errors.New("foreign fixture authority")
	}
	switch id {
	case 10:
		return "postgres", nil
	case 42:
		return "A", nil
	case 77:
		return "B", nil
	case 91:
		return `a "quote\é`, nil
	case 92:
		return strings.Repeat("n", 63), nil
	case 93:
		return strings.Repeat("é", 31), nil
	}
	return "", errors.New("unknown fixture identity")
}

func TestACLItemIdentity(t *testing.T) {
	types, other := testTypes(), testTypes()
	d, a := types[ACLItem], types[ACLItemArray]
	if d.ID() != 1033 || a.ID() != 1034 || !d.Concrete() || !a.Concrete() || d.Name() != "aclitem" || d.SQLName() != "ACLITEM" || d.DatabaseName() != "ACLITEM" || d.PostgreSQLTypeLength() != 16 || d.AssociatedArray() != a.ID() || a.Element() != d || a.DatabaseName() != "_ACLITEM" || d.Usage().Typmod != -1 || d.Usage().Collation != (CollationRef{}) || !StoredScalarCastType(d.ID()) || other[ACLItem] == d || other[ACLItemArray].Element() != other[ACLItem] {
		t.Fatal("ACL scalar/array generation", d, a)
	}
	if !d.OutputSessionDependent() {
		t.Fatal("released aclitemout is STABLE")
	}
	plan, err := PlanCoercion(types[Text].Usage(), d.Usage(), Explicit)
	if err != nil || !plan.SessionDependent() {
		t.Fatal("released aclitemin is STABLE", err)
	}
	plan, err = PlanCoercion(d.Usage(), types[Text].Usage(), Explicit)
	if err != nil || !plan.RequiresCatalogOutput() {
		t.Fatal("ACL output lacks authority demand", err)
	}
	if _, err := d.BinaryInputWithEnvironment(context.Background(), make([]byte, 16), -1, InputEnvironment{}); err == nil {
		t.Fatal("ACL binary receive admitted")
	}
	v, _ := ACLItemValue(d, new(ACLAuthority), 0, 10, 2, 0)
	if _, err := d.AppendBinaryWithEnvironment(context.Background(), nil, v, OutputEnvironment{}); err == nil {
		t.Fatal("ACL binary send admitted")
	}
}

func TestACLItemConstructor(t *testing.T) {
	types := testTypes()
	authority := new(ACLAuthority)
	other := new(ACLAuthority)
	for _, tc := range []struct {
		d                      *Descriptor
		a                      *ACLAuthority
		grantee, grantor, p, o uint32
	}{
		{nil, authority, 0, 10, 0, 0}, {types[Text], authority, 0, 10, 0, 0}, {types[ACLItem], nil, 0, 10, 0, 0}, {types[ACLItem], authority, 1, 10, 1 << 15, 0}, {types[ACLItem], authority, 1, 10, 2, 1},
	} {
		if _, err := ACLItemValue(tc.d, tc.a, tc.grantee, tc.grantor, tc.p, tc.o); err == nil {
			t.Fatalf("invalid ACL constructor accepted: descriptor=%p authority=%p grantee=%d grantor=%d privileges=%#x options=%#x", tc.d, tc.a, tc.grantee, tc.grantor, tc.p, tc.o)
		}
	}
	value, err := ACLItemValue(types[ACLItem], authority, 0, 10, ACLPrivilegeMask, ACLPrivilegeMask)
	if err != nil {
		t.Fatal(err)
	}
	copy := value
	a, g, grantor, p, o, err := copy.ACLItem()
	if err != nil || a != authority || g != 0 || grantor != 10 || p != 32767 || o != 32767 || copy != value {
		t.Fatal("inline checked copy", a, g, grantor, p, o, err)
	}
	foreign, _ := ACLItemValue(types[ACLItem], other, 0, 10, p, o)
	if SameDatum(value, foreign) {
		t.Fatal("foreign authority confused with same IDs")
	}
	if _, _, _, _, _, err := Null(types[ACLItem]).ACLItem(); err == nil {
		t.Fatal("NULL admitted as PUBLIC")
	}
	// A real map key checks the comparable carrier without reflecting the large
	// Value type through the compiler's interface-type metadata path.
	if !map[Value]bool{value: true}[copy] {
		t.Fatal("ACL value copy lost comparable map-key identity")
	}
	if _, err := (Value{typ: types[ACLItem]}).Retag(types[ACLItem]); err == nil {
		t.Fatal("unchecked zero ACL retagged")
	}
	var inline Value
	var constructorError error
	if allocations := testing.AllocsPerRun(100, func() {
		inline, constructorError = ACLItemValue(types[ACLItem], authority, 0, 10, ACLPrivilegeMask, ACLPrivilegeMask)
	}); allocations != 0 {
		t.Fatal("scalar ACL constructor allocations", allocations)
	}
	if constructorError != nil || inline != value {
		t.Fatal("inline constructor result", constructorError)
	}
}

func TestACLItemCodec(t *testing.T) {
	types := testTypes()
	r := &aclCodecFixture{}
	input := InputEnvironment{ACLItem: r}
	output := OutputEnvironment{ACLItem: r, QuoteAllIdentifiers: true}
	for _, tc := range []struct {
		input, output string
		p, o          uint32
		warnings      int
	}{
		{"A=wr**/B", "A=r*w/B", 6, 2, 0}, {"A=wrraa**/B", "A=a*rw/B", 7, 1, 0}, {"A=*r**/B", "A=r*/B", 2, 2, 0}, {"=r*/B", "=r*/B", 2, 2, 0}, {"A=/B", "A=/B", 0, 0, 0}, {"user A=r/B", "A=r/B", 2, 0, 0}, {`""=r/B`, "=r/B", 2, 0, 0}, {`A=r/"B`, "A=r/B", 2, 0, 0}, {`A=r/"a ""quote\é"`, `A=r/"a ""quote\é"`, 2, 0, 0}, {"A=r \n", "A=r/postgres", 2, 0, 1},
	} {
		before := r.warnings
		value, err := types[ACLItem].InputWithEnvironment(context.Background(), tc.input, -1, input)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		_, _, _, p, o, err := value.ACLItem()
		if err != nil || p != tc.p || o != tc.o {
			t.Fatal("decoded masks", p, o, err)
		}
		text, err := types[ACLItem].OutputWithEnvironment(context.Background(), value, output)
		if err != nil || text != tc.output || r.warnings-before != tc.warnings {
			t.Fatal(tc.input, text, err, r.warnings-before, tc.output)
		}
	}
	for i := range len(ACLPrivilegeLetters) {
		bit := uint32(1) << i
		text := "A=" + string(ACLPrivilegeLetters[i]) + "*/B"
		value, err := types[ACLItem].InputWithEnvironment(context.Background(), text, -1, input)
		if err != nil {
			t.Fatal(err)
		}
		_, _, _, p, o, _ := value.ACLItem()
		if p != bit || o != bit {
			t.Fatal("released privilege bit", i, p, o, bit)
		}
	}
	for _, name := range []string{strings.Repeat("n", 63), strings.Repeat("é", 31)} {
		if _, err := types[ACLItem].InputWithEnvironment(context.Background(), name+"=r/B", -1, input); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		text, code string
		warn       bool
	}{
		{strings.Repeat("n", 64) + "=r/B", "42622", false}, {strings.Repeat("é", 32) + "=r/B", "42622", false}, {"missing=z/missing", "22P02", false}, {"missing=r/", "42704", false}, {"A=r/", "22P02", false}, {"A=r1/B", "22P02", true}, {"A=r /B", "22P02", true},
	} {
		before := r.warnings
		_, err := types[ACLItem].InputWithEnvironment(context.Background(), tc.text, -1, input)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code {
			t.Fatal(tc.text, err)
		}
		if (r.warnings > before) != tc.warn {
			t.Fatal("scanner warning precedence", tc.text, r.warnings-before)
		}
	}
	if _, err := types[ACLItem].Input(context.Background(), "A=r/B", -1); err == nil {
		t.Fatal("missing input authority accepted")
	}
	value, _ := ACLItemValue(types[ACLItem], &r.authority, 42, 77, 2, 0)
	if _, err := types[ACLItem].Output(context.Background(), value); err == nil {
		t.Fatal("missing output authority accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := types[ACLItem].InputWithEnvironment(ctx, "A=r/B", -1, input); !errors.Is(err, context.Canceled) {
		t.Fatal("input cancellation", err)
	}
}

func TestACLItemEquality(t *testing.T) {
	types := testTypes()
	d := types[ACLItem]
	authority := new(ACLAuthority)
	plan, err := BindComparisonPredicate(CompareEqual, d, d, types[Bool], CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SessionDependent() {
		t.Fatal("ACL equality must be immutable")
	}
	for i := range 15 {
		bit := uint32(1) << i
		a, _ := ACLItemValue(d, authority, 1, 2, bit, 0)
		for _, tc := range []struct {
			g, grantor, p, o uint32
			equal            bool
		}{{1, 2, bit, 0, true}, {2, 2, bit, 0, false}, {1, 3, bit, 0, false}, {1, 2, 0, 0, false}, {1, 2, bit, bit, false}} {
			b, _ := ACLItemValue(d, authority, tc.g, tc.grantor, tc.p, tc.o)
			value, err := plan.Evaluate(context.Background(), a, b, InputEnvironment{})
			equal, _ := value.Boolean()
			if err != nil || equal != tc.equal || SameDatum(a, b) != tc.equal {
				t.Fatal(i, tc, equal, err)
			}
		}
	}
	for _, operator := range []ComparisonOperator{CompareNotEqual, CompareLess, CompareLessEqual, CompareGreater, CompareGreaterEqual} {
		if _, err := BindComparisonPredicate(operator, d, d, types[Bool], CollationRef{}, false); err == nil {
			t.Fatal("scalar unsupported operator admitted", operator)
		}
	}
	null, err := plan.Evaluate(context.Background(), Null(d), Null(d), InputEnvironment{})
	if err != nil || !null.IsNull() {
		t.Fatal("strict NULL", err)
	}
	array := types[ACLItemArray]
	element, _ := ACLItemValue(d, authority, 0, 10, 2, 2)
	a, _ := OwnedArrayValue(array, []ArrayDimension{{Lower: 0, Length: 2}}, []Value{element, Null(d)})
	b, _ := OwnedArrayValue(array, []ArrayDimension{{Lower: 1, Length: 2}}, []Value{element, Null(d)})
	predicate, err := BindComparisonPredicate(CompareEqual, array, array, types[Bool], CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		right Value
		equal bool
	}{{a, true}, {b, false}} {
		result, err := predicate.Evaluate(context.Background(), a, tc.right, InputEnvironment{})
		got, _ := result.Boolean()
		if err != nil || got != tc.equal {
			t.Fatal("array bounds/NULL", got, err)
		}
	}
	order, err := BindComparisonPredicate(CompareLess, array, array, types[Bool], CollationRef{}, false)
	if err != nil {
		t.Fatal("array ordering procedure must resolve", err)
	}
	if _, err := order.Evaluate(context.Background(), a, b, InputEnvironment{}); err == nil {
		t.Fatal("ACL array element ordering invented")
	}
}
