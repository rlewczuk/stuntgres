package views_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
)

func TestCopyProgressRuleImportCollision(t *testing.T) {
	e := engine.New()
	worker := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, worker, "CREATE VIEW copy_progress_import AS SELECT 1 AS v")
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
