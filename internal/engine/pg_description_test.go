package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgDescriptionUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2609}
	d, ok := e.catalog.state.Relation(address)
	if !ok {
		t.Fatal("missing catalog descriptor")
	}
	return d, e.catalog.Domain.Ref(e.catalog.state.Epoch(), address)
}

func pgDescriptionUnitBag(t *testing.T, source *querySource) map[ids.ObjectAddress]string {
	t.Helper()
	bag := make(map[ids.ObjectAddress]string)
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
		key := ids.ObjectAddress{ClassID: ids.OID(class), ObjectID: ids.OID(object), SubID: int32(sub)}
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
	if d.Name() != "pg_description" || d.Namespace().ObjectID != 11 || d.RelationKind != catalog.CatalogRelation || d.ColumnMeta.HighWater != 4 || len(d.Columns) != 4 {
		t.Fatal(d)
	}
	for i, tc := range []struct {
		name      string
		typ       sqltype.ID
		collation uint32
	}{{"objoid", sqltype.Oid, 0}, {"classoid", sqltype.Oid, 0}, {"objsubid", sqltype.Int4, 0}, {"description", sqltype.Text, 950}} {
		c := d.Columns[i]
		if c.Name != tc.name || c.Usage.Type.ID() != tc.typ || c.Usage.Typmod != -1 || c.Usage.Collation.ID != tc.collation || c.Nullable || !c.NullabilityKnown || c.OriginTable != 2609 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) {
			t.Fatal("exact descriptor", i, c)
		}
	}
	if _, exists := d.Index["oid"]; exists {
		t.Fatal("fabricated row oid")
	}
	if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
		t.Fatal("virtual catalog metadata must validate without physical constraints", err)
	}
	physical := *d
	physical.RelationKind = catalog.TableRelation
	if err := (relationProvider{}).Validate(e.CatalogRead(), &physical); err == nil {
		t.Fatal("physical table NOT NULL metadata requires constraint identities")
	}
	foreignType, _ := New().CatalogRead().LookupType(sqltype.Oid)
	for _, tc := range []struct {
		name       string
		invalidate func(*catalog.Relation)
	}{
		{"identity", func(r *catalog.Relation) { r.ObjectAddress.ObjectID = 0 }},
		{"type", func(r *catalog.Relation) { r.Columns[0].Usage.Type = foreignType }},
		{"live attribute", func(r *catalog.Relation) { r.Attributes[0] = 0 }},
		{"constraint record", func(r *catalog.Relation) { r.Constraints.NotNull = []catalog.NotNullConstraint{{Attribute: 1}} }},
		{"column comment", func(r *catalog.Relation) { r.ColumnMeta.Comments = map[int32]string{99: "dead attribute"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := *d
			invalid.Columns = append([]catalog.Column(nil), d.Columns...)
			invalid.Attributes = append([]int32(nil), d.Attributes...)
			tc.invalidate(&invalid)
			if err := (relationProvider{}).Validate(e.CatalogRead(), &invalid); err == nil {
				t.Fatal("virtual-source exemption bypassed descriptor validation")
			}
		})
	}
	class := e.catalog.state.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259})
	rows, err := e.captureCatalogSourceLocked(context.Background(), e.CatalogRead(), e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published), ids.ObjectRef{}, class)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows.anonymous {
		oid, _ := row[0].OID()
		if oid == 2609 {
			name, _ := row[1].Text()
			namespace, _ := row[2].OID()
			kind, _ := row[class.Index["relkind"]].Text()
			natts, _ := row[class.Index["relnatts"]].Integer()
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
	oldState := *e.catalog.state
	oldState.CloneDescriptors()
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20000}
	relation := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
	d := &catalog.Relation{ObjectAddress: relation, Attributes: []int32{1, 3}, Comment: "whole", ColumnMeta: catalog.ColumnMetadata{Comments: map[int32]string{1: "", 2: "stale dropped", 3: "third"}}}
	oldState.SetDescriptor(namespace, &catalog.Namespace{ObjectAddress: namespace, Comment: "namespace"})
	oldState.SetDescriptor(relation, d)
	old := e.catalog.read(&oldState)
	captured, err := captureCatalogSourceRows(catalogSourceInput{Context: context.Background(), Catalog: old, Ref: ref, Source: source, Owner: (old).Owner()}, capturePGDescription)
	if err != nil {
		t.Fatal(err)
	}
	bag := pgDescriptionUnitBag(t, captured)
	want := map[ids.ObjectAddress]string{namespace: "namespace", relation: "whole", {ClassID: ids.RelationClassID, ObjectID: 20000, SubID: 3}: "third"}
	for key, text := range want {
		if bag[key] != text {
			t.Fatal(key, bag[key], text)
		}
	}
	if _, ok := bag[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000, SubID: 2}]; ok {
		t.Fatal("stale map entry projected")
	}
	if _, ok := bag[ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000, SubID: 1}]; ok {
		t.Fatal("empty row projected")
	}
	if !reflect.DeepEqual(d.Attributes, []int32{1, 3}) || d.ColumnMeta.Comments[2] != "stale dropped" {
		t.Fatal("capture mutated descriptor")
	}
	newState := oldState
	newState.CloneDescriptors()
	updated := *d
	updated.Comment = "replacement"
	updated.ColumnMeta.Comments = map[int32]string{3: "replacement third"}
	newState.SetDescriptor(relation, &updated)
	newRows, err := captureCatalogSourceRows(catalogSourceInput{Context: context.Background(), Catalog: e.catalog.read(&newState), Ref: ref, Source: source, Owner: (e.catalog.read(&newState)).Owner()}, capturePGDescription)
	if err != nil || pgDescriptionUnitBag(t, newRows)[relation] != "replacement" {
		t.Fatal(err)
	}
	newState.RemoveDescriptor(relation)
	if pgDescriptionUnitBag(t, captured)[relation] != "whole" || descriptorComment(oldState.Get(relation), 3) != "third" {
		t.Fatal("retained snapshot changed")
	}
	// Simulate a nested provider relocking after publication: opening the virtual
	// source must use the statement frontier rather than the newly sampled state.
	e.catalog.state = &newState
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope := &StatementScope{op: op, owner: op.session.transactions, commentCatalog: old}
	e.mu.Lock()
	frozen, err := e.openRelationSourceLocked(scope, ref, e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), queryRowMark{}, nil, nil)
	if err != nil || pgDescriptionUnitBag(t, frozen)[relation] != "whole" {
		t.Fatal("nested frontier", err)
	}
}

