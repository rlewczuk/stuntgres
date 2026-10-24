package table

import (
	"errors"
	"iter"
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
)

const preparationEpoch ids.CatalogEpoch = 7

type preparationFixture struct {
	domain     *ids.Domain
	a, b, c, e *catalog.Relation
	base       catalog.Read
}

func preparationRelation(oid ids.OID) *catalog.Relation {
	return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, RelationKind: catalog.TableRelation}
}

func newPreparationFixture() preparationFixture {
	f := preparationFixture{domain: &ids.Domain{}}
	f.a, f.b, f.c, f.e = preparationRelation(100), preparationRelation(101), preparationRelation(102), preparationRelation(103)
	f.base = f.read(preparationEpoch, f.a, f.b, f.c, f.e)
	return f
}

func (f preparationFixture) read(epoch ids.CatalogEpoch, descriptors ...catalog.Descriptor) catalog.Read {
	state := catalog.NewState(epoch)
	for _, d := range descriptors {
		state.SetDescriptor(d.Address(), d)
	}
	return catalog.NewRead(&catalog.Owner{Domain: f.domain}, state)
}

// effectKinds names each effect's kind in order.
func effectKinds(effects []DropEffect) []string {
	var kinds []string
	for _, effect := range effects {
		switch {
		case effect.Descriptor != nil:
			kinds = append(kinds, "replace")
		case effect.Remove.Address != (ids.ObjectAddress{}):
			kinds = append(kinds, "remove")
		case effect.Before != nil:
			kinds = append(kinds, "project")
		case effect.Notice != "":
			kinds = append(kinds, "notice:"+effect.Notice)
		case effect.Layout != nil:
			kinds = append(kinds, "layout")
		}
	}
	return kinds
}

// The log keeps the provider's effect order. Each address reads its latest
// descriptor effect and, independently, its latest layout; a removal stays.
func TestDropPreparationEffectOrder(t *testing.T) {
	f := newPreparationFixture()
	a1, a2, b2, c2 := preparationRelation(100), preparationRelation(100), preparationRelation(101), preparationRelation(102)
	lineage := index.LayoutLineage{Source: f.a, Target: a2}
	d := &DropPreparation{Base: f.base, Discovery: f.base}
	d.RecordEffect(DropEffect{Descriptor: a1})
	d.RecordEffect(DropEffect{Notice: "n"})
	d.RecordEffect(DropEffect{Index: f.a.Address(), Layout: &lineage})
	d.RecordEffect(DropEffect{Descriptor: a2})
	d.RecordEffect(DropEffect{Remove: f.domain.Ref(preparationEpoch, f.b.Address())})
	d.RecordEffect(DropEffect{Descriptor: b2})
	d.RecordEffect(DropEffect{Before: f.c, After: c2})

	if got, want := effectKinds(d.Effects()), []string{"replace", "notice:n", "layout", "replace", "remove", "replace", "project"}; !slices.Equal(got, want) {
		t.Fatalf("effects = %v, want %v", got, want)
	}
	if descriptor, present, handled := d.DefinitionDelta(f.a.Address()); descriptor != a2 || !present || !handled {
		t.Fatalf("a delta = %v %v %v, want the latest replacement", descriptor, present, handled)
	}
	if layout, present := d.LayoutDelta(f.a.Address()); !present || layout.Target != a2 {
		t.Fatalf("a layout = %v %v, want the recorded lineage", layout, present)
	}
	if descriptor, present, handled := d.DefinitionDelta(f.b.Address()); descriptor != nil || present || !handled {
		t.Fatalf("b delta = %v %v %v, want removed", descriptor, present, handled)
	}
	if descriptor, present, handled := d.DefinitionDelta(f.c.Address()); descriptor != c2 || !present || !handled {
		t.Fatalf("c delta = %v %v %v, want the projected shape", descriptor, present, handled)
	}
	if _, _, handled := d.DefinitionDelta(f.e.Address()); handled {
		t.Fatal("an untouched address must not be handled")
	}
	if _, present := d.LayoutDelta(f.b.Address()); present {
		t.Fatal("b has no layout effect")
	}
}

