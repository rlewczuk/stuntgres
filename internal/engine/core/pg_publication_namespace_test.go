package core_test

import (
	"context"
	"errors"
	"maps"
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

func publicationNamespaceInput(t *testing.T, e *core.Engine, body func(pgcatalog.Input, core.AuthorizationView)) {
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
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 6237}
	source, ok := auth.Catalog.Get(a).(*catalog.Relation)
	if !ok {
		t.Fatal("missing pg_publication_namespace descriptor")
	}
	body(pgcatalog.Input{Context: op.Context(), Owner: auth.Owner, Catalog: auth.Catalog, Roles: auth.Roles, Ref: auth.Ref(a), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})}, auth)
}

func publicationNamespaceProducer(t *testing.T) pgcatalog.Producer {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(6237)
	if !ok || produce == nil {
		t.Fatal("missing pg_publication_namespace producer")
	}
	return produce
}

func TestPGPublicationNamespaceRegistrationFacts(t *testing.T) {
	e, other := engine.New(), engine.New()
	v := e.CatalogRead()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 6237}
	d, ok := v.Get(a).(*catalog.Relation)
	if !ok {
		t.Fatal("missing pg_publication_namespace descriptor")
	}
	publicationNamespaceProducer(t)
	if d.Name() != "pg_publication_namespace" || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}) || d.RowType != 10109 || d.RelationKind != catalog.CatalogRelation || d.VirtualView || d.CatalogView || catalog.SharedRelation(d) || !d.History.HasIndexes || d.ColumnMeta.HighWater != 3 || len(d.Columns) != 3 || len(d.Attributes) != 3 || len(d.Index) != 3 || len(d.ColumnMeta.CatalogAttributes) != 3 || !pgcatalog.RegisteredSource(v, d) {
		t.Fatal("publicationNamespace registration", d)
	}
	registered := 0
	for _, spec := range pgcatalog.Specs {
		if spec.OID == 6237 {
			registered++
		}
	}
	if registered != 1 {
		t.Fatal("publicationNamespace source registration count", registered)
	}
	if pgcatalog.ClassKind(v, d) != "r" || pgcatalog.ClassPersistence(v, d) != "p" {
		t.Fatal("class kind/persistence")
	}
	names := []string{"oid", "pnpubid", "pnnspid"}
	types := []sqltype.ID{26, 26, 26}
	for i, c := range d.Columns {
		typ, ok := v.LookupType(types[i])
		foreign, _ := other.CatalogRead().LookupType(types[i])
		fact := d.ColumnMeta.CatalogAttributes[i]
		collation := uint32(0)
		if !ok || c.Name != names[i] || c.Usage.Type != typ || typ == foreign || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.Nullable || !c.NullabilityKnown || c.OriginTable != 6237 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[c.Name] != i || !fact.NotNull || fact.Collation != collation || fact.Dimensions != 0 {
			t.Fatal("publicationNamespace attribute", i, c, fact)
		}
	}
	otherRelation := other.CatalogRead().MustRelation(a)
	if d == otherRelation {
		t.Fatal("shared relation descriptor across engines")
	}
	if d.Owner != e.RoleStore.Domain.Principal(10) || d.Owner == otherRelation.Owner || !d.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 2 || len(d.Security.ColumnACL) != 0 {
		t.Fatal("bootstrap authority", d.Security)
	}
	all := authorization.InsertPrivilege | authorization.SelectPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.TruncatePrivilege | authorization.ReferencesPrivilege | authorization.TriggerPrivilege | authorization.MaintainPrivilege
	seenOwner, seenPublic := false, false
	for _, entry := range d.Security.ACL.Entries {
		if entry.Grantor != d.Owner || entry.GrantOptions != 0 {
			t.Fatal("ACL grant authority", entry)
		}
		switch entry.Grantee {
		case authorization.ACLGrantee{Role: d.Owner}:
			if seenOwner || entry.Privileges != all {
				t.Fatal("owner ACL", entry)
			}
			seenOwner = true
		case authorization.ACLGrantee{Public: true}:
			if seenPublic || entry.Privileges != authorization.SelectPrivilege {
				t.Fatal("PUBLIC ACL", entry)
			}
			seenPublic = true
		default:
			t.Fatal("unexpected ACL entry", entry)
		}
	}
	if !seenOwner || !seenPublic {
		t.Fatal("missing bootstrap grant")
	}
	row := v.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10109})
	array := v.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10108})
	if row.Name() != "pg_publication_namespace" || array.Name() != "_pg_publication_namespace" || row.Array != 10108 || array.SQLType.Element() != row.SQLType || row.OwningRelation != a || array.OwningRelation != a || row.Owner != d.Owner || array.Owner != d.Owner || row.Namespace() != d.Namespace() || array.Namespace() != d.Namespace() {
		t.Fatal("row array ownership", row, array)
	}
	otherRow := other.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10109})
	otherArray := other.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 10108})
	if row == otherRow || array == otherArray || row.SQLType == otherRow.SQLType || array.SQLType == otherArray.SQLType {
		t.Fatal("shared composite types across engines")
	}
	if err := core.RelationProvider.Validate(v, d); err != nil {
		t.Fatal(err)
	}
	oldACL := authorization.ACLValue{Explicit: true, Entries: append([]authorization.ACLEntry(nil), d.Security.ACL.Entries...)}
	otherACL := authorization.ACLValue{Explicit: true, Entries: append([]authorization.ACLEntry(nil), otherRelation.Security.ACL.Entries...)}
	ref := e.Catalog.Domain.Ref(v.Epoch(), a)
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	if !publicationNamespaceACLEqual(d.Security.ACL, oldACL) || !publicationNamespaceACLEqual(otherRelation.Security.ACL, otherACL) || len(e.CatalogRead().MustRelation(a).Security.ACL.Entries) != 0 {
		t.Fatal("ACL publicationNamespace crossed retained root/engine")
	}
	// An ACL-only successor remains a registered, coherently captured source.
	publicationNamespaceInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		rows, err := publicationNamespaceProducer(t)(in)
		if err != nil || rows == nil || len(rows) != 0 {
			t.Fatal("ACL revision capture", rows, err)
		}
	})
}

