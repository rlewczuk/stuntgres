package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/publication"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func publicationRelProducer(t *testing.T) pgcatalog.Producer {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(6106)
	if !ok || produce == nil {
		t.Fatal("missing pg_publication_rel producer")
	}
	return produce
}

func publicationRelInput(t *testing.T, e *core.Engine, body func(pgcatalog.Input, core.AuthorizationView)) {
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
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 6106}
	source := auth.Catalog.MustRelation(a)
	body(pgcatalog.Input{Context: op.Context(), Owner: auth.Owner, Catalog: auth.Catalog, Roles: auth.Roles, Ref: auth.Ref(a), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})}, auth)
}

// Bind through the normal retained-expression path; no statement is executed.
func publicationRelFilter(t *testing.T, e *core.Engine, r *catalog.Relation, text string) *stored.Policy {
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
	raw := enginetest.Statement(t, "SELECT "+text).(*parser.Select).Targets[0]
	condition, err := core.BindRetainedPredicate(e, op, auth, r, raw, sqltype.InputEnvironment{}, "PUBLICATION WHERE", "WHERE")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publication.ValidateFilter(context.Background(), auth.Catalog, condition, raw.SourcePosition()); err != nil {
		t.Fatal(err)
	}
	return condition
}

func publicationRelInstall(t *testing.T, e *core.Engine, m *catalog.PublicationRelation) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	v := e.Catalog.Current()
	state := catalog.CloneState(v.State())
	state.SetDescriptor(m.Address(), m)
	state.BindName(catalog.DescriptorNameKey(m), m.Address())
	if err := (catalog.PublicationRelationProvider{}).Validate(v.WithState(state), m); err != nil {
		t.Fatal(err)
	}
	e.Catalog.State = state
}

func publicationRelPopulated(t *testing.T) (*core.Engine, *catalog.Relation, *catalog.PublicationRelation) {
	t.Helper()
	e, r, _, original := publicationOwnerFixture(t)
	m := *original
	m.ObjectRevision++
	m.ColumnsSpecified = true
	m.SetColumns([]int32{1, 2})
	m.Filter = publicationRelFilter(t, e, r, "a > 0")
	publicationRelInstall(t, e, &m)
	return e, r, &m
}

func TestPGPublicationRelRegistrationFacts(t *testing.T) {
	e, other := engine.New(), engine.New()
	v := e.CatalogRead()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 6106}
	d := v.MustRelation(a)
	publicationRelProducer(t)
	count := 0
	for _, spec := range pgcatalog.Specs {
		if spec.OID == 6106 {
			count++
		}
	}
	if count != 1 || d.Name() != "pg_publication_rel" || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}) || d.RelationKind != catalog.CatalogRelation || d.VirtualView || d.CatalogView || catalog.SharedRelation(d) || !pgcatalog.RegisteredSource(v, d) || pgcatalog.ClassKind(v, d) != "r" || pgcatalog.ClassPersistence(v, d) != "p" || len(d.Columns) != 5 || len(d.Attributes) != 5 || len(d.Index) != 5 || len(d.ColumnMeta.CatalogAttributes) != 5 || d.ColumnMeta.HighWater != 5 {
		t.Fatal("relation registration", count, d)
	}
	names := []string{"oid", "prpubid", "prrelid", "prqual", "prattrs"}
	types := []sqltype.ID{26, 26, 26, 194, 22}
	for i, c := range d.Columns {
		typ, ok := v.LookupType(types[i])
		foreign, _ := other.CatalogRead().LookupType(types[i])
		fact := d.ColumnMeta.CatalogAttributes[i]
		collation := uint32(0)
		if i == 3 { collation = 950 }
		dimensions := int16(0)
		if i == 4 { dimensions = 1 }
		if !ok || typ == foreign || c.Name != names[i] || c.Usage.Type != typ || c.Usage.Typmod != -1 || c.Usage.Collation.ID != collation || c.Nullable != (i >= 3) || !c.NullabilityKnown || c.OriginTable != 6106 || c.OriginAttribute != int16(i+1) || d.Attributes[i] != int32(i+1) || d.Index[c.Name] != i || fact.NotNull != (i < 3) || fact.Collation != collation || fact.Dimensions != dimensions {
			t.Fatal("owned attribute", i, c, fact)
		}
	}
	foreign := other.CatalogRead().MustRelation(a)
	if d == foreign || d.Owner != e.RoleStore.Domain.Principal(10) || d.Owner == foreign.Owner || !d.Security.ACL.Explicit || len(d.Security.ACL.Entries) != 2 || len(d.Security.ColumnACL) != 0 {
		t.Fatal("owned relation authority", d.Security)
	}
	all := authorization.InsertPrivilege | authorization.SelectPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.TruncatePrivilege | authorization.ReferencesPrivilege | authorization.TriggerPrivilege | authorization.MaintainPrivilege
	owner, public := false, false
	for _, entry := range d.Security.ACL.Entries {
		if entry.Grantor != d.Owner || entry.GrantOptions != 0 {
			t.Fatal("grant authority", entry)
		}
		switch entry.Grantee {
		case authorization.ACLGrantee{Role: d.Owner}:
			if owner || entry.Privileges != all { t.Fatal("owner ACL", entry) }
			owner = true
		case authorization.ACLGrantee{Public: true}:
			if public || entry.Privileges != authorization.SelectPrivilege { t.Fatal("PUBLIC ACL", entry) }
			public = true
		default:
			t.Fatal("unexpected grant", entry)
		}
	}
	if !owner || !public { t.Fatal("missing grant") }
	rowAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)}
	row := v.MustType(rowAddress)
	arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}
	array := v.MustType(arrayAddress)
	if row.Name() != d.Name() || array.Name() != "_pg_publication_rel" || array.SQLType.Element() != row.SQLType || row.OwningRelation != a || array.OwningRelation != a || row.Owner != d.Owner || array.Owner != d.Owner || row.Namespace() != d.Namespace() || array.Namespace() != d.Namespace() {
		t.Fatal("reciprocal row/array ownership", row, array)
	}
	foreignRow, foreignArray := other.CatalogRead().MustType(rowAddress), other.CatalogRead().MustType(arrayAddress)
	if row == foreignRow || array == foreignArray || row.SQLType == foreignRow.SQLType || array.SQLType == foreignArray.SQLType {
		t.Fatal("shared type generations")
	}
	if err := core.RelationProvider.Validate(v, d); err != nil { t.Fatal(err) }
}

