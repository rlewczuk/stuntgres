package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestPublicationMembershipRuntimeCapture(t *testing.T) {
	e, relation, p, _ := publicationOwnerFixture(t)
	var retained [][]sqltype.Value
	publicationInput(t, e, func(in pgcatalog.Input, auth core.AuthorizationView) {
		runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})
		var facts []pgcatalog.PublicationTableFact
		if err := runtime.VisitPublicationTables(in.Context, in.Catalog, p, func(fact pgcatalog.PublicationTableFact) error { facts = append(facts, fact); return nil }); err != nil {
			t.Fatal(err)
		}
		if len(facts) != 1 || facts[0].Relation != relation || !slices.Equal(facts[0].Columns, []int32{1, 2}) || facts[0].Filter != nil {
			t.Fatal("actual domain facts", facts)
		}
		consume := func(pgcatalog.PublicationTableFact) error { return nil }
		if err := runtime.VisitPublicationTables(in.Context, in.Catalog.WithState(catalog.CloneState(in.Catalog.State())), p, consume); err == nil {
			t.Fatal("unpaired root admitted")
		}
		detached := *p
		if err := runtime.VisitPublicationTables(in.Context, in.Catalog, &detached, consume); err == nil {
			t.Fatal("detached publication authority admitted")
		}
		missing := core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth})
		if err := missing.VisitPublicationTables(in.Context, in.Catalog, p, consume); err == nil {
			t.Fatal("missing engine admitted")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := runtime.VisitPublicationTables(ctx, in.Catalog, p, consume); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled membership capture", err)
		}
		sentinel := errors.New("serialization rejected")
		if err := runtime.VisitPublicationTables(in.Context, in.Catalog, p, func(pgcatalog.PublicationTableFact) error { return sentinel }); !errors.Is(err, sentinel) {
			t.Fatal("serialization error lost", err)
		}
		source := in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.PublicationTablesOID})
		in.Source = source
		in.Ref = auth.Ref(source.Address())
		in.Runtime = runtime
		produce, ok := pgcatalog.ProducerFor(pgcatalog.PublicationTablesOID)
		if !ok {
			t.Fatal("missing effective membership producer")
		}
		rows, err := produce(in)
		if err != nil || len(rows) != 1 {
			t.Fatal("effective source", rows, err)
		}
		retained = rows
		for i, want := range []string{p.Name(), "public", relation.Name()} {
			got, err := rows[0][i].Text()
			if err != nil || got != want {
				t.Fatal("typed effective source identity", i, got, err)
			}
		}
		if rows[0][3].ArrayLen() != 2 || !rows[0][4].IsNull() {
			t.Fatal("effective projection/filter", rows[0])
		}
		next := catalog.CloneState(in.Catalog.State())
		renamed := *relation
		renamed.ObjectName = "publication_runtime_current"
		renamed.ObjectRevision++
		next.UnbindName(catalog.DescriptorNameKey(relation))
		next.SetDescriptor(renamed.Address(), &renamed)
		next.BindName(catalog.DescriptorNameKey(&renamed), renamed.Address())
		e.Catalog.State = next
		bad := in
		bad.Catalog = in.Catalog.WithState(next)
		if rows, err := produce(bad); err == nil || rows != nil {
			t.Fatal("unpaired effective source admitted", rows, err)
		}
	})
	publicationInput(t, e, func(in pgcatalog.Input, auth core.AuthorizationView) {
		source := in.Catalog.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.PublicationTablesOID})
		in.Source = source
		in.Ref = auth.Ref(source.Address())
		produce, _ := pgcatalog.ProducerFor(pgcatalog.PublicationTablesOID)
		rows, err := produce(in)
		if err != nil || len(rows) != 1 {
			t.Fatal(rows, err)
		}
		name, _ := rows[0][2].Text()
		if name != "publication_runtime_current" {
			t.Fatal("new membership capture stale", name)
		}
	})
	name, _ := retained[0][2].Text()
	if name != relation.Name() {
		t.Fatal("retained row backing changed", name)
	}
}

func TestPublicationPresentedRootColumns(t *testing.T) {
	for _, leafFirst := range []bool{false, true} {
		name := "root_first"
		if leafFirst {
			name = "leaf_first"
		}
		t.Run(name, func(t *testing.T) {
			e, relation, p, mapping := publicationOwnerFixture(t)
			state := catalog.CloneState(e.CatalogRead().State())
			root := *relation
			root.RelationKind = catalog.PartitionedRelation
			leaf := *relation
			leaf.ObjectAddress.ObjectID += 10
			if leafFirst {
				root.ObjectAddress.ObjectID, leaf.ObjectAddress.ObjectID = leaf.ObjectAddress.ObjectID, root.ObjectAddress.ObjectID
			}
			root.ObjectName, leaf.ObjectName = "presented_root", "presented_leaf"
			root.Partition = &catalog.PartitionDefinition{Children: []ids.ObjectAddress{leaf.Address()}}
			leaf.Membership = &catalog.PartitionMembership{Parent: root.Address()}
			rooted := *p
			rooted.ViaRoot = true
			rootMap := *mapping
			rootMap.Relation = root.Address()
			rootMap.ColumnsSpecified = true
			rootMap.SetColumns([]int32{2})
			leafMap := rootMap
			leafMap.ObjectAddress.ObjectID += 10
			leafMap.Relation = leaf.Address()
			leafMap.SetColumns([]int32{1})
			state.UnbindName(catalog.DescriptorNameKey(relation))
			for _, descriptor := range []catalog.Descriptor{&root, &leaf, &rooted, &rootMap, &leafMap} {
				state.SetDescriptor(descriptor.Address(), descriptor)
				state.BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
			}
			e.Mu.Lock()
			e.Catalog.State = state
			e.Mu.Unlock()
			publicationInput(t, e, func(in pgcatalog.Input, auth core.AuthorizationView) {
				runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})
				count := 0
				err := runtime.VisitPublicationTables(in.Context, in.Catalog, &rooted, func(fact pgcatalog.PublicationTableFact) error {
					count++
					if fact.Relation != &root || !slices.Equal(fact.Columns, []int32{2}) || &fact.Columns[0] != &rootMap.Columns[0] {
						t.Fatal("presented root borrowed another mapping", fact)
					}
					return nil
				})
				if err != nil || count != 1 {
					t.Fatal("deduplicated root projection", count, err)
				}
			})
		})
	}
}
