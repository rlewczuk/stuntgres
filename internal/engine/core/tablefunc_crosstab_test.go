package core_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func crosstabUnitInstalled(t *testing.T, e *core.Engine) {
	t.Helper()
	enginetest.Run(t, e, "CREATE EXTENSION tablefunc")
}
func crosstabUnitExpression(t *testing.T, e *core.Engine, name, source string, extra ...parser.Expr) plan.InstalledTablefuncCrosstabExpr {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: source}}}}
	raw.Args = append(raw.Args, extra...)
	node, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledTablefuncCrosstabExpr)
}

func TestTablefuncCrosstabExpressionIdentity(t *testing.T) {
	e := engine.New()
	crosstabUnitInstalled(t, e)
	n := crosstabUnitExpression(t, e, "crosstab3", "SELECT 'g'::text,0,'v'::text")
	if err := stored.ValidateInstalledTablefuncCrosstab(n, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := n.Rebuild(n.Info, n.Children())
	if err != nil {
		t.Fatal(err)
	}
	next := rebuilt.(plan.InstalledTablefuncCrosstabExpr)
	if next.Ref != n.Ref || next.Procedure != n.Procedure || next.RoutineReference != n.Procedure || next.Usage != n.Usage || !next.SameSourceOccurrence(n) || !next.SameGroup(n, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) {
		t.Fatal("rebuild lost capture", next)
	}
	info := n.Info
	info.RoutineReference = nil
	if _, err := n.Rebuild(info, n.Children()); err == nil {
		t.Fatal("lost routine reference accepted")
	}
	if _, err := n.Rebuild(n.Info, nil); err == nil {
		t.Fatal("malformed children accepted")
	}
	foreign := engine.New()
	crosstabUnitInstalled(t, foreign)
	if err := stored.ValidateInstalledTablefuncCrosstab(n, foreign.CatalogRead()); err == nil {
		t.Fatal("foreign same-number provider accepted")
	}
	mapped, changed, err := stored.MapExpression(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil, nil)
	if err != nil || changed || !reflect.DeepEqual(mapped, n) {
		t.Fatal("unchanged map lost sharing", changed, err)
	}
	// A same-address descriptor copy is not a genuine named result generation.
	result := *n.Usage.Type
	bad := n
	bad.Usage.Type = &result
	if err := stored.ValidateInstalledTablefuncCrosstab(bad, e.CatalogRead()); err == nil {
		t.Fatal("foreign named result generation accepted")
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Display.Source = "connectby_text" },
		func(p *catalog.Procedure) { p.Owner = foreign.CatalogRead().MustProcedure(n.Ref.Address).Owner },
		func(p *catalog.Procedure) {
			installation := *p.Origin.Installation
			origin := *p.Origin
			origin.Installation = &installation
			p.Origin = &origin
		},
	} {
		proc := *n.Procedure
		mutate(&proc)
		bad := n
		bad.Procedure, bad.RoutineReference = &proc, &proc
		if err := stored.ValidateInstalledTablefuncCrosstab(bad, e.CatalogRead()); err == nil {
			t.Fatal("forged crosstab declaration accepted")
		}
	}
	for _, mutate := range []func(*plan.InstalledTablefuncCrosstabExpr){
		func(call *plan.InstalledTablefuncCrosstabExpr) { call.Ref.Epoch++ },
		func(call *plan.InstalledTablefuncCrosstabExpr) { call.RoutineReference = nil },
		func(call *plan.InstalledTablefuncCrosstabExpr) {
			info := expr.InfoOf(call.Args[0])
			typ := *info.Usage.Type
			info.Usage.Type = &typ
			arg := plan.LiteralExpr{Info: info}
			call.Args = []expr.Expr{arg}
		},
	} {
		bad := n
		mutate(&bad)
		if err := stored.ValidateInstalledTablefuncCrosstab(bad, e.CatalogRead()); err == nil {
			t.Fatal("forged crosstab carrier accepted")
		}
	}
}