// Notice positions index effects: a rewind keeps the notices before the
// position and every later non-notice effect, in order, and reindexes them.
func TestDropPreparationRewindNotices(t *testing.T) {
	f := newPreparationFixture()
	a2, b2 := preparationRelation(100), preparationRelation(101)
	lineage := index.LayoutLineage{Source: f.c}
	d := &DropPreparation{Base: f.base, Discovery: f.base}
	d.RecordEffect(DropEffect{Notice: "n0"})
	d.RecordEffect(DropEffect{Descriptor: a2})
	position := d.NoticePosition()
	d.RecordEffect(DropEffect{Notice: "n1"})
	d.RecordEffect(DropEffect{Descriptor: b2})
	d.RecordEffect(DropEffect{Notice: "n2"})
	d.RecordEffect(DropEffect{Index: f.c.Address(), Layout: &lineage})
	if position != 2 {
		t.Fatalf("position = %d, want 2", position)
	}
	d.RewindNotices(position)

	if got, want := effectKinds(d.Effects()), []string{"notice:n0", "replace", "replace", "layout"}; !slices.Equal(got, want) {
		t.Fatalf("effects = %v, want %v", got, want)
	}
	if descriptor, present, _ := d.DefinitionDelta(f.b.Address()); descriptor != b2 || !present {
		t.Fatalf("b delta = %v %v, want its reindexed replacement", descriptor, present)
	}
	if layout, present := d.LayoutDelta(f.c.Address()); !present || layout.Source != f.c {
		t.Fatalf("c layout = %v %v, want its reindexed lineage", layout, present)
	}
	if d.NoticePosition() != 4 {
		t.Fatalf("position after rewind = %d, want 4", d.NoticePosition())
	}
}

// Observe captures each address once, from Base when it is there. The fence
// rejects a changed observation, another statement, transaction, epoch or
// domain, and ignores additive members.
func TestDropPreparationObservations(t *testing.T) {
	f := newPreparationFixture()
	statement, other := new(int), new(int)
	tx := f.domain.Tx(ids.SessionToken{}, 1, preparationEpoch, nil, 0)
	otherTx := f.domain.Tx(ids.SessionToken{}, 2, preparationEpoch, nil, 0)
	replaced, added, extra := preparationRelation(100), preparationRelation(200), preparationRelation(201)
	current := f.read(preparationEpoch, replaced, f.b, added)
	d := &DropPreparation{Ref: f.domain.Ref(preparationEpoch, f.a.Address()), Statement: statement, TxRef: tx, Base: f.base}
	d.Observe(f.a.Address(), current)
	d.Observe(added.Address(), current)
	d.Observe(f.a.Address(), f.read(preparationEpoch))

	same := f.read(preparationEpoch, f.a, added, extra)
	if err := d.ValidateObservations(statement, tx, same); err != nil {
		t.Fatalf("unchanged observations: %v", err)
	}
	for name, check := range map[string]func() error{
		"changed":     func() error { return d.ValidateObservations(statement, tx, current) },
		"statement":   func() error { return d.ValidateObservations(other, tx, same) },
		"transaction": func() error { return d.ValidateObservations(statement, otherTx, same) },
		"epoch":       func() error { return d.ValidateObservations(statement, tx, f.read(preparationEpoch+1, f.a, added)) },
		"domain": func() error {
			return d.ValidateObservations(statement, tx, catalog.NewRead(&catalog.Owner{Domain: &ids.Domain{}}, catalog.NewState(preparationEpoch)))
		},
	} {
		if err := check(); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatalf("%s: err = %v, want a replaced catalog", name, err)
		}
	}
	if !d.CaptureObservation(f.a.Address(), f.a) || d.CaptureObservation(f.a.Address(), replaced) {
		t.Fatal("CaptureObservation must accept the same observation and reject a different one")
	}
	if err := d.ValidateObservations(statement, tx, same); err != nil {
		t.Fatalf("a rejected capture replaced the observation: %v", err)
	}
}

// fakeDefinitionInput is a definition input over a fixed descriptor map.
type fakeDefinitionInput map[ids.ObjectAddress]catalog.Descriptor

func (in fakeDefinitionInput) Descriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	d, present := in[address]
	return d, present
}
func (in fakeDefinitionInput) Lookup(ids.NameKey) ids.ObjectAddress { return ids.ObjectAddress{} }
func (in fakeDefinitionInput) Descriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
	return maps.All(in)
}
func (in fakeDefinitionInput) ProceduresNamed(ids.ObjectAddress, string) iter.Seq[*catalog.Procedure] {
	return func(func(*catalog.Procedure) bool) {}
}
func (in fakeDefinitionInput) HasDynamicProcedureName(string) bool      { return false }
func (in fakeDefinitionInput) HasProviderProcedureOverride(string) bool { return false }