func TestPGPublicationRelCaptureOwnership(t *testing.T) {
	produce := publicationRelProducer(t)
	publicationRelInput(t, engine.New(), func(in pgcatalog.Input, _ core.AuthorizationView) {
		rows, err := produce(in)
		if err != nil || rows == nil || len(rows) != 0 { t.Fatal("empty producer", rows, err) }
		source, err := core.CaptureCatalogSourceRows(in, produce)
		if err != nil || source == nil || source.Anonymous == nil || len(source.Anonymous) != 0 || source.Ref != in.Ref { t.Fatal("anonymous empty wrapper", source, err) }
	})
	e, _, m := publicationRelPopulated(t)
	foreign := engine.New()
	var other pgcatalog.Input
	publicationRelInput(t, foreign, func(in pgcatalog.Input, _ core.AuthorizationView) { other = in })
	retainedRoles := e.Roles()
	aclUnitRoles(e, "publication_rel_new_root")
	publicationRelInput(t, e, func(in pgcatalog.Input, auth core.AuthorizationView) {
		descriptors, names, roles := maps.Collect(in.Catalog.State().Descriptors()), maps.Collect(in.Catalog.State().Names()), e.RoleStore.Published
		assertRoots := func(t *testing.T) {
			t.Helper()
			if !maps.Equal(descriptors, maps.Collect(e.Catalog.Current().State().Descriptors())) || !maps.Equal(names, maps.Collect(e.Catalog.Current().State().Names())) || roles != e.RoleStore.Published { t.Fatal("capture mutated accepted roots") }
		}
		rows, err := produce(in)
		if err != nil || len(rows) != 1 || len(rows[0]) != 5 { t.Fatal("populated producer", rows, err) }
		for i, cell := range rows[0] {
			if cell.Type() != in.Source.Columns[i].Usage.Type || cell.IsNull() { t.Fatal("owned populated cell", i, cell) }
		}
		for i, oid := range []ids.OID{m.Address().ObjectID, m.Publication.ObjectID, m.Relation.ObjectID} {
			want, err := sqltype.OIDValue(in.Source.Columns[i].Usage.Type, uint32(oid))
			if err != nil || !reflect.DeepEqual(rows[0][i], want) { t.Fatal("actual edge identity", i, rows[0][i], err) }
		}
		source, err := core.CaptureCatalogSourceRows(in, produce)
		if err != nil || source == nil || source.Ref != in.Ref || !reflect.DeepEqual(source.Anonymous, rows) { t.Fatal("populated shared wrapper", source, err) }
		cases := []struct { name string; mutate func(*pgcatalog.Input) }{
			{"nil context", func(b *pgcatalog.Input) { b.Context = nil }},
			{"nil owner", func(b *pgcatalog.Input) { b.Owner = nil }},
			{"foreign owner", func(b *pgcatalog.Input) { b.Owner = other.Owner }},
			{"foreign domain", func(b *pgcatalog.Input) { b.Ref = other.Owner.Domain.Ref(in.Ref.Epoch, in.Ref.Address) }},
			{"stale epoch", func(b *pgcatalog.Input) { b.Ref.Epoch-- }},
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
			{"unpaired root", func(b *pgcatalog.Input) { b.Catalog = in.Catalog.WithState(catalog.CloneState(in.Catalog.State())) }},
			{"nil source", func(b *pgcatalog.Input) { b.Source = nil }},
			{"source substitution", func(b *pgcatalog.Input) { n := *in.Source; b.Source = &n }},
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
		mutations := []struct { name string; change func(*catalog.Relation) }{
			{"short width", func(d *catalog.Relation) { d.Columns = append([]catalog.Column(nil), d.Columns[:4]...) }},
			{"long width", func(d *catalog.Relation) { d.Columns = append(append([]catalog.Column(nil), d.Columns...), d.Columns[0]) }},
			{"column name", func(d *catalog.Relation) { d.Columns = append([]catalog.Column(nil), d.Columns...); d.Columns[0].Name = "impostor" }},
			{"relation name", func(d *catalog.Relation) { d.ObjectName = "impostor" }},
			{"stable attribute", func(d *catalog.Relation) { d.Attributes = append([]int32(nil), d.Attributes...); d.Attributes[0]++ }},
		}
		for i := range in.Source.Columns {
			mutations = append(mutations, struct { name string; change func(*catalog.Relation) }{"foreign type " + in.Source.Columns[i].Name, func(d *catalog.Relation) {
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
				bad.Catalog, bad.Source = in.Catalog.WithState(state), &next
				paired := auth
				paired.Catalog = bad.Catalog
				bad.Runtime = core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: paired})
				if bad.Catalog.Get(next.Address()) != bad.Source { t.Fatal("not current malformed source") }
				raw, err := produce(bad)
				subscriptionInvariant(t, raw == nil, err)
				wrapped, err := core.CaptureCatalogSourceRows(bad, produce)
				subscriptionInvariant(t, wrapped == nil, err)
				assertRoots(t)
			})
		}
		assertRoots(t)
	})
}