func TestTablefuncCrosstabDeferredRecordContext(t *testing.T) {
	e := engine.New()
	crosstabUnitInstalled(t, e)
	call := crosstabUnitExpression(t, e, "crosstab", "SELECT 1,2,3")
	catalogRecord, _ := e.CatalogRead().LookupType(2249)
	if !call.Usage.Type.IsDeferredRecord() || call.Usage.Type == catalogRecord || call.Usage.Type.IsComposite() {
		t.Fatal("unresolved result must own a deferred record, not a catalog identity or tuple shape")
	}
	if !sqltype.Null(call.Usage.Type).ValidResult(call.Usage) {
		t.Fatal("unresolved result cannot initialize a typed SRF frame")
	}
	if err := stored.ValidateInstalledTablefuncCrosstab(call, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := call.Rebuild(call.Info, call.Children())
	if err != nil || rebuilt.ResultUsage() != call.Usage {
		t.Fatal("reconstruction lost deferred result identity", err)
	}
	// Exercise the actual source/frame seam separately from installed-routine
	// authority (covered by the public direct/prepared diagnostic scenario).
	// Source opening, not stage construction, must reject the missing shape.
	node := &plan.SetProjectNode{
		QuerySchema: plan.QuerySchema{{Usage: call.Usage}},
		Child:       &plan.SingletonNode{}, Calls: []expr.Expr{call.TablefuncCrosstab},
		Frame: 1, MarkerWidth: 1, CallSlots: []int{0},
		Steps: []plan.SetStep{{Kind: exec.QuerySetCallStep, Index: 0}},
	}
	state := &exec.State{Source: &exec.Source{}}
	stage, err := exec.OpenSetProjectNode(context.Background(), state, node)
	if err != nil {
		t.Fatal("record context failed before source opening", err)
	}
	defer stage.Close()
	_, present, err := stage.NextOccurrence(context.Background())
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "function returning record called in context that cannot accept type record"}
	var diagnostic *sqlstate.Error
	if present || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal("missing complete execution context diagnostic", present, err)
	}
	if !stage.Closed || stage.Source != nil || stage.Finish != nil || stage.Produce != nil {
		t.Fatal("source-opening failure retained the invocation or child stage")
	}
	if err := stage.Close(); err != nil {
		t.Fatal("repeated close after context error", err)
	}
}

