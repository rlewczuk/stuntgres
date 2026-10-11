package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"stuntgres/internal/sqltype"
	"testing"
)

func availableUnitSource(e *Engine, oid OID) (*relationDescriptor, ObjectRef) {
	a := ObjectAddress{ClassID: RelationClassID, ObjectID: oid}
	return e.state.descriptors[a].(*relationDescriptor), ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: a}
}

func TestAvailableExtensionsShapeAndOwnership(t *testing.T) {
	e, foreign := New(), New()
	for _, spec := range []struct {
		oid, row, array OID
		names []string
		ids []sqltype.ID
		collations []uint32
	}{
		{12081, 12083, 12082, []string{"name", "default_version", "installed_version", "comment"}, []sqltype.ID{19, 25, 25, 25}, []uint32{950, 100, 950, 100}},
		{12085, 12087, 12086, []string{"name", "version", "installed", "superuser", "trusted", "relocatable", "schema", "requires", "comment"}, []sqltype.ID{19, 25, 16, 16, 16, 16, 19, 1003, 25}, []uint32{950, 100, 0, 0, 0, 0, 950, 950, 100}},
	} {
		d, ref := availableUnitSource(e, spec.oid)
		owner := e.roles.names["postgres"]
		if !d.virtualView || d.relationKind != catalogRelation || d.namespace.ObjectID != 11 || d.owner != owner || d.rowType != sqltype.ID(spec.row) || len(d.columns) != len(spec.names) || d.attributeHighWater != int32(len(spec.names)) {
			t.Fatal("view identity", d)
		}
		if len(d.acl.Entries) != 1 || !d.acl.Explicit || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Grantor != owner || d.acl.Entries[0].Privileges != SelectPrivilege {
			t.Fatal("PUBLIC SELECT", d.acl)
		}
		for i, c := range d.columns {
			owned, _ := e.CatalogRead().LookupType(spec.ids[i])
			if c.Name != spec.names[i] || c.Usage.Type != owned || c.Usage.Typmod != -1 || uint32(c.Usage.Collation.ID) != spec.collations[i] || !c.Nullable || !c.NullabilityKnown || c.OriginTable != uint32(spec.oid) || c.OriginAttribute != int16(i+1) || d.attributes[i] != int32(i+1) || d.index[c.Name] != i {
				t.Fatal("column", i, c)
			}
			invalid := *d
			invalid.columns = append([]ColumnDescriptor(nil), d.columns...)
			invalid.columns[i].Usage.Type, _ = foreign.CatalogRead().LookupType(spec.ids[i])
			if source, err := captureAvailableExtensions(context.Background(), e.extensions, e.CatalogRead(), ref, &invalid); err == nil || source != nil {
				t.Fatal("foreign generation accepted", i, source, err)
			}
		}
		if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
			t.Fatal(err)
		}
		row := e.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: spec.row}].(*typeDescriptor)
		array := e.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: spec.array}].(*typeDescriptor)
		ownedArray, ok := e.CatalogRead().LookupArray(row.typ)
		if !ok || ownedArray != array.typ || row.array != sqltype.ID(spec.array) || array.typ.Element() != row.typ || row.owningRelation != d.address || array.owningRelation != d.address || row.owner != owner || array.owner != owner {
			t.Fatal("reciprocal ownership", row, array)
		}
		for _, width := range []int{0, len(d.columns)-1, len(d.columns)+1} {
			invalid := *d
			invalid.columns = make([]ColumnDescriptor, width)
			if source, err := captureAvailableExtensions(context.Background(), e.extensions, e.CatalogRead(), ref, &invalid); err == nil || source != nil {
				t.Fatal("malformed width accepted", width, source, err)
			}
		}
	}
}

func availableUnitExpected(t *testing.T, e *Engine, catalog CatalogRead, version any, installed bool) {
	t.Helper()
	comment := "text search dictionary that removes accents"
	for _, oid := range []OID{12081, 12085} {
		d, ref := availableUnitSource(e, oid)
		source, err := captureAvailableExtensions(context.Background(), e.extensions, catalog, ref, d)
		if err != nil || source == nil || source.count() != 1 || source.ref != ref {
			t.Fatal("capture", source, err)
		}
		want := []any{"unaccent", "1.1", version, comment}
		if oid == 12085 {
			want = []any{"unaccent", "1.1", installed, true, true, true, nil, nil, comment}
		}
		for i, v := range source.anonymous[0] {
			if v.IsNull() {
				if want[i] != nil { t.Fatal("unexpected NULL", i) }
				continue
			}
			var got any
			if d.columns[i].Usage.Type.ID() == sqltype.Bool {
				got, err = v.Boolean()
			} else {
				got, err = v.Text()
			}
			if err != nil || got != want[i] { t.Fatal("value", i, got, want[i], err) }
		}
	}
}

