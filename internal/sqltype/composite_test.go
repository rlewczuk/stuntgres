package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func compositeBuiltins() map[ID]*Descriptor {
	out := make(map[ID]*Descriptor)
	for _, d := range Builtins() {
		out[d.ID()] = d
	}
	return out
}
func compositeForTest(t *testing.T, id ID, members ...CompositeMember) *Descriptor {
	t.Helper()
	d, err := Composite(id, "record_type", "public.record_type", id+1, members)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func compositeInputForTest(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func compositeOutputForTest(t *testing.T, v Value) string {
	t.Helper()
	text, err := v.Type().Output(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestCompositeDescriptorOwnership(t *testing.T) {
	b := compositeBuiltins()
	members := []CompositeMember{{1, "a", b[Int4].Usage()}, {7, "b", b[Text].Usage()}}
	d := compositeForTest(t, 50000, members...)
	members[0] = CompositeMember{}
	if d.CompositeLen() != 2 || d.CompositeMember(0).Name != "a" || d.CompositeMember(1).Attribute != 7 {
		t.Fatal("borrowed member definitions")
	}
	member := d.CompositeMember(0)
	member.Name = "changed"
	if d.CompositeMember(0).Name != "a" {
		t.Fatal("mutable accessor")
	}
	if !d.IsComposite() || !d.IsUserDefined() || !d.Concrete() || d.DatabaseName() != "" || d.AssociatedArray() != 50001 || !d.Usage().Valid() || d.Usage().Collation != (CollationRef{}) {
		t.Fatal("descriptor metadata")
	}
	var nilDescriptor *Descriptor
	if nilDescriptor.IsComposite() || nilDescriptor.CompositeLen() != 0 {
		t.Fatal("nil descriptor")
	}
	foreign := compositeBuiltins()
	nested := compositeForTest(t, 50100, CompositeMember{1, "n", foreign[Int4].Usage()})
	cases := [][]CompositeMember{
		{{0, "a", b[Int4].Usage()}}, {{-1, "a", b[Int4].Usage()}},
		{{1, "", b[Int4].Usage()}}, {{1, strings.Repeat("x", 64), b[Int4].Usage()}},
		{{1, "a", Usage{}}}, {{1, "a", b[Unknown].Usage()}}, {{1, "a", b[Void].Usage()}},
		{{1, "a", b[Int4].Usage()}, {1, "b", b[Text].Usage()}},
		{{1, "a", b[Int4].Usage()}, {2, "a", b[Text].Usage()}},
		{{1, "a", b[Int4].Usage()}, {2, "b", foreign[Int4].Usage()}},
		{{1, "a", b[Int4].Usage()}, {2, "nested", nested.Usage()}},
		{{1, "a", LookupOnly(60000, "record", 0).Usage()}},
	}
	for i, members := range cases {
		if _, err := Composite(50000, "r", "r", 50001, members); err == nil {
			t.Fatalf("case %d admitted", i)
		}
	}
	for _, args := range []struct {
		id                ID
		name, declaration string
		array             ID
	}{{0, "r", "r", 1}, {Unknown, "r", "r", 1}, {50000, "", "r", 1}, {50000, "r", "", 1}, {50000, "r", "r", 50000}} {
		if _, err := Composite(args.id, args.name, args.declaration, args.array, nil); err == nil {
			t.Fatal("invalid descriptor identity admitted")
		}
	}
	limit := make([]CompositeMember, 1600)
	for i := range limit {
		limit[i] = CompositeMember{int32(i + 1), strings.Repeat("x", i/100+1) + string(rune(0x100+i)), b[Int4].Usage()}
	}
	if _, err := Composite(50000, "r", "r", 50001, limit); err != nil {
		t.Fatal(err)
	}
	limit = append(limit, CompositeMember{1601, "last", b[Int4].Usage()})
	if _, err := Composite(50000, "r", "r", 50001, limit); err == nil {
		t.Fatal("1601 members admitted")
	}
}

func TestCompositePayloadOwnership(t *testing.T) {
	b := compositeBuiltins()
	d := compositeForTest(t, 50000, CompositeMember{1, "a", b[Text].Usage()})
	fields := []Value{String(b[Text], "original")}
	v, err := CompositeValue(d, fields)
	if err != nil {
		t.Fatal(err)
	}
	fields[0] = Null(b[Text])
	if v.CompositeField(0).IsNull() || compositeOutputForTest(t, v) != "(original)" {
		t.Fatal("borrowed field buffer")
	}
	copyField := v.CompositeField(0)
	copyField.text = "mutated"
	if v.CompositeField(0).text != "original" {
		t.Fatal("mutable field accessor")
	}
	owned := []Value{Null(b[Text])}
	ownedValue, err := OwnedCompositeValue(d, owned)
	if err != nil || &ownedValue.composite.fields[0] != &owned[0] {
		t.Fatal("owned builder copied", err)
	}
	for _, fields := range [][]Value{nil, {Value{}}, {Null(compositeBuiltins()[Text])}, {Null(b[Text]), Null(b[Text])}} {
		if _, err := CompositeValue(d, fields); err == nil {
			t.Fatal("invalid fields admitted")
		}
	}
	empty := compositeForTest(t, 50100)
	zero := compositeInputForTest(t, empty, "()")
	one := compositeInputForTest(t, d, "()")
	if zero.IsNull() || one.IsNull() || zero.composite == nil || zero.CompositeLen() != 0 || one.CompositeLen() != 1 || !one.CompositeField(0).IsNull() || one.CompositeField(0).Type() != b[Text] {
		t.Fatal("NULL/empty payload collapsed")
	}
	if SameDatum(zero, Null(empty)) || SameDatum(zero, one) || SameDatum(Value{}, Value{}) {
		t.Fatal("invalid datum equivalence")
	}
	if (Value{}).CompositeLen() != 0 || Null(d).CompositeLen() != 0 {
		t.Fatal("null accessor")
	}
	if _, err := (Value{typ: d}).Retag(d); err == nil {
		t.Fatal("missing payload retagged")
	}
	if _, err := (Value{typ: d, composite: &compositePayload{fields: []Value{Null(compositeBuiltins()[Text])}}}).Retag(d); err == nil {
		t.Fatal("foreign payload retagged")
	}
}

func TestCompositeStableAttributeMapping(t *testing.T) {
	b := compositeBuiltins()
	d := compositeForTest(t, 50000, CompositeMember{1, "a", b[Text].Usage()}, CompositeMember{7, "b", b[Int4].Usage()})
	v := compositeInputForTest(t, d, "(keep,7)")
	identity := func(v Value, target *Descriptor) (Value, error) { return v.Retag(target) }
	same, err := v.MapComposite(d, identity)
	if err != nil || same != v {
		t.Fatal("identity mapping copied", err)
	}
	rename := compositeForTest(t, 50000, CompositeMember{1, "renamed", b[Text].Usage()}, CompositeMember{7, "b", b[Int4].Usage()})
	renamed, err := v.Retag(rename)
	if err != nil || renamed.composite != v.composite || renamed.Type() != rename {
		t.Fatal("rename copied payload", err)
	}
	target := compositeForTest(t, 50000, CompositeMember{7, "b", b[Int4].Usage()}, CompositeMember{9, "new", b[Text].Usage()})
	mapped, err := v.Retag(target)
	if err != nil {
		t.Fatal(err)
	}
	if compositeOutputForTest(t, mapped) != "(7,)" || !mapped.CompositeField(1).IsNull() || mapped.CompositeField(1).Type() != b[Text] || compositeOutputForTest(t, v) != "(keep,7)" {
		t.Fatal("offset mapping or source mutation")
	}
	empty := compositeForTest(t, 50000)
	dropped, err := v.Retag(empty)
	if err != nil || dropped.IsNull() || dropped.composite == nil || compositeOutputForTest(t, dropped) != "()" {
		t.Fatal("drop projection", err)
	}
	other := compositeForTest(t, 51000, CompositeMember{1, "a", b[Text].Usage()}, CompositeMember{7, "b", b[Int4].Usage()})
	if _, err := v.Retag(other); err == nil {
		t.Fatal("Retag changed logical ID")
	}
	imported, err := v.MapComposite(other, identity)
	if err != nil || imported.composite != v.composite {
		t.Fatal("authorized descriptor-only import copied", err)
	}
	failure := errors.New("child transformation")
	if _, err := v.MapComposite(d, func(Value, *Descriptor) (Value, error) { return Value{}, failure }); !errors.Is(err, failure) {
		t.Fatal("child error replaced", err)
	}
	if _, err := v.MapComposite(d, func(Value, *Descriptor) (Value, error) { return Null(compositeBuiltins()[Text]), nil }); err == nil {
		t.Fatal("foreign mapped generation admitted")
	}
	changed := compositeForTest(t, 50000, CompositeMember{1, "a", b[Int4].Usage()}, CompositeMember{7, "b", b[Int4].Usage()})
	if _, err := v.Retag(changed); err == nil {
		t.Fatal("Retag cast child type")
	}
	varchar := b[VarChar].Usage()
	varchar.Typmod = 6
	vd := compositeForTest(t, 52000, CompositeMember{1, "s", varchar})
	vv := compositeInputForTest(t, vd, "(ab)")
	varchar.Typmod = 7
	widened := compositeForTest(t, 52000, CompositeMember{1, "s", varchar})
	if _, err := vv.Retag(widened); err == nil {
		t.Fatal("Retag changed member sizing without authorization")
	}
	allocs := testing.AllocsPerRun(100, func() {
		out, err := v.MapComposite(d, identity)
		if err != nil || out != v {
			panic("identity")
		}
	})
	if allocs != 0 {
		t.Fatalf("identity mapping allocated %g", allocs)
	}
}

func TestCompositeRecursiveRetagAndAdmission(t *testing.T) {
	from, to := compositeBuiltins(), compositeBuiltins()
	build := func(b map[ID]*Descriptor) (*Descriptor, *Descriptor) {
		inner := compositeForTest(t, 50000, CompositeMember{1, "i", b[Int4].Usage()})
		domain, err := Domain(50100, "d", "d", inner.Usage())
		if err != nil {
			t.Fatal(err)
		}
		array, err := Array(50200, "ints", b[Int4])
		if err != nil {
			t.Fatal(err)
		}
		outer := compositeForTest(t, 50300, CompositeMember{1, "d", domain.Usage()}, CompositeMember{2, "a", array.Usage()}, CompositeMember{3, "null", b[Text].Usage()})
		return outer, domain
	}
	source, sourceDomain := build(from)
	target, targetDomain := build(to)
	v := compositeInputForTest(t, source, `("(7)","{1,NULL,2}",)`)
	next, err := v.Retag(target)
	if err != nil {
		t.Fatal(err)
	}
	field := next.CompositeField(0)
	if field.Type() != targetDomain || field.CompositeField(0).Type() != to[Int4] || next.CompositeField(1).ArrayElement(1).Type() != to[Int4] || next.CompositeField(2).Type() != to[Text] {
		t.Fatal("retained foreign descendants")
	}
	if v.CompositeField(0).Type() != sourceDomain || v.CompositeField(0).CompositeField(0).Type() != from[Int4] || compositeOutputForTest(t, next) != compositeOutputForTest(t, v) {
		t.Fatal("source mutated or datum lost")
	}
	for _, d := range []*Descriptor{sourceDomain, sourceDomain.BaseType()} {
		if _, err := Array(60000, "records", d); err != nil {
			t.Fatal("executable composite array rejected", err)
		}
	}
	lookup := LookupOnly(60000, "_d", 0)
	if lookup.Concrete() || lookup.Usage().Valid() {
		t.Fatal("lookup-only array admitted")
	}
	if _, err := CompositeValue(lookup, nil); err == nil {
		t.Fatal("lookup-only payload admitted")
	}
	if _, err := PlanCoercion(lookup.Usage(), lookup.Usage(), Assignment); err == nil {
		t.Fatal("lookup-only NULL coercion admitted")
	}
	ids := compositeForTest(t, 61000, CompositeMember{1, "rc", from[Regclass].Usage()}, CompositeMember{2, "rt", from[Regtype].Usage()})
	domain, err := Domain(61100, "ids_domain", "ids_domain", ids.Usage())
	if err != nil {
		t.Fatal(err)
	}
	outer := compositeForTest(t, 61200, CompositeMember{1, "nested", domain.Usage()})
	if !outer.ContainsRegclass() || !outer.ContainsRegtype() || !outer.ContainsCatalogIdentifier() || source.ContainsCatalogIdentifier() {
		t.Fatal("catalog demand traversal")
	}
}

func TestCompositeTextCoercionBoundaries(t *testing.T) {
	b := compositeBuiltins()
	d := compositeForTest(t, 50000, CompositeMember{1, "s", b[Text].Usage()})
	other := compositeForTest(t, 50100, CompositeMember{1, "s", b[Text].Usage()})
	v := compositeInputForTest(t, d, "(ab)")
	for _, mode := range []CoercionContext{Implicit, Assignment, Explicit} {
		if _, err := PlanCoercion(d.Usage(), other.Usage(), mode); err == nil {
			t.Fatal("structural cast admitted")
		}
		p, err := PlanCoercion(d.Usage(), d.Usage(), mode)
		if err != nil {
			t.Fatal(err)
		}
		next, err := p.Apply(context.Background(), v, InputEnvironment{}, OutputEnvironment{})
		if err != nil || next != v {
			t.Fatal("identity coercion changed datum", err)
		}
	}
	for _, mode := range []CoercionContext{Implicit, Assignment} {
		if _, err := PlanCoercion(b[Text].Usage(), d.Usage(), mode); err == nil {
			t.Fatal("typed TEXT implicitly admitted")
		}
	}
	p, err := PlanCoercion(b[Text].Usage(), d.Usage(), Explicit)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := p.Apply(context.Background(), String(b[Text], "(ab)"), InputEnvironment{}, OutputEnvironment{})
	if err != nil || !SameDatum(parsed, v) {
		t.Fatal("explicit text input", err)
	}
	for _, id := range []ID{Text, VarChar, BPChar} {
		u := b[id].Usage()
		if id != Text {
			u.Typmod = 12
		}
		p, err := PlanCoercion(d.Usage(), u, Assignment)
		if err != nil {
			t.Fatal(err)
		}
		text, err := p.Apply(context.Background(), v, InputEnvironment{}, OutputEnvironment{})
		if err != nil || !strings.HasPrefix(text.text, "(ab)") {
			t.Fatal("assignment output", err)
		}
	}
	short := b[VarChar].Usage()
	short.Typmod = 6
	p, err = PlanCoercion(d.Usage(), short, Assignment)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Apply(context.Background(), v, InputEnvironment{}, OutputEnvironment{})
	var sqlerr *sqlstate.Error
	if !errors.As(err, &sqlerr) || sqlerr.Code != "22001" {
		t.Fatal("destination sizing omitted", err)
	}
	literal, err := d.SQLLiteral(context.Background(), compositeInputForTest(t, d, "(O'Reilly)"))
	if err != nil || literal != "'(O''Reilly)'" {
		t.Fatal("SQL quote layer", literal, err)
	}
}

func TestCompositeAuthorizedCatalogRemint(t *testing.T) {
	from, to := compositeBuiltins(), compositeBuiltins()
	build := func(b map[ID]*Descriptor, id ID) *Descriptor {
		array, err := Array(id+10, "catalog_ids", b[Regtype])
		if err != nil {
			t.Fatal(err)
		}
		inner := compositeForTest(t, id, CompositeMember{1, "rc", b[Regclass].Usage()}, CompositeMember{7, "rt", array.Usage()})
		domain, err := Domain(id+20, "wrapped", "wrapped", inner.Usage())
		if err != nil {
			t.Fatal(err)
		}
		return compositeForTest(t, id+30, CompositeMember{1, "nested", domain.Usage()})
	}
	source, target := build(from, 50000), build(to, 60000)
	// This unit exercises authorized datum reminting, not catalog name input.
	// REGTYPE text input deliberately requires an engine-owned resolver, even
	// for numeric spelling. Construct the source through checked typed builders.
	if _, err := source.Input(context.Background(), `("(123,""{456,NULL}"")")`, -1); err == nil {
		t.Fatal("nested REGTYPE input admitted without catalog authority")
	}
	regtype, err := RegtypeValue(from[Regtype], 456)
	if err != nil {
		t.Fatal(err)
	}
	domain := source.CompositeMember(0).Usage.Type
	physical := domain.BaseType()
	array, err := ArrayValue(physical.CompositeMember(1).Usage.Type,
		[]ArrayDimension{{Lower: 1, Length: 2}}, []Value{regtype, Null(from[Regtype])})
	if err != nil {
		t.Fatal(err)
	}
	regclass, err := from[Regclass].Input(context.Background(), "123", -1)
	if err != nil {
		t.Fatal(err)
	}
	record, err := CompositeValue(physical, []Value{regclass, array})
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(record)
	if err != nil {
		t.Fatal(err)
	}
	v, err := CompositeValue(source, []Value{wrapped})
	if err != nil {
		t.Fatal(err)
	}
	var remap func(Value, *Descriptor) (Value, error)
	remap = func(old Value, d *Descriptor) (Value, error) {
		if old.IsNull() {
			return Null(d), nil
		}
		if d.IsDomain() {
			base, err := old.BaseValue()
			if err != nil {
				return Value{}, err
			}
			next, err := remap(base, d.BaseType())
			if err != nil {
				return Value{}, err
			}
			return d.Wrap(next)
		}
		if d.IsComposite() {
			return old.MapComposite(d, remap)
		}
		if d.IsArray() {
			return old.MapArray(d, remap)
		}
		if d.ID() == Regtype {
			id, err := old.TypeOID()
			if err != nil {
				return Value{}, err
			}
			return RegtypeValue(d, id+1000)
		}
		if d.ID() == Regclass {
			return d.Input(context.Background(), "1123", -1)
		}
		return old.Retag(d)
	}
	next, err := v.MapComposite(target, remap)
	if err != nil {
		t.Fatal(err)
	}
	inner := next.CompositeField(0)
	if inner.Type() != target.CompositeMember(0).Usage.Type || inner.CompositeField(0).Type() != to[Regclass] ||
		inner.CompositeField(1).Type() != target.CompositeMember(0).Usage.Type.BaseType().CompositeMember(1).Usage.Type {
		t.Fatal("catalog remint retained source descriptor")
	}
	id, err := inner.CompositeField(1).ArrayElement(0).TypeOID()
	if err != nil || id != 1456 || !inner.CompositeField(1).ArrayElement(1).IsNull() ||
		inner.CompositeField(1).ArrayElement(1).Type() != to[Regtype] || inner.CompositeField(0).text != "1123" {
		t.Fatal("catalog child transformation failed", id, err)
	}
	if compositeOutputForTest(t, v) != `("(123,""{456,NULL}"")")` {
		t.Fatal("catalog remap mutated captured source")
	}
}