func TestTablefuncCrosstabActivationCleanup(t *testing.T) {
	e := engine.New()
	crosstabUnitInstalled(t, e)
	enginetest.Run(t, e, "CREATE TABLE ct_activation(k text,category integer,v text)")
	enginetest.Run(t, e, "INSERT INTO ct_activation VALUES('g',0,'v')")
	n := crosstabUnitExpression(t, e, "crosstab2", "SELECT 'g'::text,0,'v'::text")
	reader, _ := blockingUnitOwner(t, e)
	scope := pgrowlocksUnitScope(t, e, reader)
	scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
	e.Mu.Lock()
	auth, err := core.ScopeWriteAuthorizationLocked(scope, e)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	scope.RoutineAuth = auth
	beforeView, beforeInput, beforeOutput := scope.View, scope.Input, scope.Output
	beforeAuthority, beforeExpected := core.ScopeAuthority(scope), scope.Expected
	beforeCatalog, beforePrincipal := scope.RoutineAuth.Catalog.State(), scope.RoutineAuth.State.CurrentUser
	beforeDependencies := append([]catalog.ExpressionDependency(nil), scope.RoutineDependencies...)
	beforeDiagnostic, beforeEnum := reflect.ValueOf(beforeInput.TypeDiagnosticName).Pointer(), reflect.ValueOf(beforeInput.EnumMemberSafe).Pointer()
	beforeInput.TypeDiagnosticName, beforeInput.EnumMemberSafe = nil, nil
	unchanged := func() bool {
		input := scope.Input
		diagnostic, enum := reflect.ValueOf(input.TypeDiagnosticName).Pointer(), reflect.ValueOf(input.EnumMemberSafe).Pointer()
		input.TypeDiagnosticName, input.EnumMemberSafe = nil, nil
		return scope.View == beforeView && core.ScopeAuthority(scope) == beforeAuthority && scope.Expected == beforeExpected &&
			scope.RoutineAuth.Catalog.State() == beforeCatalog && scope.RoutineAuth.State.CurrentUser == beforePrincipal &&
			reflect.DeepEqual(scope.RoutineDependencies, beforeDependencies) &&
			diagnostic == beforeDiagnostic && enum == beforeEnum && reflect.DeepEqual(input, beforeInput) && reflect.DeepEqual(scope.Output, beforeOutput)
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
	cursor, err := n.OpenSource(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged() {
		t.Fatal("nested activation leaked caller environment")
	}
	value, present, err := cursor.Next(context.Background())
	if err != nil || !present {
		t.Fatal("missing owned source", present, err)
	}
	cursor.Close()
	if cursor.Values != nil || cursor.State != nil {
		t.Fatal("closed source retained payload")
	}
	if _, err := value.Type().Output(context.Background(), value); err != nil {
		t.Fatal("close invalidated returned value", err)
	}
	// Each command must own fresh expected analysis and source admission. The
	// constant first SELECT has no relation capture to authorize the next one.
	for _, source := range []string{
		"SELECT 1; SELECT k,category,v FROM ct_activation",
		"SELECT k,category,v FROM ct_activation; SELECT 1; SELECT k,category,v FROM ct_activation",
	} {
		values, err := scope.WriteHost().TablefuncCrosstab(source, n.Usage.Type)
		if err != nil || len(values) != 1 {
			t.Fatal("multiple command source failed", source, len(values), err)
		}
		got, err := values[0].Type().Output(context.Background(), values[0])
		if err != nil || got != "(g,v,)" {
			t.Fatal("last command result", source, got, err)
		}
		if !unchanged() {
			t.Fatal("multiple command activation leaked environment", source)
		}
	}
	for _, source := range []string{"SELECT missing_ct", "SELECT )", "SHOW work_mem", "DELETE FROM missing_ct", "SELECT k,category,v FROM ct_activation; SHOW work_mem"} {
		if _, err := scope.WriteHost().TablefuncCrosstab(source, n.Usage.Type); err == nil {
			t.Fatal("invalid source succeeded", source)
		}
		if !unchanged() {
			t.Fatal("failed activation leaked environment", source)
		}
	}
	bad := n
	bad.Ref = engine.New().Catalog.Domain.Ref(n.Ref.Epoch, n.Ref.Address)
	if _, err := bad.OpenSource(&evaluation); err == nil {
		t.Fatal("foreign capture entered source runtime")
	}
}

func TestTablefuncCrosstabControlExpressionIdentity(t *testing.T) {
	e := engine.New()
	crosstabUnitInstalled(t, e)
	n := crosstabUnitExpression(t, e, "crosstab3", "SELECT 'g'::text,0,'v'::text")
	oldType, oldProcedure := n.Usage.Type, n.Procedure
	enginetest.Run(t, e, "ALTER TYPE tablefunc_crosstab_3 RENAME TO pivot_result")
	view := e.CatalogRead()
	nextType, ok := view.LookupType(oldType.ID())
	if !ok || nextType == oldType {
		t.Fatal("type rename did not create a new generation")
	}
	mapped, changed, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		if current, ok := view.LookupType(d.ID()); ok {
			return current
		}
		return d
	})
	if err != nil || !changed {
		t.Fatal("named result retag", changed, err)
	}
	next := mapped.(plan.InstalledTablefuncCrosstabExpr)
	if next.Usage.Type != nextType || next.Ref != n.Ref || next.Procedure != oldProcedure || next.RoutineReference != oldProcedure || next.Args[0] != n.Args[0] {
		t.Fatal("retag changed capture or unchanged child ownership")
	}
	if n.Usage.Type != oldType || n.Procedure != oldProcedure {
		t.Fatal("retag mutated original")
	}
	if err := stored.ValidateInstalledTablefuncCrosstab(next, view); err != nil {
		t.Fatal(err)
	}
	remapped, err := stored.RemapExpression(next, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
	if err != nil {
		t.Fatal(err)
	}
	retained := remapped.(plan.InstalledTablefuncCrosstabExpr)
	if retained.Ref != next.Ref || retained.Procedure != next.Procedure || retained.RoutineReference != next.RoutineReference || retained.Usage != next.Usage {
		t.Fatal("structural remap lost installed capture")
	}
}

func TestTablefuncCrosstabMutableMembershipIdentity(t *testing.T) {
	for _, tc := range []struct{ name, signature string }{
		{"crosstab", "text"}, {"crosstab2", "text"}, {"crosstab3", "text"}, {"crosstab4", "text"}, {"crosstab", "text,integer"},
	} {
		t.Run(tc.name+"("+tc.signature+")", func(t *testing.T) {
			name := tc.name
			var extra []parser.Expr
			if tc.signature != "text" {
				extra = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 3}}}
			}
			e := engine.New()
			crosstabUnitInstalled(t, e)
			n := crosstabUnitExpression(t, e, name, "SELECT 'g'::text,0,'v'::text", extra...)
			installation, ok := catalog.OriginInstallation(n.Procedure)
			if !ok {
				t.Fatal("missing genuine installation")
			}
			validate := func() {
				t.Helper()
				view := e.CatalogRead()
				if err := stored.ValidateInstalledTablefuncCrosstab(n, view); err != nil {
					t.Fatal("current membership invalidated retained capture", err)
				}
				current, ok := view.Procedure(n.Ref.Address)
				if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) || current.Origin.Installation != installation {
					t.Fatal("membership changed released declaration authority")
				}
				mapped, changed, err := stored.MapExpression(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, nil, nil, nil)
				if err != nil || changed || !reflect.DeepEqual(mapped, n) {
					t.Fatal("membership changed structural capture", changed, err)
				}
			}
			// An ordinary member changes the mutable inventory, not the recipe.
			enginetest.Run(t, e, "CREATE FUNCTION ct_member() RETURNS integer LANGUAGE SQL AS 'SELECT 1'")
			enginetest.Run(t, e, "ALTER EXTENSION tablefunc ADD FUNCTION ct_member()")
			validate()
			enginetest.Run(t, e, "ALTER EXTENSION tablefunc DROP FUNCTION "+name+"("+tc.signature+")")
			validate()
			enginetest.Run(t, e, "ALTER FUNCTION "+name+"("+tc.signature+") RENAME TO ct_survivor")
			validate()
			enginetest.Run(t, e, "CREATE EXTENSION \"uuid-ossp\"")
			enginetest.Run(t, e, "ALTER EXTENSION \"uuid-ossp\" ADD FUNCTION ct_survivor("+tc.signature+")")
			validate()
			if name != "crosstab" {
				// The named result remains a real normal dependency after its
				// extension membership (including the array) is detached.
				enginetest.Run(t, e, "ALTER EXTENSION tablefunc DROP TYPE tablefunc_crosstab_"+name[len(name)-1:])
			}
			enginetest.Run(t, e, "DROP EXTENSION tablefunc")
			validate()
			rebound := crosstabUnitExpression(t, e, "ct_survivor", "SELECT 'g'::text,0,'v'::text", extra...)
			if rebound.Ref.Address != n.Ref.Address || rebound.RoutineReference != rebound.Procedure || !exec.SameRoutineDeclaration(rebound.Procedure, n.Procedure) {
				t.Fatal("renamed survivor lost synchronized routine authority")
			}
			if n.Procedure.Result == 2249 {
				if !sqltype.SameRecordUsage(rebound.Usage, n.Usage) || !rebound.Usage.Type.IsDeferredRecord() || rebound.Usage.Type == n.Usage.Type {
					t.Fatal("fresh anonymous-record bind lost its occurrence-owned logical result")
				}
			} else if rebound.Usage != n.Usage {
				t.Fatal("renamed survivor replaced the genuine named result generation")
			}
			if err := stored.ValidateInstalledTablefuncCrosstab(rebound, e.CatalogRead()); err != nil {
				t.Fatal(err)
			}
			reader, _ := blockingUnitOwner(t, e)
			scope := pgrowlocksUnitScope(t, e, reader)
			if err := scope.WriteHost().ValidateTablefuncCrosstab(n); err != nil {
				t.Fatal("live host rejected genuine absent-origin capture", err)
			}
			bad := n
			bad.Ref.Epoch++
			if err := scope.WriteHost().ValidateTablefuncCrosstab(bad); err == nil {
				t.Fatal("live host accepted stale epoch")
			}
		})
	}
}

func TestTablefuncCrosstabPreparedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION tablefunc")
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("ct_definition", "SELECT * FROM crosstab3('SELECT missing_ct')", nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("ct_definition")
	if definition == nil || definition.Description.Folds == nil {
		t.Fatal("missing real prepared definition owner")
	}
	owner := definition.Description.Folds
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("ct_binding", "ct_definition", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	binding := registry.Binding("ct_binding")
	if binding == nil || binding.Entry.Description.Folds != owner || binding.Resource == nil || binding.Resource.Root == nil {
		t.Fatal("binding lost definition/private graph")
	}
	var captured *plan.InstalledTablefuncCrosstabExpr
	plan.VisitExecutableQuery(binding.Resource.Root, func(plan.Node) {}, func(node expr.Expr) {
		plan.WalkExpression(node, func(child expr.Expr) {
			if call, ok := child.(plan.InstalledTablefuncCrosstabExpr); ok {
				copy := call
				captured = &copy
			}
		})
	})
	if captured == nil {
		t.Fatal("accepted graph lost installed source")
	}
	if err := stored.ValidateInstalledTablefuncCrosstab(*captured, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	graph := binding.Resource.Root
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("ct_definition") })
	if owner.Live() || binding.Retired || binding.Resource.Root != graph {
		t.Fatal("definition retirement changed accepted graph or retained publication rights")
	}
	if captured.Usage.Type == nil || captured.RoutineReference != captured.Procedure {
		t.Fatal("accepted capture lost original result/provider")
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("ct_binding") })
	if !binding.Retired {
		t.Fatal("closing portal retained accepted resource")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
