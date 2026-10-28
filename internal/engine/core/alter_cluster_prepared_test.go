package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func TestAlterClusterLockPolicy(t *testing.T) {
	for _, v := range []struct {
		actions []parser.AlterTableActionKind
		want    lock.Mode
	}{
		{[]parser.AlterTableActionKind{parser.ClusterOn}, lock.ShareUpdateExclusive},
		{[]parser.AlterTableActionKind{parser.SetWithoutCluster}, lock.ShareUpdateExclusive},
		{[]parser.AlterTableActionKind{parser.SetWithoutOIDs}, lock.AccessExclusive},
		{[]parser.AlterTableActionKind{parser.ClusterOn, parser.AddColumn}, lock.AccessExclusive},
		{[]parser.AlterTableActionKind{parser.AddForeignKey, parser.ClusterOn}, lock.ShareRowExclusive},
		{[]parser.AlterTableActionKind{parser.ClusterOn, parser.AddForeignKey}, lock.ShareRowExclusive},
	} {
		a := &parser.AlterTable{}
		for _, k := range v.actions {
			a.Actions = append(a.Actions, parser.AlterTableAction{Action: k})
		}
		if got := table.AlterTableLockMode(a); got != v.want {
			t.Fatal(v.actions, got, v.want)
		}
	}
	for _, k := range []parser.AlterTableActionKind{parser.ClusterOn, parser.SetWithoutCluster, parser.SetWithoutOIDs} {
		a := parser.AlterTableAction{Action: k}
		if !table.MetadataOnlyActions([]parser.AlterTableAction{a}) || table.InheritanceRecursiveAction(&catalog.Relation{}, a, false) {
			t.Fatal("physical or recursive clustering action", k)
		}
	}
	if table.AlterActionPhase(parser.ClusterOn) != 10 || table.AlterActionPhase(parser.SetWithoutCluster) != 10 || table.AlterActionPhase(parser.SetWithoutOIDs) != 0 {
		t.Fatal("wrong provider phase")
	}
}

