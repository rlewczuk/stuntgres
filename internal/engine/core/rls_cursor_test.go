package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func rlsCursorFixture(t *testing.T) (*core.Engine, *session.Operation, func()) {
	t.Helper()
	e, op, done, _ := policyUnitFixture(t)
	for _, sql := range []string{
		"CREATE ROLE rls_cursor_reader",
		"GRANT USAGE ON SCHEMA public TO rls_cursor_reader",
		"GRANT SELECT ON t,helper TO rls_cursor_reader",
		"INSERT INTO t VALUES(1,TRUE)",
		"INSERT INTO helper VALUES(1)",
		"CREATE POLICY p ON t USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id))",
		"ALTER TABLE t ENABLE ROW LEVEL SECURITY",
		"SET ROLE rls_cursor_reader",
	} {
		enginetest.RoleExecute(t, e, op, sql)
	}
	return e, op, done
}

// SQL is incidental fixture setup. The assertions inspect actual protocol
// publication and resource ownership, not a second SQL row oracle.
func TestRLSCursorOwnership(t *testing.T) {
	e, op, done := rlsCursorFixture(t)
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = scope.Finish(context.Canceled)
		}
	}()
	portal := core.NewPortal(core.PortalFields{})
	core.SetProtocolScopeBind(core.ScopeProtocol(scope), portal)
	_, err = scope.Execute(enginetest.Statement(t, "SELECT id FROM t ORDER BY id"), 0, func(context.Context, analyze.ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	producer := core.PortalProducer(portal)
	if producer == nil {
		t.Fatal("protocol did not install a producer")
	}
	defer core.CursorDispose(producer, context.Background())
	if len(core.ScopeRLSPrograms(scope)) != 0 || len(core.CursorRLSPrograms(producer)) != 1 {
		t.Fatal("policy owner did not transfer exactly once")
	}
	clause := &core.CursorRLSPrograms(producer)[0].Groups[0].Permissive[0]
	nested := clause.State.Source
	if nested == nil {
		t.Fatal("nested policy source was not admitted")
	}
	if clause.Evaluation.Host != nil || clause.Evaluation.Context != nil || clause.Evaluation.Input.DomainChecks != nil || clause.State.Input.DomainChecks != nil {
		t.Fatal("producer retained declaration-only adapters")
	}
	err = scope.Finish(nil)
	finished = true
	if err != nil {
		t.Fatal(err)
	}
	if clause.State.Source != nested {
		t.Fatal("ended statement released producer-owned policy pins")
	}
	if err := core.CursorReleaseProducer(producer, context.Background()); err != nil {
		t.Fatal(err)
	}
	if clause.State.Source != nil || core.CursorRLSPrograms(producer) != nil {
		t.Fatal("producer retained released policy state")
	}
	if err := core.CursorReleaseProducer(producer, context.Background()); err != nil {
		t.Fatal("repeated producer release", err)
	}
}

func TestRLSCursorPublication(t *testing.T) {
	e, op, done := rlsCursorFixture(t)
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = scope.Finish(context.Canceled)
		}
	}()
	if _, err := scope.Describe(enginetest.Statement(t, "SELECT id FROM t")); err != nil {
		t.Fatal(err)
	}
	target := roleTableRef(t, e, "t")
	core.SetScopeAuthority(scope, &exec.Authority{Host: core.HostOf(scope)})
	e.Mu.Lock()
	descriptor := e.PrivateCatalogLocked(scope.Owner.Catalog).MustRelation(target.Address)
	program, err := core.EngineCaptureRowSecurityLocked(e, scope, descriptor, authorization.PrincipalRef{}, catalog.PolicySelect, false, false)
	if err == nil {
		err = core.EngineMaterializePolicySourcesLocked(e, scope)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	clause := &program.Groups[0].Permissive[0]
	nested := clause.State.Source
	if nested == nil {
		t.Fatal("missing admitted nested source")
	}
	source := &exec.Source{RLS: program, Authority: core.ScopeAuthority(scope)}
	previous := op.Ctx
	cancelled, cancel := context.WithCancel(previous)
	cancel()
	op.Ctx = cancelled
	defer func() { op.Ctx = previous }()
	_, err = core.ScopeInstallSQLCursor(scope, &parser.DeclareCursor{}, &plan.ProjectNode{}, source, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("publication did not reject cancellation", err)
	}
	if len(core.ScopeRLSPrograms(scope)) != 1 || &core.ScopeRLSPrograms(scope)[0].RLSProgram != program || clause.State.Source != nested {
		t.Fatal("failed publication stole policy ownership")
	}
	err = scope.Finish(err)
	finished = true
	if !errors.Is(err, context.Canceled) || clause.State.Source != nil {
		t.Fatal("failed statement retained imported policy pins", err)
	}
}

