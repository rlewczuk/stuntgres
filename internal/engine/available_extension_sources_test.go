package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func availableUnitSource(e *Engine, oid ids.OID) (*relationDescriptor, ids.ObjectRef) {
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	return e.catalog.state.MustRelation(a), e.catalog.domain.Ref(e.catalog.state.epoch, a)
}

func TestAvailableExtensionsShapeAndOwnership(t *testing.T) {
	e, foreign := New(), New()
	for _, spec := range []struct {
		oid, row, array ids.OID
		names           []string
		ids             []sqltype.ID
		collations      []uint32
	}{
		{12081, 12083, 12082, []string{"name", "default_version", "installed_version", "comment"}, []sqltype.ID{19, 25, 25, 25}, []uint32{950, 100, 950, 100}},
		{12085, 12087, 12086, []string{"name", "version", "installed", "superuser", "trusted", "relocatable", "schema", "requires", "comment"}, []sqltype.ID{19, 25, 16, 16, 16, 16, 19, 1003, 25}, []uint32{950, 100, 0, 0, 0, 0, 950, 950, 100}},
	} {
		d, ref := availableUnitSource(e, spec.oid)
		owner := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		if !d.virtualView || d.relationKind != catalogRelation || d.namespace.ObjectID != 11 || d.owner != owner || d.rowType != sqltype.ID(spec.row) || len(d.columns) != len(spec.names) || d.attributeHighWater != int32(len(spec.names)) {
			t.Fatal("view identity", d)
		}
		if len(d.acl.Entries) != 1 || !d.acl.Explicit || !d.acl.Entries[0].Grantee.Public || d.acl.Entries[0].Grantor != owner || d.acl.Entries[0].Privileges != authorization.SelectPrivilege {
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
			if source, err := captureAvailableExtensions(context.Background(), e.catalog.extensions, e.CatalogRead(), ref, &invalid); err == nil || source != nil {
				t.Fatal("foreign generation accepted", i, source, err)
			}
		}
		if err := (relationProvider{}).Validate(e.CatalogRead(), d); err != nil {
			t.Fatal(err)
		}
		row := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: spec.row})
		array := e.catalog.state.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: spec.array})
		ownedArray, ok := e.CatalogRead().LookupArray(row.typ)
		if !ok || ownedArray != array.typ || row.array != sqltype.ID(spec.array) || array.typ.Element() != row.typ || row.owningRelation != d.address || array.owningRelation != d.address || row.owner != owner || array.owner != owner {
			t.Fatal("reciprocal ownership", row, array)
		}
		for _, width := range []int{0, len(d.columns) - 1, len(d.columns) + 1} {
			invalid := *d
			invalid.columns = make([]ColumnDescriptor, width)
			if source, err := captureAvailableExtensions(context.Background(), e.catalog.extensions, e.CatalogRead(), ref, &invalid); err == nil || source != nil {
				t.Fatal("malformed width accepted", width, source, err)
			}
		}
	}
}

func availableUnitExpected(t *testing.T, e *Engine, catalog CatalogRead, version any, installed bool) {
	t.Helper()
	comment := "text search dictionary that removes accents"
	for _, oid := range []ids.OID{12081, 12085} {
		d, ref := availableUnitSource(e, oid)
		source, err := captureAvailableExtensions(context.Background(), e.catalog.extensions, catalog, ref, d)
		if err != nil || source == nil || source.count() != 6 || source.ref != ref {
			t.Fatal("capture", source, err)
		}
		expected := map[string][]any{
			"unaccent":      {"unaccent", "1.1", version, comment},
			"intarray":      {"intarray", "1.5", nil, "functions, operators, and index support for 1-D arrays of integers"},
			"intagg":        {"intagg", "1.1", nil, "integer aggregator and enumerator (obsolete)"},
			"fuzzystrmatch": {"fuzzystrmatch", "1.2", nil, "determine similarities and distance between strings"},
			"pgcrypto":      {"pgcrypto", "1.4", nil, "cryptographic functions"},
			"uuid-ossp":     {"uuid-ossp", "1.1", nil, "generate universally unique identifiers (UUIDs)"},
		}
		if oid == 12085 {
			expected["unaccent"] = []any{"unaccent", "1.1", installed, true, true, true, nil, nil, comment}
			expected["intarray"] = []any{"intarray", "1.5", false, true, true, true, nil, nil, "functions, operators, and index support for 1-D arrays of integers"}
			expected["intagg"] = []any{"intagg", "1.1", false, true, false, true, nil, nil, "integer aggregator and enumerator (obsolete)"}
			expected["fuzzystrmatch"] = []any{"fuzzystrmatch", "1.2", false, true, true, true, nil, nil, "determine similarities and distance between strings"}
			expected["pgcrypto"] = []any{"pgcrypto", "1.4", false, true, true, true, nil, nil, "cryptographic functions"}
			expected["uuid-ossp"] = []any{"uuid-ossp", "1.1", false, true, true, true, nil, nil, "generate universally unique identifiers (UUIDs)"}
		}
		for _, row := range source.anonymous {
			name, err := row[0].Text()
			if err != nil {
				t.Fatal(err)
			}
			want, ok := expected[name]
			if !ok {
				t.Fatal("unexpected registry row", name)
			}
			delete(expected, name)
			for i, v := range row {
				if v.IsNull() {
					if want[i] != nil {
						t.Fatal("unexpected NULL", name, i)
					}
					continue
				}
				var got any
				if d.columns[i].Usage.Type.ID() == sqltype.Bool {
					got, err = v.Boolean()
				} else {
					got, err = v.Text()
				}
				if err != nil || got != want[i] {
					t.Fatal("value", name, i, got, want[i], err)
				}
			}
		}
		if len(expected) != 0 {
			t.Fatal("missing registry rows", expected)
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
	state := *e.catalog.state
	state.descriptors = maps.Clone(e.catalog.state.descriptors)
	state.names = maps.Clone(e.catalog.state.names)
	address := state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
	ext := *state.MustExtension(address)
	ext.version = "available-unknown"
	state.setDescriptor(address, &ext)
	availableUnitExpected(t, e, e.catalog.read(&state), "available-unknown", false)
	// A private installed descriptor with no registered package is not a row.
	unavailable := ext
	unavailable.address.ObjectID++
	unavailable.name = "unavailable-component-fixture"
	state.setDescriptor(unavailable.address, &unavailable)
	state.bindName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: unavailable.name}, unavailable.address)
	availableUnitExpected(t, e, e.catalog.read(&state), "available-unknown", false)
}

