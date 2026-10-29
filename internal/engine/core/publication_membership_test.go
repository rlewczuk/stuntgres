package core_test

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/publication"
	"testing"
)

func TestPublicationMembershipResolution(t *testing.T) {
	e, r, p, m := publicationOwnerFixture(t)
	base := e.CatalogRead()
	state := catalog.CloneState(base.State())
	child := *r
	child.ObjectAddress.ObjectID += 10
	child.ObjectName = "ordinary_child"
	child.Inheritance = &catalog.TableInheritance{Parents: []catalog.InheritanceParent{{Relation: r.Address(), Seqno: 1}}}
	install := func(d catalog.Descriptor) {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	install(&child)
	members := func(v catalog.Read, p *catalog.Publication) []ids.OID {
		t.Helper()
		var got []ids.OID
		if err := publication.VisitTables(v, p, func(relation *catalog.Relation) bool {
			got = append(got, relation.Address().ObjectID)
			return true
		}); err != nil {
			t.Fatal(err)
		}
		slices.Sort(got)
		return got
	}
	view := base.WithState(state)
	if got := members(view, p); !slices.Equal(got, []ids.OID{r.Address().ObjectID}) {
		t.Fatal("explicit edge dynamically inherited", got)
	}
	schema := &catalog.PublicationNamespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationNamespaceClassID, ObjectID: 910100}, ObjectRevision: 1, Publication: p.Address(), Schema: r.Namespace()}
	install(schema)
	view = base.WithState(state)
	if got := members(view, p); !slices.Equal(got, []ids.OID{r.Address().ObjectID, child.Address().ObjectID}) {
		t.Fatal("schema union", got)
	}
	future := child
	future.ObjectAddress.ObjectID++
	future.ObjectName = "future"
	install(&future)
	unlogged := child
	unlogged.ObjectAddress.ObjectID += 2
	unlogged.ObjectName = "unlogged"
	unlogged.Persistence = "unlogged"
	install(&unlogged)
	view = base.WithState(state)
	if got := members(view, p); !slices.Equal(got, []ids.OID{r.Address().ObjectID, child.Address().ObjectID, future.Address().ObjectID}) {
		t.Fatal("dynamic eligibility/future", got)
	}
	if got := members(base, p); !slices.Equal(got, []ids.OID{r.Address().ObjectID}) {
		t.Fatal("old view changed", got)
	}
	state = catalog.CloneState(base.State())
	state.RemoveDescriptor(m.Address())
	state.UnbindName(catalog.DescriptorNameKey(m))
	all := *p
	all.AllTables = true
	all.ObjectRevision++
	install(&all)
	install(&child)
	install(&unlogged)
	view = base.WithState(state)
	if got := members(view, &all); !slices.Equal(got, []ids.OID{r.Address().ObjectID, child.Address().ObjectID}) {
		t.Fatal("all-table eligibility", got)
	}
	for range view.PublicationRelations() {
		t.Fatal("all-table coverage fabricated an explicit mapping")
	}
	// Partition descendants are implicit real edges and remain dynamic without
	// fabricating explicit rows. Via-root selects the genuine presentation owner.
	state = catalog.CloneState(base.State())
	root := *r
	root.RelationKind = catalog.PartitionedRelation
	root.Partition = &catalog.PartitionDefinition{Children: []ids.ObjectAddress{child.Address()}}
	install(&root)
	leaf := child
	leaf.Inheritance = nil
	leaf.Membership = &catalog.PartitionMembership{Parent: root.Address()}
	install(&leaf)
	view = base.WithState(state)
	if got := members(view, p); !slices.Equal(got, []ids.OID{leaf.Address().ObjectID}) {
		t.Fatal("partition expansion", got)
	}
	rooted := *p
	rooted.ViaRoot = true
	rooted.ObjectRevision++
	install(&rooted)
	view = base.WithState(state)
	if got := members(view, &rooted); !slices.Equal(got, []ids.OID{root.Address().ObjectID}) {
		t.Fatal("root presentation", got)
	}
	count := 0
	if err := publication.VisitRelation(view, &leaf, func(item publication.EffectiveTable) bool {
		count++
		if item.Source != &root || item.Relation != &root || item.Mapping != m || item.ColumnsSpecified || item.Columns != nil {
			t.Fatal("identity borrowed wrong definition", item)
		}
		return true
	}); err != nil || count != 1 {
		t.Fatal("leaf admission", count, err)
	}
	explicit := 0
	for range view.PublicationRelations() {
		explicit++
	}
	if explicit != 1 {
		t.Fatal("fabricated partition mapping", explicit)
	}
}
