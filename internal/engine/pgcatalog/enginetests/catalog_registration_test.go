package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
)

func TestCatalogGrantRegistration(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	for _, spec := range pgcatalog.Specs {
		source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: spec.OID})
		if !pgcatalog.RegisteredSource(view, source) {
			t.Fatalf("registered source rejected: %s", spec.Name)
		}
		// ACL publication replaces descriptors, not registration identity. A
		// captured old descriptor remains valid only with its own snapshot.
		next := *source
		next.ObjectRevision++
		next.Security.ACL = authorization.ACLValue{Explicit: true}
		state := catalog.CloneState(view.State())
		state.SetDescriptor(next.Address(), &next)
		changed := view.WithState(state)
		if !pgcatalog.RegisteredSource(changed, &next) || !pgcatalog.RegisteredSource(view, source) || pgcatalog.RegisteredSource(changed, source) || pgcatalog.RegisteredSource(view, &next) {
			t.Fatalf("ACL replacement lost snapshot identity: %s", spec.Name)
		}
	}
	if pgcatalog.RegisteredSource(catalog.Read{}, nil) || pgcatalog.RegisteredSource(view, nil) {
		t.Fatal("missing source admitted")
	}
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TransformOID})
	foreign := engine.New().CatalogRead()
	for _, tc := range []struct {
		name string
		edit func(*catalog.Relation)
	}{
		{"unregistered", func(d *catalog.Relation) { d.ObjectAddress.ObjectID = 999999 }},
		{"wrong_class", func(d *catalog.Relation) { d.ObjectAddress.ClassID = ids.TypeClassID }},
		{"subobject", func(d *catalog.Relation) { d.ObjectAddress.SubID = 1 }},
		{"namespace", func(d *catalog.Relation) { d.ObjectNamespace.ObjectID++ }},
		{"name", func(d *catalog.Relation) { d.ObjectName = "pg_shadow" }},
		{"kind", func(d *catalog.Relation) { d.RelationKind = catalog.TableRelation }},
		{"virtual", func(d *catalog.Relation) { d.VirtualView = !d.VirtualView }},
		{"view_kind", func(d *catalog.Relation) { d.CatalogView = !d.CatalogView }},
		{"missing_column", func(d *catalog.Relation) { d.Columns = d.Columns[:len(d.Columns)-1] }},
		{"column_name", func(d *catalog.Relation) { d.Columns[0].Name = "impostor" }},
		{"column_origin", func(d *catalog.Relation) { d.Columns[0].OriginTable++ }},
		{"column_attribute", func(d *catalog.Relation) { d.Columns[0].OriginAttribute++ }},
		{"stable_attribute", func(d *catalog.Relation) { d.Attributes[0]++ }},
		{"foreign_type", func(d *catalog.Relation) {
			d.Columns[0].Usage.Type, _ = foreign.LookupType(d.Columns[0].Usage.Type.ID())
		}},
		{"column_index", func(d *catalog.Relation) { d.Index[d.Columns[0].Name] = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := *source
			next.Columns = append([]catalog.Column(nil), source.Columns...)
			next.Attributes = append([]int32(nil), source.Attributes...)
			next.Index = make(map[string]int, len(source.Index))
			for name, index := range source.Index {
				next.Index[name] = index
			}
			tc.edit(&next)
			state := catalog.CloneState(view.State())
			state.SetDescriptor(next.Address(), &next)
			state.BindName(catalog.DescriptorNameKey(&next), next.Address())
			if pgcatalog.RegisteredSource(view.WithState(state), &next) {
				t.Fatal("synthetic registration admitted")
			}
		})
	}
}

func TestCatalogBuiltinViewDefinitionInventory(t *testing.T) {
	view := engine.New().CatalogRead()
	for _, descriptor := range view.Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.CatalogRelation || pgcatalog.ClassKind(view, relation) != "v" {
			continue
		}
		oid := relation.Address().ObjectID
		if pgcatalog.SummaryViewDefinition(oid) == "" {
			t.Errorf("builtin view %s (%d) has no canonical definition", relation.Name(), oid)
		}
		definition := pgcatalog.BuiltinViewDefinition(oid, false, func(name pgcatalog.BuiltinViewName) string {
			return name.Name
		})
		if definition == "" {
			t.Errorf("builtin view %s (%d) has no formatted definition", relation.Name(), oid)
		}
	}
	relation := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.PublicationTablesOID})
	if !pgcatalog.RegisteredSource(view, relation) {
		t.Fatal("publication view lost source registration")
	}
	if producer, ok := pgcatalog.ProducerFor(pgcatalog.PublicationTablesOID); !ok || producer == nil {
		t.Fatal("publication view lost native producer")
	}
	if pgcatalog.BuiltinViewEvents(pgcatalog.PublicationTablesOID, nil) != 0 {
		t.Fatal("publication view acquired write authority")
	}
}
