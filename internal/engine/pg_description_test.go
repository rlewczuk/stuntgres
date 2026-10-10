package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgDescriptionUnitSource(t *testing.T, e *Engine) (*relationDescriptor, ObjectRef) {
	t.Helper()
	address := ObjectAddress{ClassID: RelationClassID, ObjectID: 2609}
	d, ok := e.state.descriptors[address].(*relationDescriptor)
	if !ok {
		t.Fatal("missing catalog descriptor")
	}
	return d, ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: address}
}

func pgDescriptionUnitBag(t *testing.T, source *querySource) map[ObjectAddress]string {
	t.Helper()
	bag := make(map[ObjectAddress]string)
	for _, row := range source.anonymous {
		if len(row) != 4 {
			t.Fatal("description width", row)
		}
		object, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		class, err := row[1].OID()
		if err != nil {
			t.Fatal(err)
		}
		sub, err := row[2].Integer()
		if err != nil {
			t.Fatal(err)
		}
		text, err := row[3].Text()
		if err != nil || text == "" {
			t.Fatal("description payload", text, err)
		}
		key := ObjectAddress{ClassID: OID(class), ObjectID: OID(object), SubID: int32(sub)}
		if _, exists := bag[key]; exists {
			t.Fatal("duplicate address", key)
		}
		bag[key] = text
	}
	return bag
}

