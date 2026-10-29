package core_test

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
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

func TestCopyProgressRuleImportCollision(t *testing.T) {
	e := engine.New()
	worker := sessionStart(t, e, nil)
	sessionSQL(t, worker, "CREATE VIEW copy_progress_import AS SELECT 1 AS v")
	base := e.CatalogRead()
	source := base.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ProgressCopyOID})
	public := base.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	address := base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: public, Name: "copy_progress_import"})
	original := base.MustRelation(address)
	replacement := *original
	replacement.View = stored.ViewOf(original).With(func(parts *stored.ViewParts) { parts.Rule = pgcatalog.ProgressCopyRuleOID })
	candidate := catalog.CloneState(base.State())
	candidate.SetDescriptor(address, &replacement)
	read := e.Catalog.Read(candidate)
	if catalog.ValidateDefinitionOIDs(read) == nil {
		t.Fatal("import collision with native return rule was accepted")
	}
	next := ids.OID(900000)
	if err := control.RemapViewRuleOIDs(read, func() (ids.OID, error) { next++; return next, nil }); err != nil {
		t.Fatal(err)
	}
	imported := read.MustRelation(address)
	if imported.ReturnRuleOID() == pgcatalog.ProgressCopyRuleOID || imported.ReturnRuleOID() == original.ReturnRuleOID() {
		t.Fatal("imported executable rule did not receive fresh identity")
	}
	if read.MustRelation(source.Address()).ReturnRuleOID() != pgcatalog.ProgressCopyRuleOID || base.MustRelation(address) != original {
		t.Fatal("remap changed bootstrap/retained ownership")
	}
	if err := catalog.ValidateDefinitionOIDs(read); err != nil {
		t.Fatal(err)
	}
}
