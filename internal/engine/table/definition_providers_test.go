package table

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// definitionStage is the staged half of a catalog stage: Get reads its map.
type definitionStage map[ids.ObjectAddress]catalog.Descriptor

func (s definitionStage) Create(catalog.Descriptor) error            { return nil }
func (s definitionStage) Replace(catalog.Descriptor) error           { return nil }
func (s definitionStage) UpdateDescriptor(catalog.Descriptor)        {}
func (s definitionStage) Get(a ids.ObjectAddress) catalog.Descriptor { return s[a] }
func (s definitionStage) LookupName(ids.NameKey) ids.ObjectAddress   { return ids.ObjectAddress{} }

const definitionEpoch ids.CatalogEpoch = 5

// definitionFixture is p, q and s in a TEMP namespace (old-epoch refs are
// valid only there) and r in an ordinary one; the view is the base catalog.
type definitionFixture struct {
	base        catalog.Read
	domain      *ids.Domain
	temp, plain ids.ObjectAddress
	p, q, s, r  *catalog.Relation
}

func definitionRelation(namespace ids.ObjectAddress, oid ids.OID) *catalog.Relation {
	return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: namespace, RelationKind: catalog.TableRelation}
}

func newDefinitionFixture() definitionFixture {
	f := definitionFixture{domain: &ids.Domain{}}
	f.temp = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 3000}
	f.plain = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	f.p, f.q, f.s = definitionRelation(f.temp, 100), definitionRelation(f.temp, 101), definitionRelation(f.temp, 102)
	f.r = definitionRelation(f.plain, 103)
	state := catalog.NewState(definitionEpoch)
	state.SetDescriptor(f.temp, &catalog.Namespace{ObjectAddress: f.temp, Temporary: f.domain.Session(1)})
	state.SetDescriptor(f.plain, &catalog.Namespace{ObjectAddress: f.plain})
	for _, d := range []*catalog.Relation{f.p, f.q, f.s, f.r} {
		state.SetDescriptor(d.Address(), d)
	}
	f.base = catalog.NewRead(&catalog.Owner{Domain: f.domain}, state)
	return f
}

func (f definitionFixture) ref(epoch ids.CatalogEpoch, d *catalog.Relation, sub int32) ids.ObjectRef {
	address := d.Address()
	address.SubID = sub
	return f.domain.Ref(epoch, address)
}

// The staged child c (new, in the TEMP namespace) inherits from p and
// depends on p, q (once at an old epoch), itself, a staged m and a type; s
// is a LIKE source.
func TestDefinitionProviders(t *testing.T) {
	f := newDefinitionFixture()
	c, m := definitionRelation(f.temp, 200), definitionRelation(f.temp, 201)
	c.Inheritance = &catalog.TableInheritance{Parents: []catalog.InheritanceParent{{Relation: f.p.Address(), Seqno: 1}}}
	c.Constraints.Check = []catalog.CheckConstraint{{Dependencies: []catalog.ExpressionDependency{
		{Ref: f.ref(definitionEpoch, f.p, 0)}, {Ref: f.ref(definitionEpoch-1, f.q, 0)}, {Ref: f.ref(definitionEpoch, m, 0)},
	}}}
	c.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{1: {Dependencies: []ids.ObjectRef{
		f.ref(definitionEpoch, f.q, 2), f.ref(definitionEpoch, c, 1),
		f.domain.Ref(definitionEpoch, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23}),
	}}}
	c.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Dependencies: []ids.ObjectRef{f.ref(definitionEpoch, f.p, 1)}}}
	staged := definitionStage{c.Address(): c, m.Address(): m}
	stage := &Stage{LikeSources: []catalog.Descriptor{f.s}}
	requests, retained, err := stage.DefinitionProviders(f.base, staged, maps.All(staged), f.domain)
	if err != nil {
		t.Fatal(err)
	}
	want := []DefinitionProviderRequest{
		{Ref: f.ref(definitionEpoch, f.p, 0), Mode: lock.ShareUpdateExclusive},
		{Ref: f.ref(definitionEpoch, f.q, 0), Mode: lock.AccessShare},
		{Ref: f.ref(definitionEpoch, f.s, 0), Mode: lock.AccessShare},
	}
	if !slices.Equal(requests, want) {
		t.Fatalf("requests %v, want %v", requests, want)
	}
	if len(retained) != 1 || retained[f.q.Address()] != f.q {
		t.Fatalf("retained %v, want only q", retained)
	}

	var none *Stage
	requests, retained, err = none.DefinitionProviders(f.base, definitionStage{}, maps.All(definitionStage{}), f.domain)
	if err != nil || len(requests) != 0 || retained != nil {
		t.Fatalf("empty stage: %v %v %v", requests, retained, err)
	}
}

func TestDefinitionProvidersInvalid(t *testing.T) {
	f := newDefinitionFixture()
	missing := definitionRelation(f.temp, 999)
	plain := definitionRelation(f.plain, 300)
	for _, tc := range []struct {
		name     string
		consumer *catalog.Relation
		ref      ids.ObjectRef
		want     string
	}{
		{"unstaged new provider", definitionRelation(f.temp, 200), f.ref(definitionEpoch, missing, 0), "invalid staged definition provider"},
		{"foreign domain", definitionRelation(f.temp, 200), (&ids.Domain{}).Ref(definitionEpoch, f.p.Address()), "invalid retained definition provider"},
		{"old epoch outside TEMP", plain, f.ref(definitionEpoch-1, f.r, 0), "invalid retained definition provider"},
	} {
		tc.consumer.Constraints.Check = []catalog.CheckConstraint{{Dependencies: []catalog.ExpressionDependency{{Ref: tc.ref}}}}
		staged := definitionStage{tc.consumer.Address(): tc.consumer}
		_, _, err := (&Stage{}).DefinitionProviders(f.base, staged, maps.All(staged), f.domain)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want %q", tc.name, err, tc.want)
		}
	}
}