func TestPGDescriptionShape(t *testing.T) {
	e := New()
	d, _ := pgDescriptionUnitSource(t, e)
	if d.name != "pg_description" || d.namespace.ObjectID != 11 || d.relationKind != catalogRelation || d.attributeHighWater != 4 || len(d.columns) != 4 {
		t.Fatal(d)
	}
	for i, tc := range []struct {
		name      string
		typ       sqltype.ID
		collation uint32
	}{{"objoid", sqltype.Oid, 0}, {"classoid", sqltype.Oid, 0}, {"objsubid", sqltype.Int4, 0}, {"description", sqltype.Text, 950}} {
		c := d.columns[i]
		if c.Name != tc.name || c.Usage.Type.ID() != tc.typ || c.Usage.Typmod != -1 || c.Usage.Collation.ID != tc.collation || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2609 || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) {
			t.Fatal("exact descriptor", i, c)
		}
	}
	if _, exists := d.index["oid"]; exists {
		t.Fatal("fabricated row oid")
	}
	if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal("virtual catalog metadata must validate without physical constraints", err)
	}
	physical := *d
	physical.relationKind = tableRelation
	if err := (relationProvider{}).Validate(e.CatalogRead(), &physical); err == nil {
		t.Fatal("physical table NOT NULL metadata requires constraint identities")
	}
	foreignType, _ := New().CatalogRead().LookupType(sqltype.Oid)
	for _, tc := range []struct {
		name       string
		invalidate func(*relationDescriptor)
	}{
		{"identity", func(r *relationDescriptor) { r.address.ObjectID = 0 }},
		{"type", func(r *relationDescriptor) { r.columns[0].Usage.Type = foreignType }},
		{"live attribute", func(r *relationDescriptor) { r.attributes[0] = 0 }},
		{"constraint record", func(r *relationDescriptor) { r.notNullConstraints = []notNullConstraint{{Attribute: 1}} }},
		{"column comment", func(r *relationDescriptor) { r.columnComments = map[int32]string{99: "dead attribute"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := *d
			invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
			invalid.attributes = append([]int32(nil), d.attributes...)
			tc.invalidate(&invalid)
			if err := (relationProvider{}).Validate(e.CatalogRead(), &invalid); err == nil {
				t.Fatal("virtual-source exemption bypassed descriptor validation")
			}
		})
	}
	class := e.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: 1259}].(*relationDescriptor)
	rows, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), ObjectRef{}, class)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows.anonymous {
		oid, _ := row[0].OID()
		if oid == 2609 {
			name, _ := row[1].Text()
			namespace, _ := row[2].OID()
			kind, _ := row[4].Text()
			natts, _ := row[5].Integer()
			if name != "pg_description" || namespace != 11 || kind != "r" || natts != 4 {
				t.Fatal(row)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("pg_class did not discover catalog relation")
	}
}

func TestPGDescriptionStableKeysAndSnapshots(t *testing.T) {
	e := New()
	source, ref := pgDescriptionUnitSource(t, e)
	oldState := *e.state
	oldState.descriptors = maps.Clone(e.state.descriptors)
	namespace := ObjectAddress{ClassID: NamespaceClassID, ObjectID: 20000}
	relation := ObjectAddress{ClassID: RelationClassID, ObjectID: 20000}
	d := &relationDescriptor{address: relation, attributes: []int32{1, 3}, comment: "whole", columnComments: map[int32]string{1: "", 2: "stale dropped", 3: "third"}}
	oldState.descriptors[namespace] = &namespaceDescriptor{address: namespace, comment: "namespace"}
	oldState.descriptors[relation] = d
	old := CatalogRead{owner: e, state: &oldState}
	captured, err := capturePGDescription(context.Background(), old, ref, source)
	if err != nil {
		t.Fatal(err)
	}
	bag := pgDescriptionUnitBag(t, captured)
	want := map[ObjectAddress]string{namespace: "namespace", relation: "whole", {ClassID: RelationClassID, ObjectID: 20000, SubID: 3}: "third"}
	for key, text := range want {
		if bag[key] != text {
			t.Fatal(key, bag[key], text)
		}
	}
	if _, ok := bag[ObjectAddress{ClassID: RelationClassID, ObjectID: 20000, SubID: 2}]; ok {
		t.Fatal("stale map entry projected")
	}
	if _, ok := bag[ObjectAddress{ClassID: RelationClassID, ObjectID: 20000, SubID: 1}]; ok {
		t.Fatal("empty row projected")
	}
	if !reflect.DeepEqual(d.attributes, []int32{1, 3}) || d.columnComments[2] != "stale dropped" {
		t.Fatal("capture mutated descriptor")
	}
	newState := oldState
	newState.descriptors = maps.Clone(oldState.descriptors)
	updated := *d
	updated.comment = "replacement"
	updated.columnComments = map[int32]string{3: "replacement third"}
	newState.descriptors[relation] = &updated
	newRows, err := capturePGDescription(context.Background(), CatalogRead{owner: e, state: &newState}, ref, source)
	if err != nil || pgDescriptionUnitBag(t, newRows)[relation] != "replacement" {
		t.Fatal(err)
	}
	delete(newState.descriptors, relation)
	if pgDescriptionUnitBag(t, captured)[relation] != "whole" || descriptorComment(oldState.descriptors[relation], 3) != "third" {
		t.Fatal("retained snapshot changed")
	}
	// Simulate a nested provider relocking after publication: opening the virtual
	// source must use the statement frontier rather than the newly sampled state.
	e.state = &newState
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope := &StatementScope{op: op, owner: op.session.transactions, commentCatalog: old}
	e.mu.Lock()
	frozen, err := e.openRelationSourceLocked(scope, ref, e.roles.names["postgres"], queryRowMark{}, nil)
	if err != nil || pgDescriptionUnitBag(t, frozen)[relation] != "whole" {
		t.Fatal("nested frontier", err)
	}
}

func TestPGDescriptionCanceledCapture(t *testing.T) {
	e := New()
	source, ref := pgDescriptionUnitSource(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, state := range []*catalogState{e.state, {descriptors: map[ObjectAddress]Descriptor{}}} {
		captured, err := capturePGDescription(ctx, CatalogRead{owner: e, state: state}, ref, source)
		if captured != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled source", captured, err)
		}
	}
}

func TestPGDescriptionBuiltinSeeds(t *testing.T) {
	e := New()
	source, ref := pgDescriptionUnitSource(t, e)
	captured, err := capturePGDescription(context.Background(), e.CatalogRead(), ref, source)
	if err != nil {
		t.Fatal(err)
	}
	bag := pgDescriptionUnitBag(t, captured)
	expected := map[ObjectAddress]string{
		{ClassID: NamespaceClassID, ObjectID: 11}:   "system catalog schema",
		{ClassID: NamespaceClassID, ObjectID: 2200}: "standard public schema",
	}
	for _, group := range []struct {
		class OID
		seeds []pgDescriptionExpectedSeed
	}{
		{TypeClassID, pgDescriptionExpectedTypes},
		{CollationClassID, pgDescriptionExpectedCollations},
		{ConversionClassID, pgDescriptionExpectedConversions},
	} {
		for _, seed := range group.seeds {
			expected[ObjectAddress{ClassID: group.class, ObjectID: seed.oid}] = seed.text
		}
	}
	if len(bag) != 164 || !reflect.DeepEqual(bag, expected) {
		t.Fatal("complete independent baseline", bag, expected)
	}
	for _, oid := range []OID{4401, 4450, 4451, 4532} {
		if conversionDescription(oid) != "" {
			t.Fatal("conversion hole or boundary", oid)
		}
	}
	for _, descriptor := range e.state.descriptors {
		switch descriptor.(type) {
		case *collationDescriptor, *conversionDescriptor:
			if descriptorComment(descriptor, 0) != "" || catalogDescription(descriptor, 1) != "" {
				t.Fatal("projection widened inquiry/subobject boundary", descriptor)
			}
		}
	}
	if _, exists := bag[ObjectAddress{ClassID: TypeClassID, ObjectID: 1003}]; exists {
		t.Fatal("array inherited description")
	}
}