// The view reads through the whole Prior chain, the identity source and the
// definition input; a bare view reads Base alone.
func TestDropPreparationView(t *testing.T) {
	f := newPreparationFixture()
	a2, c2, sequence := preparationRelation(100), preparationRelation(102), preparationRelation(300)
	lineage := index.LayoutLineage{Source: f.c}
	prior := &DropPreparation{Base: f.base, Discovery: f.base}
	prior.RecordEffect(DropEffect{Descriptor: a2})
	prior.RecordEffect(DropEffect{Remove: f.domain.Ref(preparationEpoch, f.b.Address())})
	prior.RecordEffect(DropEffect{Index: f.c.Address(), Layout: &lineage})
	d := &DropPreparation{Base: f.base, Discovery: f.base, Prior: prior}
	d.RecordEffect(DropEffect{Descriptor: c2})
	v := DropPreparationView{Base: f.base, Preparation: d, IdentitySource: sequence}

	if v.Replacement(f.a.Address()) != a2 || v.Replacement(f.c.Address()) != c2 || v.Replacement(f.b.Address()) != nil || v.Replacement(f.e.Address()) != nil {
		t.Fatal("Replacement must read the chain's latest effects")
	}
	if !v.Removed(f.b.Address()) || v.Removed(f.a.Address()) {
		t.Fatal("Removed must read the prior removal only")
	}
	if layout, present := v.IndexLayout(f.c.Address()); !present || layout.Source != f.c {
		t.Fatal("IndexLayout must read the prior layout")
	}
	if v.Replacement(sequence.Address()) != sequence || v.InputDescriptor(sequence.Address()) != sequence {
		t.Fatal("the identity source must replace and input its address")
	}
	if got, present := d.Descriptor(f.a.Address()); got != a2 || !present {
		t.Fatal("Descriptor must read the prior replacement")
	}
	if _, present := d.Descriptor(f.b.Address()); present {
		t.Fatal("Descriptor must hide the prior removal")
	}
	if got, present := d.Descriptor(f.e.Address()); got != f.e || !present {
		t.Fatal("Descriptor must read an untouched base descriptor")
	}

	bare := DropPreparationView{Base: f.base}
	if bare.Replacement(f.a.Address()) != nil || bare.Removed(f.a.Address()) || bare.InputDescriptor(f.a.Address()) != f.a || bare.DescriptorCount() != 4 {
		t.Fatal("a bare view must read Base alone")
	}

	projected, added := preparationRelation(101), preparationRelation(400)
	input := fakeDefinitionInput{f.a.Address(): f.a, projected.Address(): projected, added.Address(): added, f.e.Address(): f.e}
	withInput := DropPreparationView{Base: f.base, Preparation: &DropPreparation{Base: f.base, Discovery: f.base, Input: input}}
	if withInput.Replacement(f.b.Address()) != projected || withInput.Replacement(f.a.Address()) != nil || withInput.Replacement(added.Address()) != added {
		t.Fatal("Replacement must read the input's changed and added descriptors")
	}
	if !withInput.Removed(f.c.Address()) || withInput.Removed(added.Address()) {
		t.Fatal("Removed must report base descriptors the input lacks")
	}
	seen := maps.Collect(withInput.Descriptors())
	if len(seen) != 5 || seen[f.b.Address()] != projected || seen[added.Address()] != added || withInput.DescriptorCount() != 5 {
		t.Fatalf("Descriptors = %v, want the projected base and the addition", seen)
	}
	if withInput.InputDescriptor(f.c.Address()) != nil {
		t.Fatal("InputDescriptor must read an input-removed base descriptor as nil")
	}

	// DescriptorsOver reads the base at each step: once an admission adds
	// the input's addition to the base, it is no longer an addition.
	current := f.base
	count := 0
	for address := range withInput.DescriptorsOver(func() catalog.Read { return current }) {
		current = f.read(preparationEpoch, f.a, f.b, f.c, f.e, added)
		if address == added.Address() {
			t.Fatal("DescriptorsOver yielded a descriptor the current base holds")
		}
		count++
	}
	if count != 4 {
		t.Fatalf("DescriptorsOver yielded %d descriptors, want the 4 base ones", count)
	}
}
