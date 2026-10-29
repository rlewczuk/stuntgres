package schemacmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func availableUnitSource(e *core.Engine, oid ids.OID) (*catalog.Relation, ids.ObjectRef) {
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	return e.Catalog.State.MustRelation(a), e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), a)
}

func TestAvailableExtensionsShapeAndOwnership(t *testing.T) {
	e, foreign := engine.New(), engine.New()
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
		owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		if !d.VirtualView || d.RelationKind != catalog.CatalogRelation || d.Namespace().ObjectID != 11 || d.Owner != owner || d.RowType != sqltype.ID(spec.row) || len(d.Columns) != len(spec.names) || d.ColumnMeta.HighWater != int32(len(spec.names)) {
			t.Fatal("view identity", d)
		}
		if len(d.Security.ACL.Entries) != 1 || !d.Security.ACL.Explicit || !d.Security.ACL.Entries[0].Grantee.Public || d.Security.ACL.Entries[0].Grantor != owner || d.Security.ACL.Entries[0].Privileges != authorization.SelectPrivilege {
			t.Fatal("PUBLIC SELECT", d.Security.ACL)
		}
		for i, c := range d.Columns {
			owned, _ := e.CatalogRead().LookupType(spec.ids[i])
			if c.Name != spec.names[i] || c.Usage.Type != owned || c.Usage.Typmod != -1 || uint32(c.Usage.Collation.ID) != spec.collations[i] || !c.Nullable || !c.NullabilityKnown || c.OriginTable != uint32(spec.oid) || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[c.Name] != i {
				t.Fatal("column", i, c)
			}
			invalid := *d
			invalid.Columns = append([]catalog.Column(nil), d.Columns...)
			invalid.Columns[i].Usage.Type, _ = foreign.CatalogRead().LookupType(spec.ids[i])
			if source, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ref, Source: &invalid, Owner: &e.Catalog.Owner, Runtime: core.EngineCatalogRuntime{}}, captureAvailableExtensions); err == nil || source != nil {
				t.Fatal("foreign generation accepted", i, source, err)
			}
		}
		if err := (core.RelationProvider).Validate(e.CatalogRead(), d); err != nil {
			t.Fatal(err)
		}
		row := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: spec.row})
		array := e.Catalog.State.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: spec.array})
		ownedArray, ok := e.CatalogRead().LookupArray(row.SQLType)
		if !ok || ownedArray != array.SQLType || row.Array != sqltype.ID(spec.array) || array.SQLType.Element() != row.SQLType || row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Owner != owner || array.Owner != owner {
			t.Fatal("reciprocal ownership", row, array)
		}
		for _, width := range []int{0, len(d.Columns) - 1, len(d.Columns) + 1} {
			invalid := *d
			invalid.Columns = make([]catalog.Column, width)
			if source, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ref, Source: &invalid, Owner: &e.Catalog.Owner, Runtime: core.EngineCatalogRuntime{}}, captureAvailableExtensions); err == nil || source != nil {
				t.Fatal("malformed width accepted", width, source, err)
			}
		}
	}
}