func publicationNamespaceACLEqual(a, b authorization.ACLValue) bool {
	if a.Explicit != b.Explicit || len(a.Entries) != len(b.Entries) {
		return false
	}
	for i := range a.Entries {
		if a.Entries[i] != b.Entries[i] {
			return false
		}
	}
	return true
}

func TestPGPublicationNamespaceCaptureOwnership(t *testing.T) {
	produce := publicationNamespaceProducer(t)
	e, foreign := engine.New(), engine.New()
	var other pgcatalog.Input
	publicationNamespaceInput(t, foreign, func(in pgcatalog.Input, _ core.AuthorizationView) {
		other = in
	})
	retainedRoles := e.Roles()
	aclUnitRoles(e, "publicationNamespace_new_root")
	publicationNamespaceInput(t, e, func(in pgcatalog.Input, auth core.AuthorizationView) {
		descriptors, names, roles := maps.Collect(in.Catalog.State().Descriptors()), maps.Collect(in.Catalog.State().Names()), e.RoleStore.Published
		rows, err := produce(in)
		if err != nil || rows == nil || len(rows) != 0 {
			t.Fatal("actual empty producer", rows, err)
		}
		got, err := core.CaptureCatalogSourceRows(in, produce)
		if err != nil || got == nil || got.Anonymous == nil || len(got.Anonymous) != 0 || got.Ref != in.Ref {
			t.Fatal("actual empty capture", got, err)
		}
		assertRoots := func(t *testing.T) {
			t.Helper()
			if !maps.Equal(descriptors, maps.Collect(in.Catalog.State().Descriptors())) || !maps.Equal(names, maps.Collect(in.Catalog.State().Names())) || roles != e.RoleStore.Published {
				t.Fatal("capture mutated roots")
			}
		}
		cases := []struct {
			name   string
			mutate func(*pgcatalog.Input)
		}{
			{"nil context", func(b *pgcatalog.Input) { b.Context = nil }},
			{"nil owner", func(b *pgcatalog.Input) { b.Owner = nil }},
			{"foreign owner", func(b *pgcatalog.Input) { b.Owner = other.Owner }},
			{"foreign domain", func(b *pgcatalog.Input) { b.Ref = other.Owner.Domain.Ref(in.Ref.Epoch, in.Ref.Address) }},
			{"epoch", func(b *pgcatalog.Input) { b.Ref.Epoch-- }},
			{"class", func(b *pgcatalog.Input) { b.Ref.Address.ClassID = ids.TypeClassID }},
			{"object", func(b *pgcatalog.Input) { b.Ref.Address.ObjectID++ }},
			{"subobject", func(b *pgcatalog.Input) { b.Ref.Address.SubID = 1 }},
			{"nil roles", func(b *pgcatalog.Input) { b.Roles = authorization.Read{} }},
			{"foreign roles", func(b *pgcatalog.Input) { b.Roles = other.Roles }},
			{"stale roles", func(b *pgcatalog.Input) { b.Roles = retainedRoles }},
			{"nil runtime", func(b *pgcatalog.Input) { b.Runtime = nil }},
			{"foreign runtime", func(b *pgcatalog.Input) { b.Runtime = other.Runtime }},
			{"nil engine", func(b *pgcatalog.Input) { b.Runtime = core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth}) }},
			{"foreign engine", func(b *pgcatalog.Input) { b.Runtime = core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: foreign, Auth: auth}) }},
			{"unpaired catalog root", func(b *pgcatalog.Input) { b.Catalog = in.Catalog.WithState(catalog.CloneState(in.Catalog.State())) }},
			{"nil source", func(b *pgcatalog.Input) { b.Source = nil }},
			{"replacement source", func(b *pgcatalog.Input) {
				next := *in.Source
				b.Source = &next
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				bad := in
				tc.mutate(&bad)
				raw, err := produce(bad)
				subscriptionInvariant(t, raw == nil, err)
				wrapped, err := core.CaptureCatalogSourceRows(bad, produce)
				subscriptionInvariant(t, wrapped == nil, err)
				assertRoots(t)
			})
		}
		// Structural mutations are installed into an unpublished captured root:
		// these are not all rejected merely because Source != Catalog.Get(ref).
		mutations := []struct {
			name   string
			change func(*catalog.Relation)
		}{
			{"short width", func(d *catalog.Relation) { d.Columns = append([]catalog.Column(nil), d.Columns[:2]...) }},
			{"long width", func(d *catalog.Relation) { d.Columns = append(append([]catalog.Column(nil), d.Columns...), d.Columns[0]) }},
			{"column name", func(d *catalog.Relation) {
				d.Columns = append([]catalog.Column(nil), d.Columns...)
				d.Columns[0].Name = "impostor"
			}},
			{"relation name", func(d *catalog.Relation) { d.ObjectName = "impostor" }},
			{"stable attribute", func(d *catalog.Relation) {
				d.Attributes = append([]int32(nil), d.Attributes...)
				d.Attributes[0]++
			}},
		}
		for i := range in.Source.Columns {
			mutations = append(mutations, struct {
				name   string
				change func(*catalog.Relation)
			}{"foreign type " + in.Source.Columns[i].Name, func(d *catalog.Relation) {
				d.Columns = append([]catalog.Column(nil), d.Columns...)
				d.Columns[i].Usage.Type, _ = other.Catalog.LookupType(d.Columns[i].Usage.Type.ID())
			}})
		}
		for _, tc := range mutations {
			t.Run(tc.name, func(t *testing.T) {
				next := *in.Source
				tc.change(&next)
				state := catalog.CloneState(in.Catalog.State())
				state.SetDescriptor(next.Address(), &next)
				bad := in
				bad.Catalog = in.Catalog.WithState(state)
				bad.Source = &next
				paired := auth
				paired.Recapture(bad.Catalog)
				bad.Roles = paired.Roles
				bad.Runtime = core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: paired})
				if bad.Catalog.Get(next.Address()) != bad.Source {
					t.Fatal("mutation is not current captured source")
				}
				raw, err := produce(bad)
				subscriptionInvariant(t, raw == nil, err)
				wrapped, err := core.CaptureCatalogSourceRows(bad, produce)
				subscriptionInvariant(t, wrapped == nil, err)
				assertRoots(t)
			})
		}
		unpaired := in.Catalog.WithState(catalog.CloneState(in.Catalog.State()))
		if !pgcatalog.RegisteredSource(unpaired, in.Source) {
			t.Fatal("unpaired root must remain structurally registered")
		}
		err = in.Runtime.ValidateCapture(unpaired, in.Roles, in.Ref, in.Source, 6237)
		subscriptionInvariant(t, true, err)
		err = in.Runtime.ValidateCapture(in.Catalog, retainedRoles, in.Ref, in.Source, 6237)
		subscriptionInvariant(t, true, err)
		// A coherently paired unpublished root is valid, not only the committed root.
		private := in
		private.Catalog = unpaired
		paired := auth
		paired.Recapture(private.Catalog)
		private.Roles = paired.Roles
		private.Runtime = core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: paired})
		privateRows, err := produce(private)
		if err != nil || privateRows == nil || len(privateRows) != 0 {
			t.Fatal("paired private capture", privateRows, err)
		}
		privateCapture, err := core.CaptureCatalogSourceRows(private, produce)
		if err != nil || privateCapture == nil || privateCapture.Anonymous == nil || len(privateCapture.Anonymous) != 0 || privateCapture.Ref != in.Ref {
			t.Fatal("paired private wrapped capture", privateCapture, err)
		}
		assertRoots(t)
	})
}

func TestPGPublicationNamespaceCaptureCancellation(t *testing.T) {
	e := engine.New()
	produce := publicationNamespaceProducer(t)
	publicationNamespaceInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		descriptors, names, roles := maps.Collect(in.Catalog.State().Descriptors()), maps.Collect(in.Catalog.State().Names()), e.RoleStore.Published
		ctx, cancel := context.WithCancel(in.Context)
		cancel()
		in.Context = ctx
		raw, err := produce(in)
		subscriptionInvariant(t, raw == nil, err)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		wrapped, err := core.CaptureCatalogSourceRows(in, produce)
		subscriptionInvariant(t, wrapped == nil, err)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if !maps.Equal(descriptors, maps.Collect(in.Catalog.State().Descriptors())) || !maps.Equal(names, maps.Collect(in.Catalog.State().Names())) || roles != e.RoleStore.Published {
			t.Fatal("cancellation mutated roots")
		}
	})
}
