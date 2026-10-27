package core_test

import (
	"context"
	"errors"
	"maps"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func statisticExtDataInput(t *testing.T, e *core.Engine, body func(pgcatalog.Input, core.AuthorizationView)) {
	t.Helper()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatisticExtDataOID}
	d := auth.Catalog.MustRelation(address)
	body(pgcatalog.Input{Context: op.Context(), Catalog: auth.Catalog, Roles: auth.Roles, Owner: auth.Owner, Ref: auth.Ref(address), Source: d, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})}, auth)
}

func TestPGStatisticExtDataRegistrationFacts(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	d := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 3429})
	if d.Name() != "pg_statistic_ext_data" || d.Namespace().ObjectID != 11 || d.RowType != 10033 || !d.History.HasIndexes || d.VirtualView || d.CatalogView || d.RelationKind != catalog.CatalogRelation || d.ColumnMeta.HighWater != 6 || len(d.Columns) != 6 || !pgcatalog.RegisteredSource(view, d) {
		t.Fatal("DATA registration", d)
	}
	if _, ok := pgcatalog.ProducerFor(3429); !ok {
		t.Fatal("DATA producer missing")
	}
	names := []string{"stxoid", "stxdinherit", "stxdndistinct", "stxddependencies", "stxdmcv", "stxdexpr"}
	types := []sqltype.ID{26, 16, 3361, 3402, 5017, 10028}
	for i, c := range d.Columns {
		owned, ok := view.LookupType(types[i])
		a := d.ColumnMeta.CatalogAttributes[i]
		dims := int16(0)
		if i == 5 {
			dims = 1
		}
		// Statistics carriers retain their default usage collation; bootstrap
		// catalog attribute facts canonicalize that textual metadata to C.
		usageCollation, attributeCollation := uint32(0), uint32(0)
		if i >= 2 && i <= 4 {
			usageCollation, attributeCollation = 100, 950
		}
		if !ok || c.Name != names[i] || c.Usage.Type != owned || c.Usage.Typmod != -1 || c.Usage.Collation.ID != usageCollation || c.Nullable != (i >= 2) || !c.NullabilityKnown || c.OriginTable != 3429 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[c.Name] != i || a.NotNull != (i < 2) || a.Dimensions != dims || a.Collation != attributeCollation {
			t.Fatal("DATA canonical attribute", i, c, a)
		}
	}
	if d.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 0 {
		t.Fatal("DATA bootstrap must not grant PUBLIC", d.Security.ACL)
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10033})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10032})
	if row.Array != 10032 || array.SQLType.Element() != row.SQLType || row.OwningRelation != d.Address() || array.OwningRelation != d.Address() || row.Owner != d.Owner || array.Owner != d.Owner {
		t.Fatal("DATA reciprocal composite authority", row, array)
	}
	if err := core.RelationProvider.Validate(view, d); err != nil {
		t.Fatal(err)
	}
}