func TestPGPublicationRelMalformedMapping(t *testing.T) {
	e, r, m := publicationRelPopulated(t)
	// A second genuine publication prevents the malformed row's name binding
	// from invalidating the valid predecessor edge used as the prefix witness.
	v := e.CatalogRead()
	p := *v.Get(m.Publication).(*catalog.Publication)
	p.ObjectAddress.ObjectID += 10
	p.ObjectName = "publication_rel_other"
	detachedRelation := *r
	detachedRelation.ObjectAddress.ObjectID += 20
	detachedRelation.ObjectName = "publication_rel_detached"
	state := catalog.CloneState(v.State())
	for _, d := range []catalog.Descriptor{&p, &detachedRelation} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	e.Mu.Lock()
	e.Catalog.State = state
	e.Mu.Unlock()
	detachedFilter := publicationRelFilter(t, e, &detachedRelation, "a > 0")
	foreign, foreignRelation, _, _ := publicationOwnerFixture(t)
	foreignFilter := publicationRelFilter(t, foreign, foreignRelation, "a > 0")
	produce := publicationRelProducer(t)
	publicationRelInput(t, e, func(in pgcatalog.Input, auth core.AuthorizationView) {
		descriptors, names, roles := maps.Collect(in.Catalog.State().Descriptors()), maps.Collect(in.Catalog.State().Names()), e.RoleStore.Published
		cases := []struct { name string; mutate func(*catalog.PublicationRelation) }{
			{"missing publication", func(n *catalog.PublicationRelation) { n.Publication.ObjectID++ }},
			{"missing relation", func(n *catalog.PublicationRelation) { n.Relation.ObjectID++ }},
			{"unsorted attributes", func(n *catalog.PublicationRelation) { n.SetColumns([]int32{2, 1}) }},
			{"duplicate attributes", func(n *catalog.PublicationRelation) { n.SetColumns([]int32{1, 1}) }},
			{"missing attribute", func(n *catalog.PublicationRelation) { n.SetColumns([]int32{3}) }},
			{"unspecified nonempty list", func(n *catalog.PublicationRelation) { n.ColumnsSpecified = false }},
			{"specified empty list", func(n *catalog.PublicationRelation) { n.SetColumns(nil) }},
			{"foreign filter", func(n *catalog.PublicationRelation) { n.Filter = foreignFilter }},
			{"detached filter owner", func(n *catalog.PublicationRelation) { n.Filter = detachedFilter }},
			{"detached dependencies", func(n *catalog.PublicationRelation) { n.Filter = nil; n.SetFilterDependencies([]ids.ObjectAddress{{ClassID: ids.RelationClassID, ObjectID: m.Relation.ObjectID, SubID: 1}}) }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				// Keep a valid edge alongside the malformed edge. Neither API may
				// return even a valid prefix, regardless of descriptor iteration order.
				n := *m
				n.ObjectAddress.ObjectID++
				n.Publication = p.Address()
				tc.mutate(&n)
				state := catalog.CloneState(in.Catalog.State())
				state.SetDescriptor(n.Address(), &n)
				state.BindName(catalog.DescriptorNameKey(&n), n.Address())
				bad := in
				bad.Catalog = in.Catalog.WithState(state)
				paired := auth
				paired.Catalog = bad.Catalog
				bad.Runtime = core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: paired})
				if bad.Catalog.Get(in.Source.Address()) != in.Source { t.Fatal("source substituted") }
				if err := (catalog.PublicationRelationProvider{}).Validate(bad.Catalog, m); err != nil { t.Fatal("valid prefix witness invalidated", err) }
				raw, err := produce(bad)
				subscriptionInvariant(t, raw == nil, err)
				wrapped, err := core.CaptureCatalogSourceRows(bad, produce)
				subscriptionInvariant(t, wrapped == nil, err)
				if !maps.Equal(descriptors, maps.Collect(e.Catalog.Current().State().Descriptors())) || !maps.Equal(names, maps.Collect(e.Catalog.Current().State().Names())) || roles != e.RoleStore.Published { t.Fatal("malformed capture changed accepted roots") }
			})
		}
	})
}