func TestAvailableExtensionsProjection(t *testing.T) {
	e := New()
	availableUnitExpected(t, e, e.CatalogRead(), nil, false)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	extensionUnitPublish(t, e, op, extensionUnitStage(t, e, op))
	availableUnitExpected(t, e, e.CatalogRead(), "1.1", true)
	// Authentic installed descriptor, privately copied with only its version
	// changed for projection. It is never validated/published as installation.
	state := *e.state
	state.descriptors = maps.Clone(e.state.descriptors)
	state.names = maps.Clone(e.state.names)
	address := state.names[NameKey{ClassID: ExtensionClassID, Name: "unaccent"}]
	ext := *state.descriptors[address].(*extensionDescriptor)
	ext.version = "available-unknown"
	state.descriptors[address] = &ext
	availableUnitExpected(t, e, CatalogRead{owner: e, state: &state}, "available-unknown", false)
	// A private installed descriptor with no registered package is not a row.
	unavailable := ext
	unavailable.address.ObjectID++
	unavailable.name = "unavailable-component-fixture"
	state.descriptors[unavailable.address] = &unavailable
	state.names[NameKey{ClassID: ExtensionClassID, Name: unavailable.name}] = unavailable.address
	availableUnitExpected(t, e, CatalogRead{owner: e, state: &state}, "available-unknown", false)
}

func TestAvailableExtensionsMetadataValues(t *testing.T) {
	e := New()
	d, _ := availableUnitSource(e, 12085)
	empty, schema := "", "fixed_control_schema"
	metadata := registeredExtension{version: "1.1", trusted: true, superuser: true, relocatable: true, comment: &empty, schema: &schema, requires: []string{"first", "quote\"name", "comma,name"}}
	// Metadata-only projection input, not a new registry entry or provider.
	row, err := projectAvailableExtension("unaccent", metadata, &extensionDescriptor{version: "1.1", targetNamespace: ObjectAddress{ObjectID: 999}}, d)
	if err != nil { t.Fatal(err) }
	gotSchema, _ := row[6].Text()
	gotComment, _ := row[8].Text()
	deps, err := row[7].NamesOutput()
	if err != nil || deps != `{first,"quote\"name","comma,name"}` || gotSchema != schema || row[8].IsNull() || gotComment != "" {
		t.Fatal("optional control values", row, deps, err)
	}
	for _, i := range []int{2, 3, 4, 5} {
		if value, err := row[i].Boolean(); err != nil || !value { t.Fatal("independent flags", i, value, err) }
	}
	metadata.comment, metadata.schema, metadata.requires = nil, nil, nil
	row, err = projectAvailableExtension("unaccent", metadata, nil, d)
	if err != nil || !row[6].IsNull() || !row[7].IsNull() || !row[8].IsNull() || row[7].Type() != d.columns[7].Usage.Type { t.Fatal("typed absence", row, err) }
	metadata.requires = []string{strings.Repeat("x", 64)}
	if row, err := projectAvailableExtension("unaccent", metadata, nil, d); err == nil || row != nil { t.Fatal("unchecked NAME dependency", row, err) }
	// Registration detaches actual provider metadata; no fake package is added.
	registry := &Engine{}
	metadata = e.extensions["unaccent"]
	metadata.comment, metadata.schema, metadata.requires = &empty, &schema, []string{"original"}
	if err := registry.registerExtension("unaccent", metadata); err != nil { t.Fatal(err) }
	empty, schema, metadata.requires[0] = "mutated", "mutated", "mutated"
	stored := registry.extensions["unaccent"]
	if *stored.comment != "" || *stored.schema != "fixed_control_schema" || stored.requires[0] != "original" { t.Fatal("caller storage retained", stored) }
}

// A real cancellation is triggered at the next capture context checkpoint,
// deterministically, without goroutine timing or an oversized registry fixture.
type availableCancelContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}
func (c *availableCancelContext) Err() error {
	c.checks++
	if c.checks == 2 { c.cancel() }
	return c.Context.Err()
}

func TestAvailableExtensionsCapture(t *testing.T) {
	e := New()
	d, ref := availableUnitSource(e, 12081)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if source, err := captureAvailableExtensions(ctx, e.extensions, e.CatalogRead(), ref, d); source != nil || !errors.Is(err, context.Canceled) { t.Fatal("pre-cancellation", source, err) }
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	during := &availableCancelContext{Context: ctx, cancel: cancel}
	if source, err := captureAvailableExtensions(during, e.extensions, e.CatalogRead(), ref, d); source != nil || !errors.Is(err, context.Canceled) || during.checks != 2 { t.Fatal("iteration cancellation", source, err) }
	op, done := roleOperation(t, e, "postgres")
	extensionUnitPublish(t, e, op, extensionUnitStage(t, e, op))
	done()
	old, err := captureAvailableExtensions(context.Background(), e.extensions, e.CatalogRead(), ref, d)
	if err != nil { t.Fatal(err) }
	values := append([]sqltype.Value(nil), old.anonymous[0]...)
	columns := append([]ColumnDescriptor(nil), d.columns...)
	if err := e.Reset(context.Background()); err != nil { t.Fatal(err) }
	availableUnitExpected(t, e, e.CatalogRead(), nil, false)
	fresh, _ := availableUnitSource(e, 12081)
	if fresh.columns[0].Usage.Type == d.columns[0].Usage.Type || !reflect.DeepEqual(values, old.anonymous[0]) || !reflect.DeepEqual(columns, d.columns) { t.Fatal("retained generation mutated") }
	if version, err := old.anonymous[0][2].Text(); err != nil || version != "1.1" { t.Fatal("retained installed state", version, err) }
	if source, err := captureAvailableExtensions(context.Background(), e.extensions, e.CatalogRead(), ref, d); source != nil || err == nil { t.Fatal("old generation admitted", source, err) }
}