func TestPGStatisticExtDataCaptureOwnership(t *testing.T) {
	produce, ok := pgcatalog.ProducerFor(3429)
	if !ok {
		t.Fatal("DATA producer missing")
	}
	empty := engine.New()
	statisticExtDataInput(t, empty, func(in pgcatalog.Input, _ core.AuthorizationView) {
		descriptors := maps.Collect(in.Catalog.State().Descriptors())
		names := maps.Collect(in.Catalog.State().Names())
		got, err := core.CaptureCatalogSourceRows(in, produce)
		if err != nil || got == nil || got.Anonymous == nil || len(got.Anonymous) != 0 || got.Ref != in.Ref {
			t.Fatal("genuine empty anonymous source", got, err)
		}
		if !maps.Equal(descriptors, maps.Collect(in.Catalog.State().Descriptors())) || !maps.Equal(names, maps.Collect(in.Catalog.State().Names())) {
			t.Fatal("empty capture mutated roots")
		}
	})
	foreign, _, _ := ndistinctCoreFixture(t)
	var foreignInput pgcatalog.Input
	statisticExtDataInput(t, foreign, func(in pgcatalog.Input, _ core.AuthorizationView) { foreignInput = in })
	e, owner, _ := ndistinctCoreFixture(t)
	retainedRoles := e.Roles()
	enginetest.SessionSQL(t, owner, "CREATE ROLE stxd_new_root")
	statisticExtDataInput(t, e, func(in pgcatalog.Input, auth core.AuthorizationView) {
		d := in.Source
		descriptors := maps.Collect(in.Catalog.State().Descriptors())
		names := maps.Collect(in.Catalog.State().Names())
		got, err := core.CaptureCatalogSourceRows(in, produce)
		if err != nil || got == nil || len(got.Anonymous) != 2 || got.Ref != in.Ref {
			t.Fatal("genuine populated capture", got, err)
		}
		seen := make(map[uint32]bool)
		for _, row := range got.Anonymous {
			if len(row) != 6 {
				t.Fatal("DATA row width", len(row))
			}
			oid, oidErr := row[0].OID()
			inherited, inheritErr := row[1].Boolean()
			definition, exists := in.Catalog.Get(ids.ObjectAddress{ClassID: ids.StatisticExtClassID, ObjectID: ids.OID(oid)}).(*catalog.Statistics)
			text, outputErr := row[2].Type().Output(context.Background(), row[2])
			want := `{"1, 2": 2}`
			if exists && definition.Name() == "nd_b" {
				want = `{"1, 3": 2}`
			}
			if oidErr != nil || inheritErr != nil || inherited || !exists || definition.Name() != "nd_a" && definition.Name() != "nd_b" || seen[oid] || outputErr != nil || text != want {
				t.Fatal("DATA identity/payload", oid, inherited, text, oidErr, inheritErr, outputErr)
			}
			seen[oid] = true
			for i, v := range row {
				if !v.ValidResult(d.Columns[i].Usage) || v.IsNull() != (i >= 3) {
					t.Fatal("DATA typed payload", i, v)
				}
			}
		}
		cases := []struct {
			name   string
			mutate func(*pgcatalog.Input)
		}{
			{"nil owner", func(b *pgcatalog.Input) { b.Owner = nil }},
			{"foreign owner", func(b *pgcatalog.Input) { b.Owner = foreignInput.Owner }},
			{"foreign domain", func(b *pgcatalog.Input) { b.Ref = foreignInput.Owner.Domain.Ref(in.Ref.Epoch, in.Ref.Address) }},
			{"stale epoch", func(b *pgcatalog.Input) { b.Ref.Epoch-- }},
			{"wrong class", func(b *pgcatalog.Input) { b.Ref.Address.ClassID = ids.TypeClassID }},
			{"wrong object", func(b *pgcatalog.Input) { b.Ref.Address.ObjectID++ }},
			{"subobject", func(b *pgcatalog.Input) { b.Ref.Address.SubID = 1 }},
			{"missing roles", func(b *pgcatalog.Input) { b.Roles = authorization.Read{} }},
			{"foreign roles", func(b *pgcatalog.Input) { b.Roles = foreignInput.Roles }},
			{"mismatched role root", func(b *pgcatalog.Input) { b.Roles = retainedRoles }},
			{"nil runtime", func(b *pgcatalog.Input) { b.Runtime = nil }},
			{"foreign runtime", func(b *pgcatalog.Input) { b.Runtime = foreignInput.Runtime }},
			{"foreign backing engine", func(b *pgcatalog.Input) { b.Runtime = core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: foreign, Auth: auth}) }},
			{"nil backing engine", func(b *pgcatalog.Input) { b.Runtime = core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth}) }},
			{"mismatched catalog root", func(b *pgcatalog.Input) { b.Catalog = in.Catalog.WithState(catalog.CloneState(in.Catalog.State())) }},
			{"nil source", func(b *pgcatalog.Input) { b.Source = nil }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				bad := in
				tc.mutate(&bad)
				rows, err := core.CaptureCatalogSourceRows(bad, produce)
				statisticExtDataInvariant(t, rows == nil, err)
			})
		}
		badSources := []*catalog.Relation{{Columns: d.Columns[:5]}, {Columns: append(append([]catalog.Column(nil), d.Columns...), d.Columns[0])}}
		wrongName := *d
		wrongName.ObjectName = "impostor"
		badSources = append(badSources, &wrongName)
		wrongAttribute := *d
		wrongAttribute.Attributes = append([]int32(nil), d.Attributes...)
		wrongAttribute.Attributes[0]++
		badSources = append(badSources, &wrongAttribute)
		for i := range d.Columns {
			next := *d
			next.Columns = append([]catalog.Column(nil), d.Columns...)
			next.Columns[i].Usage.Type, _ = foreignInput.Catalog.LookupType(d.Columns[i].Usage.Type.ID())
			badSources = append(badSources, &next)
		}
		for _, source := range badSources {
			bad := in
			bad.Source = source
			rows, err := core.CaptureCatalogSourceRows(bad, produce)
			statisticExtDataInvariant(t, rows == nil, err)
			if err.Error() != "stuntgres: invalid statistics data source" {
				t.Fatal("source invariant primary", err)
			}
		}
		if !maps.Equal(descriptors, maps.Collect(in.Catalog.State().Descriptors())) || !maps.Equal(names, maps.Collect(in.Catalog.State().Names())) {
			t.Fatal("capture mutated catalog roots")
		}
	})
}

func statisticExtDataInvariant(t *testing.T, nilRows bool, err error) {
	t.Helper()
	if !nilRows || err == nil {
		t.Fatal("invalid capture did not fail atomically", nilRows, err)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		t.Fatal("invariant fabricated SQLSTATE", err)
	}
}

func TestPGStatisticExtDataCaptureCancellation(t *testing.T) {
	e, _, _ := ndistinctCoreFixture(t)
	produce, ok := pgcatalog.ProducerFor(3429)
	if !ok {
		t.Fatal("DATA producer missing")
	}
	statisticExtDataInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		descriptors := maps.Collect(in.Catalog.State().Descriptors())
		names := maps.Collect(in.Catalog.State().Names())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		in.Context = ctx
		rows, err := core.CaptureCatalogSourceRows(in, produce)
		statisticExtDataInvariant(t, rows == nil, err)
		if !errors.Is(err, context.Canceled) {
			t.Fatal("capture lost cancellation", err)
		}
		if !maps.Equal(descriptors, maps.Collect(in.Catalog.State().Descriptors())) || !maps.Equal(names, maps.Collect(in.Catalog.State().Names())) {
			t.Fatal("cancellation mutated catalog roots")
		}
	})
}
