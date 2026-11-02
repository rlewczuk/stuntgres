package core_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestMaterializedStoragePreparedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE MATERIALIZED VIEW ms_owned AS SELECT 1 AS id", "CREATE INDEX ms_owned_idx ON ms_owned(id)", "CREATE ACCESS METHOD ms_owned_heap TYPE TABLE HANDLER heap_tableam_handler", "BEGIN"} {
		enginetest.SessionSQL(t, s, q)
	}
	defer enginetest.SessionSQL(t, s, "ROLLBACK")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("ms_select", "SELECT COALESCE(-(7::integer),0) AS folded,id FROM ms_owned", nil)
		return err
	})
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "ms_select", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("before")
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("ms_select")
	owner, analysis := definition.Description.Folds, definition.Description.Analysis
	if owner == nil || analysis == nil || owner.Analysis != analysis {
		t.Fatal("missing actual definition analysis/fold owner")
	}
	accepted := owner.Current()
	if len(accepted.Entries) == 0 {
		t.Fatal("missing demanded unary accepted backing")
	}
	savedEntries := append(accepted.Entries[:0:0], accepted.Entries...)
	savedDependencies := append([]catalog.ExpressionDependency(nil), analysis.Dependencies...)
	oldPortal := registry.Binding("before")
	if oldPortal.Resource == nil || oldPortal.Resource.Root == nil || oldPortal.Folds == nil {
		t.Fatal("missing actual accepted portal graph/group")
	}
	graph, portalGroup := oldPortal.Resource.Root, *oldPortal.Folds
	old := enginetest.PrimaryKeyUnitRelation(t, e, "ms_owned")
	capture := func() (catalog.Read, core.PreparedEnvironment) {
		op, err := s.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		env, err := e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			t.Fatal(err)
		}
		return auth.Catalog, env
	}
	read, env := capture()
	found := false
	for _, dep := range analysis.Dependencies {
		if dep.Ref.Address == old.Address() {
			found = dep.Revision == read.Get(old.Address()).Revision()
		}
	}
	if !found {
		t.Fatal("analysis omitted actual materialized relation effective revision")
	}
	stale, err := core.BeginFoldPublication(&definition.Description, definition.Body, env, read, nil)
	if err != nil || stale == nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ALTER MATERIALIZED VIEW ms_owned SET ACCESS METHOD ms_owned_heap")
	current, currentEnv := capture()
	if current.Get(old.Address()).Revision() <= old.Revision() || analysis.Valid(current, currentEnv) {
		t.Fatal("genuine method transition did not invalidate old authority")
	}
	if err := stale.Accept(t.Context(), current, currentEnv, current.Epoch()); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("stale FoldPublication admitted", err)
	}
	bind("after")
	newPortal := registry.Binding("after")
	currentAnalysis := newPortal.Entry.Description.Analysis
	if registry.Lookup("ms_select") != definition || definition.Description.Folds != owner || definition.Description.Analysis != analysis || currentAnalysis == nil || currentAnalysis == analysis || owner.Analysis != currentAnalysis || !currentAnalysis.Valid(current, currentEnv) {
		t.Fatal("new binding failed to adopt current actual authority")
	}
	method, _ := current.LookupAccessMethod("ms_owned_heap")
	found = false
	for _, dep := range currentAnalysis.Dependencies {
		if dep.Ref.Address == method.Address() {
			found = dep.Revision == method.Revision()
		}
	}
	if !found {
		t.Fatal("new accepted analysis omitted installed method provider")
	}
	if newPortal == oldPortal || newPortal.Resource.Root == graph || oldPortal.Resource.Root != graph || oldPortal.Folds.Generation != portalGroup.Generation || !reflect.DeepEqual(oldPortal.Folds.Entries, portalGroup.Entries) || !reflect.DeepEqual(analysis.Dependencies, savedDependencies) || !reflect.DeepEqual(accepted.Entries, savedEntries) || &accepted.Entries[0] != &portalGroup.Entries[0] {
		t.Fatal("new admission mutated old accepted graph/generation/backing")
	}
	enginetest.SessionSQL(t, s, "ALTER MATERIALIZED VIEW ms_owned CLUSTER ON ms_owned_idx")
	bind("cluster")
	for n, q := range []string{"ALTER MATERIALIZED VIEW ms_owned SET ACCESS METHOD ms_owned_heap", "ALTER MATERIALIZED VIEW ms_owned CLUSTER ON ms_owned_idx", "ALTER MATERIALIZED VIEW ALL IN TABLESPACE pg_default OWNED BY CURRENT_USER SET TABLESPACE pg_default NOWAIT"} {
		before, previousEnv := capture()
		previous := owner.Current()
		previousAnalysis := owner.Analysis
		index := enginetest.PrimaryKeyUnitRelation(t, e, "ms_owned_idx")
		store := e.Storage.Relations[old.Address()]
		indexRoot := e.Storage.IndexRoots[index.Address()]
		enginetest.SessionSQL(t, s, q)
		after, afterEnv := capture()
		bind([]string{"method_noop", "cluster_noop", "bulk_noop"}[n])
		if previousEnv.Private != afterEnv.Private || before.Get(old.Address()) != after.Get(old.Address()) || before.Get(index.Address()) != after.Get(index.Address()) || !previousAnalysis.Valid(before, previousEnv) || !previousAnalysis.Valid(after, afterEnv) || owner.Current().Generation != previous.Generation || &owner.Current().Entries[0] != &previous.Entries[0] || e.Storage.Relations[old.Address()] != store || e.Storage.IndexRoots[index.Address()] != indexRoot {
			t.Fatal("proven no-op churned real private journal/effective stamp/backing", q)
		}
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("ms_select") })
	if owner.Live() || oldPortal.Resource.Root != graph || oldPortal.Retired {
		t.Fatal("definition closure revoked retained accepted portal")
	}
}