func availableUnitExpected(t *testing.T, e *core.Engine, catalog catalog.Read, version any, installed bool) {
	t.Helper()
	comment := "text search dictionary that removes accents"
	for _, oid := range []ids.OID{12081, 12085} {
		d, ref := availableUnitSource(e, oid)
		source, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: catalog, Ref: ref, Source: d, Owner: &e.Catalog.Owner, Runtime: core.EngineCatalogRuntime{}}, captureAvailableExtensions)
		if err != nil || source == nil || source.Ref != ref {
			t.Fatal("capture", source, err)
		}
		expected := map[string][]any{
			"amcheck":       {"amcheck", "1.5", nil, "functions for verifying relation integrity"},
			"citext":        {"citext", "1.8", nil, "data type for case-insensitive character strings"},
			"cube":          {"cube", "1.5", nil, "data type for multidimensional cubes"},
			"earthdistance": {"earthdistance", "1.2", nil, "calculate great-circle distances on the surface of the Earth"},
			"hstore":        {"hstore", "1.8", nil, "data type for storing sets of (key, value) pairs"},
			"unaccent":      {"unaccent", "1.1", version, comment},
			"intarray":      {"intarray", "1.5", nil, "functions, operators, and index support for 1-D arrays of integers"},
			"intagg":        {"intagg", "1.1", nil, "integer aggregator and enumerator (obsolete)"},
			"isn":           {"isn", "1.3", nil, "data types for international product numbering standards"},
			"lo":            {"lo", "1.2", nil, "Large Object maintenance"},
			"ltree":         {"ltree", "1.3", nil, "data type for hierarchical tree-like structures"},
			"pageinspect":   {"pageinspect", "1.13", nil, "inspect the contents of database pages at a low level"},
			"seg":           {"seg", "1.4", nil, "data type for representing line segments or floating-point intervals"},
			"tablefunc":     {"tablefunc", "1.0", nil, "functions that manipulate whole tables, including crosstab"},
			"tcn":           {"tcn", "1.0", nil, "Triggered change notifications"},
			"pgrowlocks":    {"pgrowlocks", "1.2", nil, "show row-level locking information"},
			"fuzzystrmatch": {"fuzzystrmatch", "1.2", nil, "determine similarities and distance between strings"},
			"pgcrypto":      {"pgcrypto", "1.4", nil, "cryptographic functions"},
			"plpgsql":       {"plpgsql", "1.0", "1.0", "PL/pgSQL procedural language"},
			"uuid-ossp":     {"uuid-ossp", "1.1", nil, "generate universally unique identifiers (UUIDs)"},
		}
		if oid == 12085 {
			expected["amcheck"] = []any{"amcheck", "1.5", false, true, false, true, nil, nil, "functions for verifying relation integrity"}
			expected["citext"] = []any{"citext", "1.8", false, true, true, true, nil, nil, "data type for case-insensitive character strings"}
			expected["cube"] = []any{"cube", "1.5", false, true, true, true, nil, nil, "data type for multidimensional cubes"}
			expected["earthdistance"] = []any{"earthdistance", "1.2", false, true, false, true, nil, "{cube}", "calculate great-circle distances on the surface of the Earth"}
			expected["hstore"] = []any{"hstore", "1.8", false, true, true, true, nil, nil, "data type for storing sets of (key, value) pairs"}
			expected["unaccent"] = []any{"unaccent", "1.1", installed, true, true, true, nil, nil, comment}
			expected["intarray"] = []any{"intarray", "1.5", false, true, true, true, nil, nil, "functions, operators, and index support for 1-D arrays of integers"}
			expected["intagg"] = []any{"intagg", "1.1", false, true, false, true, nil, nil, "integer aggregator and enumerator (obsolete)"}
			expected["isn"] = []any{"isn", "1.3", false, true, true, true, nil, nil, "data types for international product numbering standards"}
			expected["lo"] = []any{"lo", "1.2", false, true, true, true, nil, nil, "Large Object maintenance"}
			expected["ltree"] = []any{"ltree", "1.3", false, true, true, true, nil, nil, "data type for hierarchical tree-like structures"}
			expected["pageinspect"] = []any{"pageinspect", "1.13", false, true, false, true, nil, nil, "inspect the contents of database pages at a low level"}
			expected["seg"] = []any{"seg", "1.4", false, true, true, true, nil, nil, "data type for representing line segments or floating-point intervals"}
			expected["tablefunc"] = []any{"tablefunc", "1.0", false, true, true, true, nil, nil, "functions that manipulate whole tables, including crosstab"}
			expected["tcn"] = []any{"tcn", "1.0", false, true, true, true, nil, nil, "Triggered change notifications"}
			expected["pgrowlocks"] = []any{"pgrowlocks", "1.2", false, true, false, true, nil, nil, "show row-level locking information"}
			expected["fuzzystrmatch"] = []any{"fuzzystrmatch", "1.2", false, true, true, true, nil, nil, "determine similarities and distance between strings"}
			expected["pgcrypto"] = []any{"pgcrypto", "1.4", false, true, true, true, nil, nil, "cryptographic functions"}
			expected["plpgsql"] = []any{"plpgsql", "1.0", true, true, true, false, "pg_catalog", nil, "PL/pgSQL procedural language"}
			expected["uuid-ossp"] = []any{"uuid-ossp", "1.1", false, true, true, true, nil, nil, "generate universally unique identifiers (UUIDs)"}
			versions := make(map[string][]any, len(expected)+1)
			for name, row := range expected {
				versions[name+"\x00"+row[1].(string)] = row
			}
			expected = versions
			expected["fuzzystrmatch\x001.1"] = []any{"fuzzystrmatch", "1.1", false, true, true, true, nil, nil, "determine similarities and distance between strings"}
		}
		for _, row := range source.Anonymous {
			name, err := row[0].Text()
			if err != nil {
				t.Fatal(err)
			}
			if oid == 12085 {
				version, versionErr := row[1].Text()
				if versionErr != nil {
					t.Fatal(versionErr)
				}
				name += "\x00" + version
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
				if d.Columns[i].Usage.Type.ID() == sqltype.Bool {
					got, err = v.Boolean()
				} else if d.Columns[i].Usage.Type.ID() == sqltype.NameArray {
					got, err = v.NamesOutput()
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
	e := engine.New()
	availableUnitExpected(t, e, e.CatalogRead(), nil, false)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	extensionUnitPublish(t, e, op, extensionUnitStage(t, e, op))
	availableUnitExpected(t, e, e.CatalogRead(), "1.1", true)
	// Authentic installed descriptor, privately copied with only its version
	// changed for projection. It is never validated/published as installation.
	state := *e.Catalog.State
	address := state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
	ext := *state.MustExtension(address)
	ext.Version = "available-unknown"
	state.SetDescriptor(address, &ext)
	availableUnitExpected(t, e, e.Catalog.Read(&state), "available-unknown", false)
	// A private installed descriptor with no registered package is not a row.
	unavailable := ext
	unavailable.ObjectAddress.ObjectID++
	unavailable.ObjectName = "unavailable-component-fixture"
	state.SetDescriptor(unavailable.Address(), &unavailable)
	state.BindName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: unavailable.Name()}, unavailable.Address())
	availableUnitExpected(t, e, e.Catalog.Read(&state), "available-unknown", false)
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
	e := engine.New()
	d, ref := availableUnitSource(e, 12081)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if source, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: e.CatalogRead(), Ref: ref, Source: d, Owner: &e.Catalog.Owner, Runtime: core.EngineCatalogRuntime{}}, captureAvailableExtensions); source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancellation", source, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	during := &availableCancelContext{Context: ctx, cancel: cancel}
	if source, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: during, Catalog: e.CatalogRead(), Ref: ref, Source: d, Owner: &e.Catalog.Owner, Runtime: core.EngineCatalogRuntime{}}, captureAvailableExtensions); source != nil || !errors.Is(err, context.Canceled) || during.checks != 2 {
		t.Fatal("iteration cancellation", source, err)
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	extensionUnitPublish(t, e, op, extensionUnitStage(t, e, op))
	done()
	old, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ref, Source: d, Owner: &e.Catalog.Owner, Runtime: core.EngineCatalogRuntime{}}, captureAvailableExtensions)
	if err != nil {
		t.Fatal(err)
	}
	var installedRow []sqltype.Value
	for _, row := range old.Anonymous {
		if name, err := row[0].Text(); err == nil && name == "unaccent" {
			installedRow = row
		}
	}
	if installedRow == nil {
		t.Fatal("missing installed unaccent registry row")
	}
	values := append([]sqltype.Value(nil), installedRow...)
	columns := append([]catalog.Column(nil), d.Columns...)
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	availableUnitExpected(t, e, e.CatalogRead(), nil, false)
	fresh, _ := availableUnitSource(e, 12081)
	if fresh.Columns[0].Usage.Type == d.Columns[0].Usage.Type || !reflect.DeepEqual(values, installedRow) || !reflect.DeepEqual(columns, d.Columns) {
		t.Fatal("retained generation mutated")
	}
	if version, err := installedRow[2].Text(); err != nil || version != "1.1" {
		t.Fatal("retained installed state", version, err)
	}
	if source, err := core.CaptureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: e.CatalogRead(), Ref: ref, Source: d, Owner: &e.Catalog.Owner, Runtime: core.EngineCatalogRuntime{}}, captureAvailableExtensions); source != nil || err == nil {
		t.Fatal("old generation admitted", source, err)
	}
}
