package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
)

func TestStringCoreRegclassSnapshotManifest(t *testing.T) {
	eng := engine.New()
	session := enginetest.StartSession(t, eng, nil)
	enginetest.SessionSQL(t, session, "CREATE TABLE regclass_source(v INT)")
	enginetest.SessionSQL(t, session, `CREATE TABLE regclass_store(v TEXT DEFAULT btrim(' stored '),CHECK(('x'::text||'regclass_source'::regclass) IS NORMALIZED))`)
	view := eng.CatalogRead()
	ref := enginetest.CatalogRef(t, eng, view, "regclass_store")
	relation := view.MustRelation(ref.Address)
	if relation.ColumnMeta.Defaults[relation.Attributes[0]].CatalogSnapshot || !relation.Constraints.Check[0].CommentSnapshot {
		t.Fatal("stored CHECK lost REGCLASS snapshot demand or ordinary default gained it")
	}
	broken := *relation
	broken.Constraints.Check = append([]catalog.CheckConstraint(nil), relation.Constraints.Check...)
	broken.Constraints.Check[0].CommentSnapshot = false
	if err := table.ValidateChecks(view, &broken, map[string]bool{}); err == nil {
		t.Fatal("invalid stored snapshot manifest accepted")
	}
	sourceRef := enginetest.CatalogRef(t, eng, view, "regclass_source")
	source := view.MustRelation(sourceRef.Address)
	scope := &core.StatementScope{Inquiry: core.NewInquiry(core.InquiryFields{CommentCatalog: view, DeparsePath: []ids.ObjectAddress{source.Namespace()}})}
	got, err := scope.FormatRegclass(uint32(sourceRef.Address.ObjectID))
	if err != nil || got != "regclass_source" {
		t.Fatal(got, err)
	}
	got, err = scope.FormatRegclass(4294967294)
	if err != nil || got != "4294967294" {
		t.Fatal("nonexistent OID presentation", got, err)
	}
	if _, err := (&core.StatementScope{}).FormatRegclass(uint32(sourceRef.Address.ObjectID)); err == nil {
		t.Fatal("missing snapshot silently fell back")
	}
	enginetest.SessionSQL(t, session, "INSERT INTO regclass_store DEFAULT VALUES")
	enginetest.SessionSQL(t, session, "ALTER TABLE regclass_store ALTER COLUMN v TYPE TEXT")
}