func TestPGPublicationRelCaptureLifetime(t *testing.T) {
	e, r, m := publicationRelPopulated(t)
	produce := publicationRelProducer(t)
	successor := *m
	successor.ObjectRevision++
	successor.SetColumns([]int32{2})
	successor.Filter = publicationRelFilter(t, e, r, "b < 0")
	var saved []sqltype.Value
	var carrier stored.PolicyCarrier
	var structural string
	publicationRelInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		rows, err := produce(in)
		if err != nil || len(rows) != 1 { t.Fatal(rows, err) }
		saved = rows[0]
		tree, err := saved[3].Tree()
		var ok bool
		carrier, ok = tree.(stored.PolicyCarrier)
		policy := stored.PolicyOf(m.Filter)
		if err != nil || !ok || carrier.Policy != policy || carrier.Policy.Expression() != policy.Expression() || carrier.Policy.Presentation() != policy.Presentation() { t.Fatal("not actual retained policy", tree, err) }
		structural, err = carrier.StructuralOutput(context.Background())
		if err != nil || structural == "" { t.Fatal("structural output", structural, err) }
		if saved[4].IsNull() || saved[4].ArrayLen() != 2 { t.Fatal("explicit vector", saved[4]) }
		for i, sub := range []int64{1, 2} {
			want, err := sqltype.Integer(saved[4].Type().Element(), sub)
			if err != nil || !reflect.DeepEqual(saved[4].ArrayElement(i), want) { t.Fatal("stable vector element", i, err) }
		}
		next := catalog.CloneState(in.Catalog.State())
		next.SetDescriptor(successor.Address(), &successor)
		if err := (catalog.PublicationRelationProvider{}).Validate(in.Catalog.WithState(next), &successor); err != nil { t.Fatal(err) }
		e.Catalog.State = next
		retained, err := produce(in)
		if err != nil || len(retained) != 1 || !reflect.DeepEqual(retained[0], saved) { t.Fatal("old capture rebound", retained, err) }
	})
	publicationRelInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		rows, err := produce(in)
		if err != nil || len(rows) != 1 { t.Fatal(rows, err) }
		tree, err := rows[0][3].Tree()
		current, ok := tree.(stored.PolicyCarrier)
		want, vectorErr := sqltype.Integer(rows[0][4].Type().Element(), 2)
		if err != nil || !ok || current.Policy != stored.PolicyOf(successor.Filter) || current.Policy == carrier.Policy || rows[0][4].ArrayLen() != 1 || vectorErr != nil || !reflect.DeepEqual(rows[0][4].ArrayElement(0), want) { t.Fatal("fresh capture did not observe successor", rows, err) }
		next := catalog.CloneState(in.Catalog.State())
		next.UnbindName(catalog.DescriptorNameKey(&successor))
		next.RemoveDescriptor(successor.Address())
		e.Catalog.State = next
	})
	publicationRelInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		rows, err := produce(in)
		if err != nil || rows == nil || len(rows) != 0 { t.Fatal("fresh capture after removal", rows, err) }
		if saved[3].Type() != in.Source.Columns[3].Usage.Type || saved[4].Type() != in.Source.Columns[4].Usage.Type { t.Fatal("retained type generation changed") }
	})
	after, err := carrier.StructuralOutput(context.Background())
	if err != nil || after != structural || carrier.Policy != stored.PolicyOf(m.Filter) || saved[4].ArrayLen() != 2 { t.Fatal("retained predecessor lost after removal", after, err) }
	for i, sub := range []int64{1, 2} {
		want, err := sqltype.Integer(saved[4].Type().Element(), sub)
		if err != nil || !reflect.DeepEqual(saved[4].ArrayElement(i), want) { t.Fatal("retained vector mutated", i, err) }
	}
}

