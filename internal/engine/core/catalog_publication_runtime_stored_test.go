package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/publication"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPublicationPresentedFilterOwnership(t *testing.T) {
	e, relation, p, mapping := publicationOwnerFixture(t)
	base := e.CatalogRead()
	state := catalog.CloneState(base.State())
	namespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 910010}, ObjectName: "publication_root_schema", ObjectRevision: 1, Owner: relation.Owner}
	root := *relation
	root.ObjectAddress.ObjectID = 910011
	root.ObjectName = "publication_root"
	root.ObjectNamespace = namespace.Address()
	root.RelationKind = catalog.PartitionedRelation
	root.Partition = &catalog.PartitionDefinition{Children: []ids.ObjectAddress{relation.Address()}}
	leaf := *relation
	leaf.Membership = &catalog.PartitionMembership{Parent: root.Address()}
	schema := &catalog.PublicationNamespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationNamespaceClassID, ObjectID: 910012}, ObjectRevision: 1, Publication: p.Address(), Schema: namespace.Address()}
	for _, descriptor := range []catalog.Descriptor{namespace, &root, &leaf, schema} {
		state.SetDescriptor(descriptor.Address(), descriptor)
		state.BindName(catalog.DescriptorNameKey(descriptor), descriptor.Address())
	}
	e.Mu.Lock()
	e.Catalog.State = state
	e.Mu.Unlock()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	raw := enginetest.Statement(t, "SELECT a > 0").(*parser.Select).Targets[0]
	e.Mu.Lock()
	filter, err := core.BindRetainedPredicate(e, op, auth, &leaf, raw, sqltype.InputEnvironment{}, "PUBLICATION WHERE", "WHERE")
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	refs, err := publication.ValidateFilter(context.Background(), auth.Catalog, filter, raw.SourcePosition())
	if err != nil {
		t.Fatal(err)
	}
	direct := *mapping
	direct.Filter = filter
	direct.SetFilterDependencies(refs)
	state = catalog.CloneState(state)
	state.SetDescriptor(direct.Address(), &direct)
	e.Mu.Lock()
	e.Catalog.State = state
	e.Mu.Unlock()
	presentation := filter.Presentation()
	var retained pgcatalog.PublicationTableFact
	publicationInput(t, e, func(in pgcatalog.Input, auth core.AuthorizationView) {
		runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})
		count := 0
		err := runtime.VisitPublicationTables(in.Context, in.Catalog, p, func(fact pgcatalog.PublicationTableFact) error {
			count++
			retained = fact
			return nil
		})
		if err != nil || count != 1 || retained.Relation != &leaf || retained.Filter != filter || len(retained.Columns) != 2 || &retained.Columns[0] != &leaf.Attributes[0] {
			t.Fatal("direct metadata lost captured owners", retained, count, err)
		}
		count = 0
		err = publication.VisitRelation(in.Catalog, &leaf, func(item publication.EffectiveTable) bool {
			count++
			if item.Filter != nil {
				t.Fatal("inquiry projection changed DML broad coverage")
			}
			return true
		})
		if err != nil || count != 1 {
			t.Fatal("DML coverage", count, err)
		}
		next := catalog.CloneState(in.Catalog.State())
		replacement := direct
		replacement.ObjectRevision++
		replacement.Filter = nil
		replacement.SetFilterDependencies(nil)
		next.SetDescriptor(replacement.Address(), &replacement)
		e.Catalog.State = next
	})
	publicationInput(t, e, func(in pgcatalog.Input, auth core.AuthorizationView) {
		runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth})
		if err := runtime.VisitPublicationTables(in.Context, in.Catalog, p, func(fact pgcatalog.PublicationTableFact) error {
			if fact.Filter != nil {
				t.Fatal("new capture retained retired direct filter")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	if retained.Filter != filter || filter.Presentation() != presentation || &retained.Columns[0] != &leaf.Attributes[0] {
		t.Fatal("retained direct metadata backing changed")
	}
}