func TestPGDescriptionCanceledCapture(t *testing.T) {
	e := New()
	source, ref := pgDescriptionUnitSource(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, state := range []*catalog.State{e.catalog.state, catalog.NewState(0)} {
		captured, err := captureCatalogSourceRows(catalogSourceInput{Context: ctx, Catalog: e.catalog.read(state), Ref: ref, Source: source, Owner: (e.catalog.read(state)).Owner()}, capturePGDescription)
		if captured != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial canceled source", captured, err)
		}
	}
}

func TestPGDescriptionBuiltinSeeds(t *testing.T) {
	e := New()
	source, ref := pgDescriptionUnitSource(t, e)
	captured, err := captureCatalogSourceRows(catalogSourceInput{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ref, Source: source, Owner: (e.CatalogRead()).Owner()}, capturePGDescription)
	if err != nil {
		t.Fatal(err)
	}
	bag := pgDescriptionUnitBag(t, captured)
	expected := map[ids.ObjectAddress]string{
		{ClassID: ids.NamespaceClassID, ObjectID: 11}:   "system catalog schema",
		{ClassID: ids.NamespaceClassID, ObjectID: 99}:   "reserved schema for TOAST tables",
		{ClassID: ids.NamespaceClassID, ObjectID: 2200}: "standard public schema",
	}
	for _, group := range []struct {
		class ids.OID
		seeds []pgDescriptionExpectedSeed
	}{
		{ids.TypeClassID, pgDescriptionExpectedTypes},
		{ids.CollationClassID, pgDescriptionExpectedCollations},
		{ids.ConversionClassID, pgDescriptionExpectedConversions},
	} {
		for _, seed := range group.seeds {
			expected[ids.ObjectAddress{ClassID: group.class, ObjectID: seed.oid}] = seed.text
		}
	}
	if len(bag) != 166 || !reflect.DeepEqual(bag, expected) {
		t.Fatal("complete independent baseline", bag, expected)
	}
	for _, oid := range []ids.OID{4401, 4450, 4451, 4532} {
		if conversionDescription(oid) != "" {
			t.Fatal("conversion hole or boundary", oid)
		}
	}
	for _, descriptor := range e.catalog.state.Descriptors() {
		switch descriptor.(type) {
		case *catalog.Collation, *catalog.Conversion:
			if descriptorComment(descriptor, 0) != "" || catalogDescription(descriptor, 1) != "" {
				t.Fatal("projection widened inquiry/subobject boundary", descriptor)
			}
		}
	}
	if _, exists := bag[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 1003}]; exists {
		t.Fatal("array inherited description")
	}
}
