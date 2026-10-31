package core_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
)

func TestDropClosureRetiredDiamond(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION closure_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$")
	enginetest.Run(t, e, "CREATE EVENT TRIGGER closure_drop ON sql_drop EXECUTE FUNCTION closure_drop()")
	for _, sql := range []string{
		"CREATE TABLE closure_top(i integer)",
		"CREATE TABLE closure_left() INHERITS(closure_top)",
		"CREATE TABLE closure_right() INHERITS(closure_top)",
		"CREATE TABLE closure_bottom() INHERITS(closure_left,closure_right)",
		"CREATE VIEW closure_consumer AS SELECT i FROM closure_bottom",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
	var roots []ids.ObjectAddress
	for _, name := range []string{"closure_top", "closure_left", "closure_right", "closure_bottom"} {
		address := enginetest.RoleTableRef(t, e, name).Address
		roots = append(roots, address)
		reader.MarkDropRoots([]ids.ObjectAddress{address})
		if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), address)); err != nil {
			t.Fatal(err)
		}
	}
	consumer := enginetest.RoleTableRef(t, e, "closure_consumer").Address
	before := stage.RemovalCount()
	edges, err := view.DropClosure(reader, append(roots, roots[0]))
	if err != nil {
		t.Fatal(err)
	}
	counts := map[ids.ObjectAddress]int{}
	for _, edge := range edges {
		counts[edge.Address]++
		if counts[edge.Address] != 1 || slices.Contains(roots, edge.Address) {
			t.Fatal("duplicate/retired result", edge)
		}
	}
	if counts[consumer] != 1 || !stage.DropFlags[consumer].Normal || stage.DropFlags[consumer].Original || stage.RemovalCount() != before {
		t.Fatal("retired diamond lost live reverse owner or created effects", edges)
	}
	for i, address := range roots {
		if got := stage.DropFlags[address]; !got.Original || got.Normal != (i != 0) {
			t.Fatal("diamond provenance", i, got)
		}
	}
}

func TestDropClosurePassiveViewGuard(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE FUNCTION closure_drop() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$",
		"CREATE EVENT TRIGGER closure_drop ON sql_drop EXECUTE FUNCTION closure_drop()",
		"CREATE DOMAIN closure_domain AS integer",
		"CREATE TABLE closure_source(c closure_domain)",
		"CREATE VIEW closure_passive AS SELECT c FROM closure_source",
		"CREATE VIEW closure_explicit AS SELECT NULL::closure_domain AS c",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	source := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_source").Address)
	domain := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.Columns[0].Usage.Type.ID())}
	passive := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_passive").Address)
	explicit := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_explicit").Address)
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	reader := core.StageDropProvider(context.Background(), stage, nil, nil, nil)
	for _, relation := range []*catalog.Relation{passive, explicit} {
		got, err := view.PassiveTypeDependency(reader, relation, domain)
		if err != nil || got != (relation == passive) {
			t.Fatal("actual view dependency classification", relation.Name(), got, err)
		}
	}
	edges, err := view.DropClosure(reader, []ids.ObjectAddress{domain})
	if err != nil {
		t.Fatal(err)
	}
	found := map[ids.ObjectAddress]bool{}
	for _, edge := range edges {
		if edge.Address == passive.Address() || edge.Address == explicit.Address() {
			found[edge.Address] = true
			if !edge.Normal || (edge.Target == domain) != (edge.Address == explicit.Address()) {
				t.Fatal("passive capture displaced genuine source-column owner", edge)
			}
		}
	}
	if !found[passive.Address()] || !found[explicit.Address()] || stage.DropFlags[source.Address()].Normal {
		t.Fatal("view reach lost or subobject flags leaked upward", edges)
	}
	for _, relation := range []*catalog.Relation{passive, explicit} {
		if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), relation.Address())); err != nil {
			t.Fatal(err)
		}
		got, err := view.PassiveTypeDependency(reader, relation, domain)
		if err != nil || got != (relation == passive) {
			t.Fatal("retirement bypassed passive-type classification", got, err)
		}
	}
	retiredEdges, err := view.DropClosure(reader, []ids.ObjectAddress{domain, passive.Address(), explicit.Address()})
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range retiredEdges {
		if edge.Relation != nil && (edge.Relation.Address() == passive.Address() || edge.Relation.Address() == explicit.Address()) {
			t.Fatal("retired view or output payload became an effect", edge)
		}
	}
}
