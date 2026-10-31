package core_test

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/regobj"
)

func copyProgressRuleMetadata(t *testing.T, e *core.Engine, read catalog.Read) *catalog.Relation {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ProgressCopyOID}
	source := read.MustRelation(address)
	rule := ids.ObjectAddress{ClassID: 2618, ObjectID: pgcatalog.ProgressCopyRuleOID}
	if source.View != nil || source.CatalogReturnRule != rule.ObjectID || source.ReturnRuleOID() != rule.ObjectID || !pgcatalog.RegisteredSource(read, source) {
		t.Fatal("bootstrap rule became missing/executable", source)
	}
	if !catalog.DefinitionOIDUsed(source, rule.ObjectID) || !slices.Contains(catalog.RelationPayloadAddresses(source), rule) {
		t.Fatal("unreserved/unowned return rule")
	}
	if err := catalog.ValidateDefinitionOIDs(read); err != nil {
		t.Fatal("colliding native rule identity", err)
	}
	if regobj.OwnedAddressByName(source, 2618, "_RETURN") != rule.ObjectID {
		t.Fatal("rule name lookup lost identity")
	}
	identity, err := regobj.IdentifyAddress(read, e.Roles(), nil, rule, ids.SessionToken{})
	if err != nil || !identity.Found || len(identity.Names) != 3 || identity.Names[2] != "_RETURN" {
		t.Fatal("rule address lookup", identity, err)
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), read, core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e}))
	if err != nil {
		t.Fatal(err)
	}
	var owned []pgcatalog.DependEdge
	for _, edge := range edges {
		if edge.Dependent == rule {
			owned = append(owned, edge)
		}
	}
	expected := []pgcatalog.DependEdge{{Dependent: rule, Referenced: address, DepType: 'i'}}
	if !reflect.DeepEqual(owned, expected) {
		t.Fatalf("released return-rule dependency bag=%+v expected=%+v", owned, expected)
	}
	return source
}

func TestCopyProgressRuleMetadata(t *testing.T) {
	e := engine.New()
	oldRead := e.CatalogRead()
	old := copyProgressRuleMetadata(t, e, oldRead)
	copied := *old
	copied.ObjectRevision++
	if copied.ReturnRuleOID() != old.ReturnRuleOID() {
		t.Fatal("copy-on-write lost metadata")
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if fresh := copyProgressRuleMetadata(t, e, e.CatalogRead()); fresh == old {
		t.Fatal("reset reused descriptor")
	}
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	copyProgressRuleMetadata(t, e, e.CatalogRead())
	if oldRead.MustRelation(old.Address()) != old || old.ReturnRuleOID() != pgcatalog.ProgressCopyRuleOID {
		t.Fatal("replacement altered retained rule identity")
	}
}
