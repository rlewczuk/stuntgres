package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func citextUnitType(t *testing.T, id ID) *Descriptor {
	t.Helper()
	d, err := Citext(id, "citext", "public.citext")
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestCitextInputOutput(t *testing.T) {
	ctx := context.Background()
	d := citextUnitType(t, 45000)
	for _, text := range []string{"", "MiXeD", "  Straße İ Σς é🙂  ", "e\u0301", "O'Reilly\\path\nNext"} {
		v, err := d.Input(ctx, text, -1)
		if err != nil || v.Type() != d || v.IsNull() {
			t.Fatal(v, err)
		}
		if got, err := d.Output(ctx, v); err != nil || got != text {
			t.Fatal(got, err)
		}
		if got, err := v.Text(); err != nil || got != text {
			t.Fatal(got, err)
		}
		if got, err := d.SQLLiteral(ctx, v); err != nil || got != "'"+strings.ReplaceAll(text, "'", "''")+"'" {
			t.Fatal(got, err)
		}
	}
	for _, text := range []string{"\x00", "\xff", "\xc3\x28", "\xe2\x82"} {
		_, err := d.Input(ctx, text, -1)
		if state, ok := err.(interface{ SQLState() string }); !ok || state.SQLState() != "22021" {
			t.Fatal(text, err)
		}
	}
	if _, err := d.Input(ctx, "valid", 2); err == nil {
		t.Fatal("typmod admitted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.InputWithEnvironment(cancelled, "valid", -1, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if got, err := d.SQLLiteral(ctx, Null(d)); err != nil || got != "NULL" {
		t.Fatal(got, err)
	}
	for _, args := range []struct {
		id        ID
		name, sql string
	}{{0, "citext", "citext"}, {Unknown, "citext", "citext"}, {45000, "", "citext"}, {45000, "citext", ""}} {
		if _, err := Citext(args.id, args.name, args.sql); err == nil {
			t.Fatal("invalid identity admitted", args)
		}
	}
}

func TestCitextIdentity(t *testing.T) {
	d, generation := citextUnitType(t, 45000), citextUnitType(t, 45000)
	foreign, _ := QueryInt(45000, "citext", "public.citext")
	for _, v := range []Value{String(d, "MiXeD"), Null(d)} {
		if got, err := v.Retag(d); err != nil || got.Type() != d || got.IsNull() != v.IsNull() {
			t.Fatal(got, err)
		}
		for _, target := range []*Descriptor{generation, foreign, citextUnitType(t, 45001)} {
			if _, err := v.Retag(target); err == nil {
				t.Fatal("foreign family/generation retagged", target)
			}
		}
	}
	if _, err := Null(foreign).Retag(d); err == nil {
		t.Fatal("foreign NULL retagged")
	}
	if !d.IsCitext() || !d.IsUserDefined() || d.IsBuiltin() || !d.Concrete() || d.DatabaseName() != "CITEXT" {
		t.Fatal("scalar identity")
	}
	if _, err := BindComparison(d, d, d.Usage().Collation, false); err == nil {
		t.Fatal("advanced comparison authority invented")
	}
}

func TestCitextMetadata(t *testing.T) {
	d := citextUnitType(t, 45000)
	f, ok := d.PGTypeMetadata()
	if !ok || f.Kind != "b" || f.Category != "S" || f.Preferred || f.Length != -1 || f.ByValue || f.Alignment != "i" || f.Storage != "x" || f.Collation != 100 || f.Modifier != -1 || f.Input != 0 || f.Output != 0 || f.Receive != 0 || f.Send != 0 {
		t.Fatal(f, ok)
	}
	u := d.Usage()
	if !u.Valid() || u.Typmod != -1 || u.Collation.ID != 100 {
		t.Fatal(u)
	}
	for _, id := range []uint32{950, 951} {
		u.Collation = CollationRef{ID: id, Derivation: Default}
		if !u.Valid() {
			t.Fatal("supported declared collation rejected", u)
		}
	}
	registered, err := RegisteredCollation(46000, 950)
	if err != nil {
		t.Fatal(err)
	}
	u.Collation = registered
	if !u.Valid() {
		t.Fatal("registered collation rejected", u)
	}
	u.Collation = CollationRef{ID: 46000}
	if u.Valid() {
		t.Fatal("unregistered collation admitted")
	}
	u.Collation = registered
	u.Typmod = 6
	if u.Valid() {
		t.Fatal("sized citext usage admitted")
	}
	array := LookupArray(45001, "_citext", d)
	if array.Concrete() || array.Element() != d {
		t.Fatal("array storage authority invented")
	}
}

func TestCitextCoercion(t *testing.T) {
	ctx := context.Background()
	d := citextUnitType(t, 45000)
	builtins := map[ID]*Descriptor{}
	for _, typ := range Builtins() {
		builtins[typ.ID()] = typ
	}
	for _, tc := range []struct {
		from, to *Descriptor
		contexts [3]bool
	}{
		{d, d, [3]bool{true, true, true}},
		{d, citextUnitType(t, 45000), [3]bool{}},
		{builtins[Text], d, [3]bool{false, true, true}},
		{builtins[VarChar], d, [3]bool{false, true, true}},
		{builtins[BPChar], d, [3]bool{false, true, true}},
		{builtins[Bool], d, [3]bool{false, true, true}},
		{builtins[Inet], d, [3]bool{false, true, true}},
		{builtins[Int4], d, [3]bool{false, true, true}},
		{d, builtins[Text], [3]bool{true, true, true}},
		{d, builtins[VarChar], [3]bool{true, true, true}},
		{d, builtins[BPChar], [3]bool{false, true, true}},
		{d, builtins[Int4], [3]bool{false, false, true}},
		{d, builtins[Bool], [3]bool{false, false, true}},
	} {
		for i, allowed := range tc.contexts {
			p, err := PlanCoercion(tc.from.Usage(), tc.to.Usage(), CoercionContext(i))
			if (err == nil) != allowed {
				t.Fatalf("%s -> %s context %d: %v", tc.from.Name(), tc.to.Name(), i, err)
			}
			if allowed {
				v, err := p.Apply(ctx, Null(tc.from), InputEnvironment{}, OutputEnvironment{})
				if err != nil || !v.IsNull() || v.Type() != tc.to {
					t.Fatal("typed NULL", v, err)
				}
			}
		}
	}
	for _, tc := range []struct {
		id         ID
		text, want string
	}{
		{Text, "MiXeD  ", "MiXeD  "}, {VarChar, "MiXeD  ", "MiXeD  "}, {BPChar, "MiX     ", "MiX"},
		{Bool, "true", "true"}, {Bool, "false", "false"}, {Inet, "192.0.2.1/32", "192.0.2.1/32"},
		{Inet, "192.0.2.129/24", "192.0.2.129/24"}, {Inet, "2001:db8::1/128", "2001:db8::1/128"}, {Int4, "42", "42"},
	} {
		from := builtins[tc.id]
		v, err := from.Input(ctx, tc.text, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []CoercionContext{Assignment, Explicit} {
			p, err := PlanCoercion(from.Usage(), d.Usage(), mode)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			if text, err := d.Output(ctx, got); err != nil || text != tc.want {
				t.Fatal(tc, text, err)
			}
			if _, err := p.Apply(ctx, String(citextUnitType(t, 45000), "foreign"), InputEnvironment{}, OutputEnvironment{}); err == nil {
				t.Fatal("foreign input accepted")
			}
		}
	}
	for _, tc := range []struct {
		id   ID
		text string
		mod  int32
		want string
	}{
		{Int4, "42", -1, "42"}, {Text, "MiXeD  ", -1, "MiXeD  "}, {VarChar, "Longer", 7, "Lon"}, {BPChar, "MiX", 9, "MiX  "},
	} {
		to := builtins[tc.id].Usage()
		to.Typmod = tc.mod
		p, err := PlanCoercion(d.Usage(), to, Explicit)
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Apply(ctx, String(d, tc.text), InputEnvironment{}, OutputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if text, err := to.Type.Output(ctx, got); err != nil || text != tc.want {
			t.Fatal(tc, text, err)
		}
	}
	// The existing string binder consumes the outgoing implicit edge using
	// descriptor-aware admission; a dynamic numeric identity alone is not enough.
	text := builtins[Text].Usage()
	stringPlan, err := BindString("lower", text, text, []Usage{d.Usage()}, [4]bool{true}, false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := stringPlan.Evaluate(ctx, [4]Value{String(d, "MiXeD")}, InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := result.Text(); err != nil || got != "mixed" || result.Type() != text.Type {
		t.Fatal(got, err)
	}
	foreign, err := QueryInt(d.ID(), "citext", "public.citext")
	if err != nil {
		t.Fatal(err)
	}
	if !StringTextInput(d) || StringTextInput(foreign) || StringTextInput(nil) {
		t.Fatal("implicit text family authority")
	}
	for _, mode := range []CoercionContext{Implicit, Assignment, Explicit} {
		array := LookupArray(45001, "_citext", d)
		if _, err := PlanCoercion(d.Usage(), array.Usage(), mode); err == nil {
			t.Fatal("array cast admitted")
		}
	}
}

func TestCitextRemap(t *testing.T) {
	from, to := citextUnitType(t, 45000), citextUnitType(t, 45001)
	for _, v := range []Value{String(from, "MiXeD e\u0301"), Null(from)} {
		mapped, err := v.RemapCitext(to)
		if err != nil || mapped.Type() != to || mapped.IsNull() != v.IsNull() || mapped.text != v.text || v.Type() != from {
			t.Fatal(mapped, err)
		}
		foreign, _ := Cube(to.ID(), "citext", "public.citext")
		if _, err := v.RemapCitext(foreign); err == nil {
			t.Fatal("foreign family remapped")
		}
		if _, err := Null(foreign).RemapCitext(to); err == nil {
			t.Fatal("foreign source remapped")
		}
	}
}