func TestAvailableExtensionsMetadataValues(t *testing.T) {
	e := New()
	d, _ := availableUnitSource(e, 12085)
	empty, schema := "", "fixed_control_schema"
	metadata := registeredExtension{version: "1.1", trusted: true, superuser: true, relocatable: true, comment: &empty, schema: &schema, requires: []string{"first", "quote\"name", "comma,name"}}
	// Metadata-only projection input, not a new registry entry or provider.
	row, err := projectAvailableExtension("unaccent", metadata, &extensionDescriptor{version: "1.1", targetNamespace: ids.ObjectAddress{ObjectID: 999}}, d)
	if err != nil {
		t.Fatal(err)
	}
	gotSchema, _ := row[6].Text()
	gotComment, _ := row[8].Text()
	deps, err := row[7].NamesOutput()
	if err != nil || deps != `{first,"quote\"name","comma,name"}` || gotSchema != schema || row[8].IsNull() || gotComment != "" {
		t.Fatal("optional control values", row, deps, err)
	}
	for _, i := range []int{2, 3, 4, 5} {
		if value, err := row[i].Boolean(); err != nil || !value {
			t.Fatal("independent flags", i, value, err)
		}
	}
	metadata.comment, metadata.schema, metadata.requires = nil, nil, nil
	row, err = projectAvailableExtension("unaccent", metadata, nil, d)
	if err != nil || !row[6].IsNull() || !row[7].IsNull() || !row[8].IsNull() || row[7].Type() != d.columns[7].Usage.Type {
		t.Fatal("typed absence", row, err)
	}
	metadata.requires = []string{strings.Repeat("x", 64)}
	if row, err := projectAvailableExtension("unaccent", metadata, nil, d); err == nil || row != nil {
		t.Fatal("unchecked NAME dependency", row, err)
	}
	// Registration detaches actual provider metadata; no fake package is added.
	registry := &Engine{}
	metadata = e.catalog.extensions["unaccent"]
	metadata.comment, metadata.schema, metadata.requires = &empty, &schema, []string{"original"}
	if err := registry.registerExtension("unaccent", metadata); err != nil {
		t.Fatal(err)
	}
	empty, schema, metadata.requires[0] = "mutated", "mutated", "mutated"
	stored := registry.catalog.extensions["unaccent"]
	if *stored.comment != "" || *stored.schema != "fixed_control_schema" || stored.requires[0] != "original" {
		t.Fatal("caller storage retained", stored)
	}
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
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestAvailableExtensionsCapture(t *testing.T) {
	e := New()
	d, ref := availableUnitSource(e, 12081)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if source, err := captureAvailableExtensions(ctx, e.catalog.extensions, e.CatalogRead(), ref, d); source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancellation", source, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	during := &availableCancelContext{Context: ctx, cancel: cancel}
	if source, err := captureAvailableExtensions(during, e.catalog.extensions, e.CatalogRead(), ref, d); source != nil || !errors.Is(err, context.Canceled) || during.checks != 2 {
		t.Fatal("iteration cancellation", source, err)
	}
	op, done := roleOperation(t, e, "postgres")
	extensionUnitPublish(t, e, op, extensionUnitStage(t, e, op))
	done()
	old, err := captureAvailableExtensions(context.Background(), e.catalog.extensions, e.CatalogRead(), ref, d)
	if err != nil {
		t.Fatal(err)
	}
	var installedRow []sqltype.Value
	for _, row := range old.anonymous {
		if name, err := row[0].Text(); err == nil && name == "unaccent" {
			installedRow = row
		}
	}
	if installedRow == nil {
		t.Fatal("missing installed unaccent registry row")
	}
	values := append([]sqltype.Value(nil), installedRow...)
	columns := append([]ColumnDescriptor(nil), d.columns...)
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	availableUnitExpected(t, e, e.CatalogRead(), nil, false)
	fresh, _ := availableUnitSource(e, 12081)
	if fresh.columns[0].Usage.Type == d.columns[0].Usage.Type || !reflect.DeepEqual(values, installedRow) || !reflect.DeepEqual(columns, d.columns) {
		t.Fatal("retained generation mutated")
	}
	if version, err := installedRow[2].Text(); err != nil || version != "1.1" {
		t.Fatal("retained installed state", version, err)
	}
	if source, err := captureAvailableExtensions(context.Background(), e.catalog.extensions, e.CatalogRead(), ref, d); source != nil || err == nil {
		t.Fatal("old generation admitted", source, err)
	}
}