func TestRLSEagerDetach(t *testing.T) {
	e, op, done := rlsCursorFixture(t)
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = scope.Finish(context.Canceled)
		}
	}()
	result, err := scope.Execute(enginetest.Statement(t, "SELECT id FROM t ORDER BY id"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows == nil {
		t.Fatal("missing eager row owner")
	}
	defer result.Rows.Close()
	rows, ok := result.Rows.(*exec.FinalRows)
	if !ok || rows.Source == nil {
		t.Fatal("unexpected eager row representation")
	}
	if rows.Source.RLS != nil || rows.Source.Authority != nil || rows.Source.Host != nil {
		t.Fatal("eager data retained borrowed admission")
	}
	if len(core.ScopeRLSPrograms(scope)) != 1 {
		t.Fatal("eager data stole statement policy ownership")
	}
	clause := &core.ScopeRLSPrograms(scope)[0].Groups[0].Permissive[0]
	if clause.State.Source == nil {
		t.Fatal("statement lost nested policy pins before finish")
	}
	err = scope.Finish(nil)
	finished = true
	if err != nil {
		t.Fatal(err)
	}
	if clause.State.Source != nil || rows.Source == nil {
		t.Fatal("eager detach confused program and data lifetimes")
	}
}

// Retained policy graphs can include pre-authority fallback hosts and distinct
// nested command authorities; neither may keep an ended opening scope alive.
func TestRLSCursorSourceAuthority(t *testing.T) {
	e, op, done, _ := policyUnitFixture(t)
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = scope.Finish(context.Canceled)
		}
	}()
	opening := core.HostOf(scope)
	borrowed := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: opening}}
	input := sqltype.InputEnvironment{DomainChecks: borrowed}
	output := sqltype.OutputEnvironment{Regclass: scope}
	fallback := &exec.Source{Host: opening}
	nested := &exec.Source{Authority: &exec.Authority{Host: opening}}
	stageSource := &exec.Source{Authority: &exec.Authority{Host: opening}}
	prefix := &exec.Source{Host: opening}
	joined := &exec.Source{Host: opening}
	root := &exec.Source{Host: opening, Input: fallback, NestedInputs: nested}
	root.Sources = []*exec.Source{root, joined}
	root.Inheritance = []exec.InheritanceQuerySegment{{Prefix: prefix}}
	root.JoinLeft, root.JoinRight = joined, fallback
	child := &exec.State{Source: nested, Input: input, Output: output, Invocation: borrowed, Outer: borrowed}
	closes := 0
	stage := &exec.Stage{Source: stageSource, Evaluation: exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: opening, Input: input, Output: output}, EvalFrame: exec.EvalFrame{Query: child, Outer: borrowed}}, Finish: func() error { closes++; return nil }}
	root.NestedStage = stage
	root.Lineage = &exec.QueryLockLineage{Stage: stageSource, Inputs: nested}
	program := core.NewRowSecurityProgram(core.RowSecurityProgramFields{RLSProgram: exec.RLSProgram{Groups: []exec.RLSRuntimeGroup{{Permissive: []exec.RLSRuntimeClause{{Evaluation: exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: opening, Input: input, Output: output}}, State: exec.State{Source: root, Input: input, Output: output, Invocation: borrowed}}}}}}})
	root.RLS = &program.RLSProgram // Borrowed edge is not a second ownership traversal.
	core.SetScopeRLSPrograms(scope, []*core.RowSecurityProgram{program})
	producer := core.NewCursor(core.CursorFields{Authority: &exec.Authority{}})
	defer core.CursorDispose(producer, context.Background())
	core.ScopeTakeRowSecurityPrograms(scope, producer)
	sources := []*exec.Source{root, fallback, nested, stageSource, prefix, joined}
	for _, source := range sources {
		if source.Authority != core.CursorAuthority(producer) || source.Host != nil || source.LiveHost() != nil {
			t.Fatal("source retained opening authority")
		}
	}
	if child.Input.DomainChecks != nil || child.Output.Regclass != nil || stage.Evaluation.Input.DomainChecks != nil || stage.Evaluation.Host != nil || stage.Evaluation.Context != nil || borrowed.Host != nil || borrowed.Context != nil {
		t.Fatal("retained child evaluation kept opening adapters")
	}
	err = scope.Finish(nil)
	finished = true
	if err != nil {
		t.Fatal(err)
	}
	if closes != 0 {
		t.Fatal("opening Finish closed producer-owned stage")
	}
	fetch, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer fetch.Finish(nil)
	core.CursorAuthority(producer).Host = core.HostOf(fetch)
	for _, source := range sources {
		if source.LiveHost() != core.HostOf(fetch) {
			t.Fatal("runtime provider did not resolve current shared FETCH authority")
		}
	}
	if err := core.CursorReleaseProducer(producer, context.Background()); err != nil {
		t.Fatal(err)
	}
	if closes != 1 {
		t.Fatal("nested stage teardown count", closes)
	}
	if err := core.CursorReleaseProducer(producer, context.Background()); err != nil || closes != 1 {
		t.Fatal("repeated teardown", closes, err)
	}
}
