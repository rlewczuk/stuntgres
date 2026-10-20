package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func pgTypeUnitSource(t *testing.T, e *Engine) (*catalog.Relation, ids.ObjectRef) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1247}
	d, ok := e.catalog.state.Relation(address)
	if !ok {
		t.Fatal("missing type source")
	}
	return d, e.catalog.Domain.Ref(e.catalog.state.Epoch(), address)
}
func pgTypeUnitBag(t *testing.T, source *querySource, d *catalog.Relation) map[uint32][]sqltype.Value {
	t.Helper()
	bag := make(map[uint32][]sqltype.Value)
	for _, row := range source.anonymous {
		if len(row) != 32 {
			t.Fatal("row width", len(row))
		}
		for i, v := range row {
			if v.Type() != d.Columns[i].Usage.Type || i < 29 && v.IsNull() {
				t.Fatal("cell type/null", i, v)
			}
		}
		id, err := row[0].OID()
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := bag[id]; exists {
			t.Fatal("duplicate type", id)
		}
		bag[id] = row
	}
	for _, id := range []uint32{16, 20, 23, 25, 19, 1003, 71, 210, 13254, 13253, 3614, 3615, 3643, 3645} {
		if bag[id] == nil {
			t.Fatal("missing genuine type", id)
		}
	}
	for _, id := range []uint32{142, 143, 600, 1017, 4533, 4534, 4535, 6152, 6153, 6155, 22, 30, 1790} {
		if bag[id] != nil {
			t.Fatal("unsupported advertised type", id)
		}
	}
	for _, pair := range [][2]uint32{{3614, 3643}, {3615, 3645}} {
		array, err := bag[pair[0]][14].OID()
		if err != nil || array != pair[1] {
			t.Fatal("FTS companion identity", pair, array, err)
		}
		element, err := bag[pair[1]][13].OID()
		if err != nil || element != pair[0] {
			t.Fatal("FTS element identity", pair, element, err)
		}
	}
	int4 := bag[23]
	if n, _ := int4[4].Integer(); n != 4 {
		t.Fatal("int4 length", n)
	}
	if b, _ := int4[5].Boolean(); !b {
		t.Fatal("int4 byval")
	}
	if n, _ := int4[14].OID(); n != 1007 {
		t.Fatal("int4 array", n)
	}
	if n, _ := int4[15].ProcedureOID(); n != 42 {
		t.Fatal("int4 input", n)
	}
	if n, _ := int4[16].ProcedureOID(); n != 43 {
		t.Fatal("int4 output", n)
	}
	for _, i := range []int{29, 30, 31} {
		if !int4[i].IsNull() {
			t.Fatal("int4 default/acl is not NULL", i)
		}
	}
	if n, _ := bag[71][11].OID(); n != 1247 {
		t.Fatal("row relation", n)
	}
	if n, _ := bag[210][11].OID(); n != 0 {
		t.Fatal("array relation", n)
	}
	seed := bag[13254]
	tree, err := seed[29].Tree()
	if err != nil {
		t.Fatal(err)
	}
	text, err := tree.StructuralOutput(context.Background())
	if err != nil || text != "{SQLVALUEFUNCTION :op 4 :type 1184 :typmod 2 :location -1}" {
		t.Fatal(text, err)
	}
	if seed[30].IsNull() {
		t.Fatal("seeded default is NULL")
	}
	return bag
}
func TestPGTypeCaptureOwnership(t *testing.T) {
	e := New()
	catalogView := e.CatalogRead()
	d, ref := pgTypeUnitSource(t, e)
	roles := e.roles.read(e.catalog.owner(), catalogView, e.roles.published)
	if len(d.Columns) != 32 || d.RowType != 71 || !d.History.HasIndexes {
		t.Fatal("source definition", d)
	}
	for i, c := range d.Columns {
		if !c.Usage.Valid() {
			t.Fatal("invalid registered type column usage", i, c)
		}
		if c.OriginTable != 1247 || c.OriginAttribute != int16(i+1) || !c.NullabilityKnown || c.Nullable != (i >= 29) {
			t.Fatal("column definition", i, c)
		}
		collation := uint32(0)
		if i == 1 || i == 29 || i == 30 {
			collation = 950
		}
		dimensions := int16(0)
		if i == 31 {
			dimensions = 1
		}
		if c.Usage.Collation.ID != collation || c.DeclaredDimensions != dimensions {
			t.Fatal("attribute usage", i, c)
		}
	}
	descriptors, names := maps.Collect(e.catalog.state.Descriptors()), maps.Collect(e.catalog.state.Names())
	capture := func() *querySource {
		t.Helper()
		s, err := captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: catalogView, Roles: roles, Ref: ref, Source: d, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	first, second := capture(), capture()
	bag := pgTypeUnitBag(t, second, d)
	if first.ref != ref || !maps.Equal(descriptors, maps.Collect(e.catalog.state.Descriptors())) || !maps.Equal(names, maps.Collect(e.catalog.state.Names())) {
		t.Fatal("capture mutated authority")
	}
	first.anonymous[0][0] = sqltype.Null(d.Columns[0].Usage.Type)
	first.anonymous[0] = nil
	if !reflect.DeepEqual(pgTypeUnitBag(t, second, d), bag) {
		t.Fatal("shared capture backing")
	}
	ordinary := func(s *querySource, err error) {
		t.Helper()
		if s != nil || err == nil {
			t.Fatal("invalid capture accepted", s, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("invariant gained SQLSTATE", err)
		}
	}
	other := New()
	foreign := other.CatalogRead()
	s, err := captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: foreign, Roles: roles, Ref: ref, Source: d, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	ordinary(s, err)
	s, err = captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: catalogView, Roles: other.roles.read(other.catalog.owner(), foreign, other.roles.published), Ref: ref, Source: d, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	ordinary(s, err)
	s, err = captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: catalogView, Roles: roles, Ref: ref, Source: nil, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	ordinary(s, err)
	s, err = captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: catalog.Read{}, Roles: roles, Ref: ref, Source: d, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	ordinary(s, err)
	s, err = captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: catalogView, Roles: roles, Ref: ids.ObjectRef{}, Source: d, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	ordinary(s, err)
	badRef := ref
	badRef.Epoch++
	s, err = captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: catalogView, Roles: roles, Ref: badRef, Source: d, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	ordinary(s, err)
	badRef = ref
	badRef = other.catalog.Domain.Ref(badRef.Epoch, badRef.Address)
	s, err = captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: catalogView, Roles: roles, Ref: badRef, Source: d, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	ordinary(s, err)
	badRef = ref
	badRef.Address.ObjectID = 1259
	s, err = captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: catalogView, Roles: roles, Ref: badRef, Source: d, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	ordinary(s, err)
	for _, badWidth := range []bool{false, true} {
		root := catalog.CloneState(catalogView.State())
		bad := *d
		bad.Columns = append([]catalog.Column(nil), d.Columns...)
		if badWidth {
			bad.Columns = bad.Columns[:31]
		} else {
			bad.Columns[0].Usage.Type, _ = foreign.LookupType(sqltype.Oid)
		}
		root.SetDescriptor(d.Address(), &bad)
		view := e.catalog.read(root)
		s, err = captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: roles, Ref: ref, Source: &bad, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
		ordinary(s, err)
	}
	// A genuine explicit empty ACL is distinct from an absent/default ACL.
	root := catalog.CloneState(catalogView.State())
	original := catalogView.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23})
	changed := *original
	changed.ACL = authorization.ACLValue{Explicit: true}
	changed.ACLSyntheticDefault = false
	root.SetDescriptor(changed.Address(), &changed)
	s, err = captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: e.catalog.read(root), Roles: roles, Ref: ref, Source: d, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range s.anonymous {
		if id, _ := row[0].OID(); id == 23 {
			found = true
			if row[31].IsNull() || row[31].ArrayLen() != 0 {
				t.Fatal("explicit empty ACL lost", row[31])
			}
		}
	}
	if !found {
		t.Fatal("modified ACL type absent")
	}
	for _, at := range []int32{1, 10, 100, int32(catalogView.DescriptorCount() + 25)} {
		during, cancel := context.WithCancel(context.Background())
		ctx := &cancelAtErrCheck{Context: during, cancel: cancel, at: at}
		got, err := captureCatalogSourceRows(pgcatalog.Input{Context: ctx, Catalog: catalogView, Roles: roles, Ref: ref, Source: d, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
		cancel()
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled partial capture", got, err)
		}
	}
	if err := e.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	fresh, freshRef := pgTypeUnitSource(t, e)
	freshCatalog := e.CatalogRead()
	s, err = captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: freshCatalog, Roles: e.roles.read(e.catalog.owner(), freshCatalog, e.roles.published), Ref: freshRef, Source: fresh, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	if err != nil {
		t.Fatal(err)
	}
	// Value pointers must be reowned; compare fixed inventory and relationships,
	// not equality of native descriptor pointers across catalog replacement.
	pgTypeUnitBag(t, s, fresh)
	pgTypeUnitBag(t, second, d)
	if fresh == d || fresh.Columns[0].Usage.Type == d.Columns[0].Usage.Type {
		t.Fatal("replacement did not reown source")
	}
	s, err = captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: freshCatalog, Roles: e.roles.read(e.catalog.owner(), freshCatalog, e.roles.published), Ref: freshRef, Source: d, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	ordinary(s, err)
	s, err = captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: catalogView, Roles: roles, Ref: ref, Source: d, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	if err != nil {
		t.Fatal("retained root rejected", err)
	}
	pgTypeUnitBag(t, s, d)
}

func TestPGTypeInstalledDomainAuthority(t *testing.T) {
	e := New()
	install := func() {
		op, done := roleOperation(t, e, "postgres")
		defer done()
		for _, q := range []string{
			"CREATE EXTENSION intarray",
			"CREATE DOMAIN pt18_query_domain AS query_int",
			"CREATE DOMAIN pt18_query_nested AS pt18_query_domain",
		} {
			roleExecute(t, e, op, q)
		}
	}
	install()
	catalogView := e.CatalogRead()
	source, ref := pgTypeUnitSource(t, e)
	roles := e.roles.read(e.catalog.owner(), catalogView, e.roles.published)
	captured, err := captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: catalogView, Roles: roles, Ref: ref, Source: source, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	if err != nil {
		t.Fatal(err)
	}
	oldRows := pgTypeUnitBag(t, captured, source)
	array := func(t *testing.T, view catalog.Read, d *catalog.Type) *catalog.Type {
		t.Helper()
		a, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Array)})
		if !ok || a.SQLType.Element() != d.SQLType {
			t.Fatal("missing canonical domain array", d)
		}
		return a
	}
	check := func(view catalog.Read, rows map[uint32][]sqltype.Value) {
		t.Helper()
		base := domainUnitType(t, view, "query_int")
		domain := domainUnitType(t, view, "pt18_query_domain")
		nested := domainUnitType(t, view, "pt18_query_nested")
		ext, ok := view.Extension(base.Extension)
		if !ok || len(ext.Members) != 3 {
			t.Fatal("missing actual intarray installation")
		}
		if err := stockIntarray.ValidateExtension(view, ext); err != nil {
			t.Fatal(err)
		}
		output := uint32(ext.Members[2].ObjectID)
		for _, pair := range []struct{ d, base *catalog.Type }{{domain, base}, {nested, domain}} {
			if pair.d.SQLType.DomainBase().Type != pair.base.SQLType {
				t.Fatal("wrong immediate base generation")
			}
			want := sqltype.PGTypeMetadata{
				PostgreSQLAttributeFacts: sqltype.PostgreSQLAttributeFacts{Length: -1, Alignment: "i", Storage: "p"},
				Kind:                     "d", Category: "U", Base: pair.base.SQLType.ID(),
				Input: 2597, Output: output, Receive: 2598, Modifier: -1,
			}
			got, err := pgcatalog.TypeFacts(context.Background(), view, pair.d, sqltype.OutputEnvironment{})
			if err != nil || got != want {
				t.Fatal("installed immediate-domain facts", got, want, err)
			}
			a := array(t, view, pair.d)
			wantArray := sqltype.PGTypeMetadata{
				PostgreSQLAttributeFacts: sqltype.PostgreSQLAttributeFacts{Length: -1, Alignment: "i", Storage: "x"},
				Kind:                     "b", Category: "A", Element: pair.d.SQLType.ID(), Subscript: 6179,
				Input: 750, Output: 751, Receive: 2400, Send: 2401, Analyze: 3816, Modifier: -1,
			}
			gotArray, err := pgcatalog.TypeFacts(context.Background(), view, a, sqltype.OutputEnvironment{})
			if err != nil || gotArray != wantArray {
				t.Fatal("installed domain companion facts", gotArray, wantArray, err)
			}
			row, companion := rows[uint32(pair.d.SQLType.ID())], rows[uint32(a.SQLType.ID())]
			if len(row) != 32 || len(companion) != 32 {
				t.Fatal("missing captured installed domain pair")
			}
			if value, err := row[16].ProcedureOID(); err != nil || value != output {
				t.Fatal("captured inherited output", value, output, err)
			}
			if value, err := row[25].OID(); err != nil || value != uint32(pair.base.SQLType.ID()) {
				t.Fatal("captured immediate base", value, pair.base.SQLType.ID(), err)
			}
			if value, err := companion[13].OID(); err != nil || value != uint32(pair.d.SQLType.ID()) {
				t.Fatal("captured canonical element", value, pair.d.SQLType.ID(), err)
			}
		}
	}
	check(catalogView, oldRows)
	nested := domainUnitType(t, catalogView, "pt18_query_nested")
	for _, name := range []string{"query_int", "pt18_query_domain"} {
		for _, missing := range []bool{true, false} {
			kind := "foreign_generation"
			if missing {
				kind = "missing"
			}
			t.Run(name+"/"+kind, func(t *testing.T) {
				root := catalog.CloneState(catalogView.State())
				original := domainUnitType(t, catalogView, name)
				if missing {
					root.RemoveDescriptor(original.Address())
				} else {
					// Same ID and family, deliberately a different immutable
					// descriptor generation: numeric equality is not authority.
					foreign := *original.SQLType
					replacement := *original
					replacement.SQLType = &foreign
					root.SetDescriptor(original.Address(), &replacement)
				}
				bad := e.catalog.read(root)
				for _, d := range []*catalog.Type{nested, array(t, catalogView, nested)} {
					facts, err := pgcatalog.TypeFacts(context.Background(), bad, d, sqltype.OutputEnvironment{})
					if err == nil || facts != (sqltype.PGTypeMetadata{}) {
						t.Fatal("accepted missing/foreign immediate base", facts, err)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatal("base invariant gained SQLSTATE", err)
					}
				}
			})
		}
	}
	oldBase := domainUnitType(t, catalogView, "query_int")
	if err := e.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	install()
	fresh := e.CatalogRead()
	freshSource, freshRef := pgTypeUnitSource(t, e)
	newCapture, err := captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: fresh, Roles: e.roles.read(e.catalog.owner(), fresh, e.roles.published), Ref: freshRef, Source: freshSource, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	if err != nil {
		t.Fatal(err)
	}
	check(fresh, pgTypeUnitBag(t, newCapture, freshSource))
	if domainUnitType(t, fresh, "query_int").SQLType == oldBase.SQLType {
		t.Fatal("reinstallation retained old type generation")
	}
	// Old rows and old catalog-aware inheritance retain their original actual
	// installation even if the receiving catalog reuses numeric addresses.
	check(catalogView, oldRows)
	oldCapture, err := captureCatalogSourceRows(pgcatalog.Input{Context: t.Context(), Catalog: catalogView, Roles: roles, Ref: ref, Source: source, Owner: e.catalog.owner(), Runtime: engineCatalogRuntime{engine: e}}, capturePGType)
	if err != nil {
		t.Fatal("retained installed catalog rejected", err)
	}
	check(catalogView, pgTypeUnitBag(t, oldCapture, source))
}