func TestAlterClusterPreparedOwnership(t *testing.T) {
	t.Run("exact-provider-fence", clusterExactProviderFence)
	t.Run("inherited-noop-identity", clusterInheritedIdentity)
	t.Run("prepared-owner", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TABLE cluster_owned(id int)", "CREATE INDEX cluster_owned_a ON cluster_owned(id)", "BEGIN"} {
			enginetest.SessionSQL(t, s, q)
		}
		defer enginetest.SessionSQL(t, s, "ROLLBACK")
		cycle := s.NewProtocolCycle()
		command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
		command(func(scope *core.StatementScope) error {
			description, err := scope.ParseProtocol("cluster_alter", "ALTER TABLE cluster_owned CLUSTER ON cluster_owned_a", nil)
			if err != nil {
				return err
			}
			if description.HasRows || description.Empty || len(description.ParameterOIDs) != 0 || description.Description.Parameters.Count != 0 || len(description.Description.Results) != 0 {
				t.Fatal("utility exposed result/parameter metadata")
			}
			return scope.CloseProtocolStatement("cluster_alter")
		})
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("cluster_select", "SELECT COALESCE(-(7::integer),0) AS folded,id FROM cluster_owned", nil)
			return err
		})
		bind := func(name string) {
			command(func(scope *core.StatementScope) error {
				_, err := scope.BindProtocolPortal(name, "cluster_select", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
				return err
			})
		}
		bind("before")
		registry := core.PayloadOf(s).PreparedSQL
		definition := registry.Lookup("cluster_select")
		owner := definition.Description.Folds
		analysis := definition.Description.Analysis
		if owner == nil || analysis == nil || owner.Analysis != analysis {
			t.Fatal("missing registered definition owner")
		}
		accepted := owner.Current()
		if len(accepted.Entries) == 0 {
			t.Fatal("missing accepted fold backing")
		}
		savedEntries := append(accepted.Entries[:0:0], accepted.Entries...)
		savedDependencies := append([]catalog.ExpressionDependency(nil), analysis.Dependencies...)
		oldPortal := registry.Binding("before")
		if oldPortal.Resource == nil || oldPortal.Resource.Root == nil || oldPortal.Folds == nil {
			t.Fatal("missing accepted graph/group")
		}
		graph, portalGroup := oldPortal.Resource.Root, *oldPortal.Folds
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
		old := primaryKeyUnitRelation(t, e, "cluster_owned")
		oldIndex := primaryKeyUnitRelation(t, e, "cluster_owned_a")
		found := false
		for _, dep := range analysis.Dependencies {
			if dep.Ref.Address == old.Address() {
				found = dep.Revision == old.Revision()
			}
		}
		if !found {
			t.Fatal("analysis omitted exact parent stamp")
		}
		stale, err := core.BeginFoldPublication(&definition.Description, definition.Body, env, read, nil)
		if err != nil || stale == nil {
			t.Fatal(err)
		}
		enginetest.SessionSQL(t, s, "ALTER TABLE cluster_owned CLUSTER ON cluster_owned_a")
		current, currentEnv := capture()
		next := current.MustRelation(old.Address())
		nextIndex := current.MustRelation(oldIndex.Address())
		if next.Revision() <= old.Revision() || nextIndex.Revision() <= oldIndex.Revision() || !nextIndex.SQLIndex.Clustered || oldIndex.SQLIndex.Clustered || read.Get(old.Address()) != old || &nextIndex.SQLIndex.Keys[0] != &oldIndex.SQLIndex.Keys[0] {
			t.Fatal("immutable scalar/revision transition")
		}
		if analysis.Valid(current, currentEnv) {
			t.Fatal("old analysis remained valid")
		}
		if err = stale.Accept(t.Context(), current, currentEnv, current.Epoch()); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("stale publication admitted", err)
		}
		bind("after")
		newPortal := registry.Binding("after")
		currentAnalysis := newPortal.Entry.Description.Analysis
		if registry.Lookup("cluster_select") != definition || definition.Description.Folds != owner || definition.Description.Analysis != analysis || currentAnalysis == nil || currentAnalysis == analysis || owner.Analysis != currentAnalysis || !currentAnalysis.Valid(current, currentEnv) {
			t.Fatal("actual owner failed to refresh")
		}
		if newPortal == oldPortal || newPortal.Resource.Root == graph || oldPortal.Resource.Root != graph || oldPortal.Folds.Generation != portalGroup.Generation || !reflect.DeepEqual(oldPortal.Folds.Entries, portalGroup.Entries) || !reflect.DeepEqual(analysis.Dependencies, savedDependencies) || !reflect.DeepEqual(accepted.Entries, savedEntries) || &accepted.Entries[0] != &portalGroup.Entries[0] {
			t.Fatal("accepted artifact ownership/backing changed")
		}
		for _, q := range []string{"ALTER TABLE cluster_owned CLUSTER ON cluster_owned_a", "ALTER TABLE cluster_owned SET WITHOUT OIDS"} {
			before, previousEnv := capture()
			previous := owner.Current()
			enginetest.SessionSQL(t, s, q)
			after, afterEnv := capture()
			bind(q)
			if previousEnv.Private != afterEnv.Private {
				t.Fatal("no-op replaced private catalog journal", q)
			}
			if before.Get(old.Address()) != after.Get(old.Address()) || before.Get(oldIndex.Address()) != after.Get(oldIndex.Address()) || !currentAnalysis.Valid(after, afterEnv) || !currentAnalysis.Valid(before, previousEnv) || owner.Current().Generation != previous.Generation || &owner.Current().Entries[0] != &previous.Entries[0] {
				t.Fatal("no-op changed effective stamps/backing")
			}
		}
		enginetest.SessionSQL(t, s, "ALTER TABLE cluster_owned SET WITHOUT CLUSTER")
		bind("clear")
		clear, clearEnv := capture()
		clearAnalysis := owner.Analysis
		clearAccepted := owner.Current()
		enginetest.SessionSQL(t, s, "ALTER TABLE cluster_owned SET WITHOUT CLUSTER")
		again, againEnv := capture()
		bind("clear_noop")
		if clear.Get(old.Address()) != again.Get(old.Address()) || clear.Get(oldIndex.Address()) != again.Get(oldIndex.Address()) || clearEnv.Private != againEnv.Private || !clearAnalysis.Valid(clear, clearEnv) || !clearAnalysis.Valid(again, againEnv) || owner.Current().Generation != clearAccepted.Generation || &owner.Current().Entries[0] != &clearAccepted.Entries[0] {
			t.Fatal("already-clear changed effective stamps/backing")
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("cluster_select") })
		if owner.Live() || oldPortal.Resource.Root != graph || oldPortal.Retired {
			t.Fatal("definition close revoked accepted graph")
		}
	})
	t.Run("cancel-final-install", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TABLE cluster_cancel(id int)", "CREATE INDEX cluster_cancel_a ON cluster_cancel(id)", "CREATE INDEX cluster_cancel_b ON cluster_cancel(id)", "ALTER TABLE cluster_cancel CLUSTER ON cluster_cancel_a"} {
			enginetest.SessionSQL(t, s, q)
		}
		before := e.CatalogRead()
		parent := primaryKeyUnitRelation(t, e, "cluster_cancel")
		a := primaryKeyUnitRelation(t, e, "cluster_cancel_a")
		b := primaryKeyUnitRelation(t, e, "cluster_cancel_b")
		store := e.Storage.Relations[parent.Address()]
		ra, rb := e.Storage.IndexRoots[a.Address()], e.Storage.IndexRoots[b.Address()]
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		op, err := s.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		_, err = scope.Execute(enginetest.Statement(t, "ALTER TABLE cluster_cancel CLUSTER ON cluster_cancel_b"), 0, nil)
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		journal := scope.PendingCatalog()
		if journal == nil {
			scope.Finish(context.Canceled)
			t.Fatal("missing complete private journal")
		}
		for _, old := range []*catalog.Relation{parent, a, b} {
			candidate, ok := journal.Descriptors[old.Address()].(*catalog.Relation)
			if !ok || candidate.Revision() <= old.Revision() || journal.Originals[old.Address()] != old {
				scope.Finish(context.Canceled)
				t.Fatal("missing candidate/exact predecessor", old.Name())
			}
		}
		if journal.Descriptors[a.Address()].(*catalog.Relation).SQLIndex.Clustered || !journal.Descriptors[b.Address()].(*catalog.Relation).SQLIndex.Clustered {
			scope.Finish(context.Canceled)
			t.Fatal("incomplete switch")
		}
		cancel()
		if err = scope.Finish(nil); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if e.Catalog.State != before.State() || e.CatalogRead().Get(parent.Address()) != parent || e.CatalogRead().Get(a.Address()) != a || e.CatalogRead().Get(b.Address()) != b || !a.SQLIndex.Clustered || b.SQLIndex.Clustered || e.Storage.Relations[parent.Address()] != store || e.Storage.IndexRoots[a.Address()] != ra || e.Storage.IndexRoots[b.Address()] != rb {
			t.Fatal("canceled final install published catalog/backing")
		}
	})
}