func TestPGPublicationRelRejectedPublication(t *testing.T) {
	e, r, m := publicationRelPopulated(t)
	n := *m
	n.ObjectRevision++
	n.SetColumns([]int32{2})
	n.Filter = publicationRelFilter(t, e, r, "b < 0")
	before := e.CatalogRead()
	descriptors, names, roles := maps.Collect(before.State().Descriptors()), maps.Collect(before.State().Names()), e.RoleStore.Published
	var predecessor [][]sqltype.Value
	produce := publicationRelProducer(t)
	publicationRelInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		var err error
		predecessor, err = produce(in)
		if err != nil || len(predecessor) != 1 { t.Fatal(predecessor, err) }
	})
	stage := e.BeginCatalogStage(before)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&n); err != nil { t.Fatal(err) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil { t.Fatal(err) }
	cancel() // A real validated candidate exists before the final publish fence.
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: before.Epoch()}); !errors.Is(err, context.Canceled) { t.Fatal("canceled candidate admitted", err) }
	if e.CatalogRead().State() != before.State() || e.CatalogRead().Get(m.Address()) != m || !maps.Equal(descriptors, maps.Collect(e.CatalogRead().State().Descriptors())) || !maps.Equal(names, maps.Collect(e.CatalogRead().State().Names())) || roles != e.RoleStore.Published { t.Fatal("rejected candidate changed roots") }
	publicationRelInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		rows, err := produce(in)
		if err != nil || !reflect.DeepEqual(rows, predecessor) { t.Fatal("rejected candidate changed payload", rows, err) }
	})
}

func TestPGPublicationRelCaptureCancellation(t *testing.T) {
	e, _, _ := publicationRelPopulated(t)
	produce := publicationRelProducer(t)
	publicationRelInput(t, e, func(in pgcatalog.Input, _ core.AuthorizationView) {
		descriptors, names, roles := maps.Collect(in.Catalog.State().Descriptors()), maps.Collect(in.Catalog.State().Names()), e.RoleStore.Published
		rows, err := produce(in)
		if err != nil || len(rows) != 1 { t.Fatal("populated cancellation witness", rows, err) }
		ctx, cancel := context.WithCancel(in.Context)
		cancel()
		in.Context = ctx
		raw, err := produce(in)
		if raw != nil || !errors.Is(err, context.Canceled) { t.Fatal("canceled populated producer", raw, err) }
		wrapped, err := core.CaptureCatalogSourceRows(in, produce)
		if wrapped != nil || !errors.Is(err, context.Canceled) { t.Fatal("canceled populated wrapper", wrapped, err) }
		if !maps.Equal(descriptors, maps.Collect(e.Catalog.Current().State().Descriptors())) || !maps.Equal(names, maps.Collect(e.Catalog.Current().State().Names())) || roles != e.RoleStore.Published { t.Fatal("cancellation changed roots") }
	})
}
