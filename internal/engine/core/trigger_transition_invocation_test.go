package core_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTriggerTransitionInvocation(t *testing.T) {
	e, s, read, _, ref := transitionRuntimeFixture(t, 1)
	transitionRuntimeQueue(t, s, ref, transitionRuntimeRow(t, read, 1, 5))
	restoreOuter := core.ScopeInstallTransitionTest(s, ref, stored.TriggerInsert, "", "x")
	shape, found := e.AnalysisHost(s.Op).LookupNamedRelation("x")
	if !found {
		t.Fatal("missing invocation namespace")
	}
	outer, err := s.WriteHost().OpenNamedRelation("x", shape.Schema)
	if err != nil {
		t.Fatal(err)
	}
	restoreCommand := s.ActivateSQLRoutine(nil, read)
	if current, ok := e.AnalysisHost(s.Op).LookupNamedRelation("x"); !ok || &current.Schema[0] != &shape.Schema[0] {
		t.Fatal("command scratch cleared invocation namespace")
	}
	if err := restoreCommand(nil); err != nil {
		t.Fatal(err)
	}
	integer, _ := read.LookupType(sqltype.Int4)
	helper := triggerUnitProcedure(t, e, s, "transition_helper", 9500020, integer, proc.OrdinaryInvocation, "BEGIN PERFORM id FROM x; RETURN 0; END")
	_, err = core.ScopeInvokePLRoutine(s, helper, &proc.Frame{}, e.CatalogRead(), sqltype.CollationRef{})
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "42P01" {
		t.Fatal("separate function inherited trigger namespace", err)
	}
	afterHelper, err := s.WriteHost().OpenNamedRelation("x", shape.Schema)
	if err != nil || &afterHelper.Row(0)[0] != &outer.Row(0)[0] {
		t.Fatal("error unwind lost outer backing", err)
	}
	finish, err := s.BeginRoutineCommand()
	if err != nil {
		t.Fatal(err)
	}
	nested, _ := core.ScopeEffectTestIDs(s)
	transitionRuntimeQueue(t, s, ref, transitionRuntimeRow(t, read, 2, 6))
	restoreNested := core.ScopeInstallTransitionTest(s, ref, stored.TriggerInsert, "", "x")
	inner, err := s.WriteHost().OpenNamedRelation("x", shape.Schema)
	if err != nil || &inner.Row(0)[0] == &outer.Row(0)[0] {
		t.Fatal("nested invocation borrowed outer accepted data", err)
	}
	restoreNested()
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	if _, retained := core.ScopeAfterTestFrames(s)[nested]; retained {
		t.Fatal("nested command retained transition frame")
	}
	afterNested, err := s.WriteHost().OpenNamedRelation("x", shape.Schema)
	if err != nil || &afterNested.Row(0)[0] != &outer.Row(0)[0] {
		t.Fatal("nested return did not restore outer data", err)
	}
	restoreOuter()
	if _, found := e.AnalysisHost(s.Op).LookupNamedRelation("x"); found {
		t.Fatal("invocation leaked binding namespace")
	}
	if _, err := s.WriteHost().OpenNamedRelation("x", shape.Schema); err == nil {
		t.Fatal("invocation leaked scan access")
	}
	if err := core.ScopeDrainAfterTest(s); err != nil {
		t.Fatal(err)
	}
	if _, found := e.AnalysisHost(s.Op).LookupNamedRelation("x"); found {
		t.Fatal("real trigger invocation leaked namespace")
	}
	t.Run("typed-empty", func(t *testing.T) {
		e, scope, _, relation, ref := transitionRuntimeFixture(t, 0)
		if err := scope.WriteHost().AfterStatement(ref, stored.TriggerInsert, nil); err != nil {
			t.Fatal(err)
		}
		leave := core.ScopeInstallTransitionTest(scope, ref, stored.TriggerInsert, "", "x")
		defer leave()
		shape, ok := e.AnalysisHost(scope.Op).LookupNamedRelation("x")
		if !ok {
			t.Fatal("empty invocation lost relation shape")
		}
		scan := &plan.NamedRelationScanNode{Name: shape.Name, QuerySchema: shape.Schema}
		state := &exec.State{Source: &exec.Source{Authority: &exec.Authority{Host: core.HostOf(scope)}}}
		first, err := exec.OpenNode(context.Background(), state, scan)
		if err != nil {
			t.Fatal(err)
		}
		second, err := exec.OpenNode(context.Background(), state, scan)
		if err != nil || first.Source == second.Source || first.Source.Count() != 0 || len(first.Columns) != len(relation.Columns) || &first.Columns[0] != &relation.Columns[0] {
			t.Fatal("empty scan lost schema or private cursor", err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		e, scope, current, relation, procedure := triggerUnitFixtureContext(t, ctx, "BEGIN PERFORM count(*) FROM x; RETURN NULL; END")
		p := triggerUnitDefinition(t, current, relation, procedure, "r", stored.TriggerInsert).Parts()
		p.Timing, p.NewTable = stored.TriggerAfter, "x"
		definition, err := stored.NewTrigger(p)
		if err != nil {
			t.Fatal(err)
		}
		relation = triggerUnitInstall(t, e, relation, definition)
		triggerUnitAdmit(t, scope, relation)
		ref := current.Owner().Domain.Ref(current.Epoch(), relation.Address())
		transitionRuntimeQueue(t, scope, ref, transitionRuntimeRow(t, current, 1, 5))
		cancel()
		if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if len(core.ScopeAfterTestFrames(scope)) != 0 {
			t.Fatal("canceled invocation retained sets")
		}
		if _, ok := e.AnalysisHost(scope.Op).LookupNamedRelation("x"); ok {
			t.Fatal("canceled invocation retained binding namespace")
		}
	})
}

func TestTriggerTransitionPreparedOwnership(t *testing.T) {
	e, s, read, relation, ref := transitionRuntimeFixture(t, 1)
	transitionRuntimeQueue(t, s, ref, transitionRuntimeRow(t, read, 1, 5))
	restore := core.ScopeInstallTransitionTest(s, ref, stored.TriggerInsert, "", "x")
	defer func() { restore() }()
	command := func(fn func() error) {
		t.Helper()
		leave := s.ActivateSQLRoutine(nil, e.CatalogRead())
		err := fn()
		if err = leave(err); err != nil {
			t.Fatal(err)
		}
	}
	command(func() error { _, err := s.ParseProtocol("transition_owned", "SELECT * FROM x", nil); return err })
	registry := core.PayloadOf(s.Op.Session).PreparedSQL
	definition := registry.Lookup("transition_owned")
	if definition == nil || definition.Description.Analysis == nil {
		t.Fatal("missing actual composed description")
	}
	description := definition.Description
	generation := description.PlanGeneration
	results := description.Results
	command(func() error {
		_, err := s.BindProtocolPortal("transition_old", "transition_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	binding := registry.Binding("transition_old")
	if binding == nil || binding.Resource == nil || binding.Resource.Root == nil {
		t.Fatal("missing actual accepted graph")
	}
	root := binding.Resource.Root
	var scan *plan.NamedRelationScanNode
	plan.VisitExecutableQuery(root, func(node plan.Node) {
		if named, ok := node.(*plan.NamedRelationScanNode); ok {
			scan = named
		}
	}, func(expr.Expr) {})
	if scan == nil || scan.Name != "x" || &scan.QuerySchema[0] != &relation.Columns[0] {
		t.Fatal("accepted scan did not retain symbolic shape")
	}
	if len(description.Analysis.Sources) != 0 || description.Analysis.Target != (ids.ObjectRef{}) {
		t.Fatal("description retained catalog source/target for invocation relation")
	}
	authority := &exec.Authority{Host: core.HostOf(s)}
	state := &exec.State{Source: &exec.Source{Authority: authority}}
	oldScan, err := exec.OpenNode(context.Background(), state, scan)
	if err != nil {
		t.Fatal(err)
	}
	again, err := exec.OpenNode(context.Background(), state, scan)
	if err != nil || oldScan.Source == again.Source || &oldScan.Source.Row(0)[0] != &again.Source.Row(0)[0] {
		t.Fatal("scans copied rows or shared mutable cursor", err)
	}
	if oldScan.Source.Authority != authority || oldScan.Source.Host != nil || oldScan.Source.LiveHost() != core.HostOf(s) {
		t.Fatal("named scan did not borrow the current execution authority slot")
	}
	// A detached authority is authoritative nil, not permission to recover a
	// stale direct Host or any data retained by a prior open.
	authority.Host = nil
	state.Source.Host = core.HostOf(s)
	if _, err := exec.OpenNode(context.Background(), state, scan); err == nil {
		t.Fatal("detached named scan recovered stale invocation access")
	}
	state.Source.Host = nil
	authority.Host = core.HostOf(s)
	if err := core.ScopeDrainAfterTest(s); err != nil {
		t.Fatal(err)
	}
	restore()
	transitionRuntimeQueue(t, s, ref, transitionRuntimeRow(t, read, 2, 6))
	restore = core.ScopeInstallTransitionTest(s, ref, stored.TriggerInsert, "", "x")
	currentScan, err := exec.OpenNode(context.Background(), state, scan)
	if err != nil || currentScan.Source.Count() != 1 || &currentScan.Source.Row(0)[0] == &oldScan.Source.Row(0)[0] {
		t.Fatal("retained leaf reused prior invocation data", err)
	}
	if binding.Resource.Root != root || definition.Description.PlanGeneration != generation || &definition.Description.Results[0] != &results[0] {
		t.Fatal("new invocation rewrote accepted graph/description generation/backing")
	}
	// A genuine descriptor shape replacement must be checked at each open;
	// retaining the old symbolic node is not permission to scan a new shape.
	changed := *relation
	changed.ObjectRevision++
	changed.Columns = slices.Clone(relation.Columns)
	changed.Columns[1].Name = "changed"
	changed.Index = map[string]int{"id": 0, "changed": 1}
	relation = triggerUnitInstall(t, e, &changed, stored.TriggersOf(relation)...)
	if err := core.ScopeDrainAfterTest(s); err != nil {
		t.Fatal(err)
	}
	restore()
	transitionRuntimeQueue(t, s, ref, transitionRuntimeRow(t, read, 3, 7))
	restore = core.ScopeInstallTransitionTest(s, ref, stored.TriggerInsert, "", "x")
	if _, err := exec.OpenNode(context.Background(), state, scan); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("old shape retained scan authority", err)
	}
	if scan.Schema()[1].Name != "v" || description.Results[1].Name != "v" {
		t.Fatal("descriptor replacement mutated prior immutable shape")
	}
	command(func() error { _, err := s.ParseProtocol("transition_new_shape", "SELECT * FROM x", nil); return err })
	if registry.Lookup("transition_new_shape").Description.Results[1].Name != "changed" {
		t.Fatal("fresh analysis did not bind current invocation shape")
	}
	command(func() error { return s.CloseProtocolPortal("transition_old") })
	if !binding.Retired {
		t.Fatal("accepted invocation resource was not retired")
	}
}
