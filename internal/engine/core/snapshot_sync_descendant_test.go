package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestSnapshotSyncDescendantDemand(t *testing.T) {
	for _, route := range []string{"plain", "trigger", "check", "generation", "incoming-action"} {
		t.Run(route, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE sync_inherited_parent(id TEXT PRIMARY KEY,v INT)")
			if route=="generation"{
				enginetest.Run(t,e,"CREATE FUNCTION sync_inherited_generation(x INT) RETURNS TEXT IMMUTABLE BEGIN ATOMIC SELECT pg_export_snapshot() WHERE x IS NOT NULL; END")
				enginetest.Run(t,e,"CREATE TABLE sync_inherited_child(token TEXT GENERATED ALWAYS AS (sync_inherited_generation(v)) STORED) INHERITS(sync_inherited_parent)")
			}else{enginetest.Run(t, e, "CREATE TABLE sync_inherited_child() INHERITS(sync_inherited_parent)")}
			switch route {
			case "trigger":
				enginetest.Run(t, e, "CREATE FUNCTION sync_inherited_export() RETURNS trigger LANGUAGE plpgsql STABLE AS $$BEGIN PERFORM pg_export_snapshot();RETURN NEW;END$$")
				enginetest.Run(t, e, "CREATE TRIGGER sync_inherited_before BEFORE UPDATE OF id ON sync_inherited_child FOR EACH ROW EXECUTE FUNCTION sync_inherited_export()")
			case "check":
				enginetest.Run(t, e, "ALTER TABLE sync_inherited_child ADD CHECK(pg_export_snapshot() IS NOT NULL)")
			case "incoming-action":
				enginetest.Run(t, e, "ALTER TABLE sync_inherited_child ADD PRIMARY KEY(id)")
				enginetest.Run(t, e, "CREATE TABLE sync_inherited_fk(fk TEXT DEFAULT pg_export_snapshot() REFERENCES sync_inherited_child(id) ON UPDATE SET DEFAULT)")
			}
			for _, target := range []int{-1, 0, 1} {
				s := snapshotSyncScope(t, e, false, session.ReadCommitted)
				statement:=&parser.Update{}
				s.LockedRef = enginetest.RoleTableRef(t, e, "sync_inherited_parent")
				e.Mu.Lock()
				parent, _ := e.Catalog.State.Relation(s.LockedRef.Address)
				s.LockedDescriptor = parent
				e.Mu.Unlock()
				if err := core.ScopeAcquireInheritedMutationTargets(s, statement); err != nil { t.Fatal(err) }
				if len(s.InheritedTargets) != 1 { t.Fatal("missing admitted descendant") }
				b := analyze.Binding{Ref: s.LockedRef, Relation: parent}
				if target >= 0 { b.Targets = []int{target} }
				e.Mu.Lock()
				core.ScopeInstallExportDemandFor(s, &b, statement)
				e.Mu.Unlock()
				want := route == "check" || route == "generation" || (route == "trigger" || route == "incoming-action") && target != 1
				if core.ScopeExportRootDemand(s) != want { t.Fatalf("target %d: demand=%v want=%v", target, core.ScopeExportRootDemand(s), want) }
				if err := core.ScopeSnapshotDataPhase(s); err != nil { t.Fatal(err) }
				roots, _, _, _ := core.ScopeExportCapture(s)
				if (roots != nil) != want { t.Fatal("descendant demand not installed before coherent capture") }
			}
		})
	}
}
