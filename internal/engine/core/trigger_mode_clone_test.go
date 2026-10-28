package core_test

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/stored"
	"testing"
)

func TestTriggerModeCloneOwnership(t *testing.T) {
	e, s := triggerModeUnitSetup(t)
	for _, q := range []string{
		"CREATE TABLE mode_tree(id integer,v integer) PARTITION BY RANGE(id)",
		"CREATE TABLE mode_mid PARTITION OF mode_tree FOR VALUES FROM(0) TO(100) PARTITION BY RANGE(id)",
		"CREATE TABLE mode_leaf PARTITION OF mode_mid FOR VALUES FROM(0) TO(100)",
		"CREATE TRIGGER a BEFORE INSERT ON mode_tree FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION mode_fn('kept')",
	} {
		sessionSQL(t, s, q)
	}
	ref := enginetest.RoleTableRef(t, e, "mode_leaf")
	old := e.CatalogRead().MustRelation(ref.Address)
	before := stored.TriggersOf(old)[0]
	parts := before.Parts()
	sessionSQL(t, s, "ALTER TABLE ONLY mode_tree DISABLE TRIGGER a")
	leaf := e.CatalogRead().MustRelation(ref.Address)
	got := stored.TriggersOf(leaf)[0]
	if got.Mode() != catalog.TriggerOrigin || got.Parts().Binding != parts.Binding || got.Parts().Presentation != parts.Presentation || &got.Parts().Arguments[0] != &parts.Arguments[0] {
		t.Fatal("ONLY mode rebuilt clone executable owner")
	}
	sessionSQL(t, s, "ALTER TABLE mode_tree DISABLE TRIGGER a")
	got = stored.TriggersOf(e.CatalogRead().MustRelation(ref.Address))[0]
	if got.Mode() != catalog.TriggerDisabled || got.Parts().Binding != parts.Binding || got.Parts().Presentation != parts.Presentation {
		t.Fatal("recursive mode rebuilt clone recipe")
	}
	sessionSQL(t, s, "ALTER TABLE mode_leaf ENABLE ALWAYS TRIGGER a")
	divergent := stored.TriggersOf(e.CatalogRead().MustRelation(ref.Address))[0]
	sessionSQL(t, s, "ALTER TABLE mode_tree ADD CONSTRAINT mode_mixed CHECK(v >= 0), DISABLE TRIGGER a")
	got = stored.TriggersOf(e.CatalogRead().MustRelation(ref.Address))[0]
	if got.Mode() != catalog.TriggerDisabled || got.OID() != before.OID() || got.Parts().Parent != parts.Parent {
		t.Fatal("mixed inherited stage lost explicit mode/identity")
	}
	if divergent.Mode() != catalog.TriggerAlways || before.Mode() != catalog.TriggerOrigin || old != nil && stored.TriggersOf(old)[0].Parts().Binding != parts.Binding {
		t.Fatal("retained clone ownership mutated")
	}
	// Traditional descendants retain their own independently owned mode envelope.
	for _, q := range []string{
		"CREATE TABLE mode_old(id integer,v integer)", "CREATE TABLE mode_inherited() INHERITS(mode_old)",
		"CREATE TRIGGER a BEFORE INSERT ON mode_old FOR EACH ROW EXECUTE FUNCTION mode_fn()",
		"CREATE TRIGGER a BEFORE INSERT ON mode_inherited FOR EACH ROW EXECUTE FUNCTION mode_fn()",
	} {
		sessionSQL(t, s, q)
	}
	childRef := enginetest.RoleTableRef(t, e, "mode_inherited")
	childBefore := e.CatalogRead().MustRelation(childRef.Address)
	sessionSQL(t, s, "ALTER TABLE mode_old ADD COLUMN extra integer, DISABLE TRIGGER a")
	child := e.CatalogRead().MustRelation(childRef.Address)
	if child == childBefore || stored.TriggersOf(child)[0].Mode() != catalog.TriggerOrigin {
		t.Fatal("traditional descendant received root trigger action")
	}
}
