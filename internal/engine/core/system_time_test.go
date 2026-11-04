package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

func TestSystemTimeLifecycle(t *testing.T) {
	t.Run("rewind_argument_ownership", timeRewindOwnership)
	t.Run("cleanup_failure", timeCleanupFailure)
	t.Run("stage_terminal_ownership", func(t *testing.T) {
		for _, canceled := range []bool{false, true} {
			e := engine.New()
			enginetest.Run(t, e, "CREATE EXTENSION tsm_system_time")
			query, input := enginetest.QueryFixture(t, e, "SELECT * FROM t TABLESAMPLE system_time('Infinity')")
			project := query.Root.(*plan.ProjectNode)
			leaf := project.Child.(*plan.SourceNode)
			acceptedSample, acceptedCount := leaf.Sample, leaf.Sample.Argument
			acceptedColumns := &leaf.QuerySchema[0]
			ctx, cancel := context.WithCancel(context.Background())
			failure, cleanup := errors.New("sampling child failure"), errors.New("sampling child cleanup")
			closes, evaluations := 0, 0
			inner := exec.Stage{Source: input, Columns: project.Schema(), Evaluation: exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, Finish: func() error { closes++; return cleanup }}
			inner.Expressions = append([]expr.Expr(nil), project.Expressions...)
			inner.Expressions[0] = enginetest.QueryProbe(project.Schema()[0].Usage, func(*exec.Context) (sqltype.Value, error) {
				evaluations++
				if canceled {
					cancel()
					return sqltype.Value{}, context.Canceled
				}
				return sqltype.Value{}, failure
			})
			bridge := &exec.Source{View: input.View, Ref: input.Ref, NestedRoot: project, NestedInputs: input, NestedStage: &inner, Materialized: true, FlatWidth: len(leaf.Schema())}
			rows, count, err := exec.Finalize(ctx, project, bridge, nil)
			cancel()
			want := failure
			if canceled {
				want = context.Canceled
			}
			if !errors.Is(err, want) || rows != nil || count != 0 || evaluations != 1 || closes != 1 {
				t.Fatal("primary error replaced or child not consumed/closed", rows, count, err, evaluations, closes)
			}
			if bridge.NestedStage != nil || input.View != nil || inner.Source != nil || inner.Selection != nil || inner.Expressions != nil || inner.Finish != nil || inner.Produce != nil || inner.Reset != nil {
				t.Fatal("failed sample retained graph, selection or pin")
			}
			if leaf.Sample != acceptedSample || !reflect.DeepEqual(leaf.Sample.Argument, acceptedCount) || &leaf.QuerySchema[0] != acceptedColumns {
				t.Fatal("terminal cleanup mutated accepted plan backing")
			}
		}
	})
	t.Run("prepared_graph_and_reset", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, sql := range []string{"CREATE EXTENSION tsm_system_time", "CREATE TABLE rows_owned(id int)", "INSERT INTO rows_owned VALUES(1),(2),(3)", "BEGIN"} {
			enginetest.SessionSQL(t, s, sql)
		}
		cycle := s.NewProtocolCycle()
		command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("rows_definition", "SELECT (point '0.10000000000000002,1')::text AS p,id FROM rows_owned TABLESAMPLE system_time('Infinity')", nil)
			return err
		})
		registry := core.PayloadOf(s).PreparedSQL
		definition := registry.Lookup("rows_definition")
		owner, analysis := definition.Description.Folds, definition.Description.Analysis
		bind := func(name string) *core.PreparedBinding {
			command(func(scope *core.StatementScope) error {
				_, err := scope.BindProtocolPortal(name, "rows_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
				return err
			})
			binding := registry.Binding(name)
			if binding == nil || binding.Resource == nil || binding.Resource.Root == nil || binding.Folds == nil {
				t.Fatal("binding lacks actual graph/group")
			}
			return binding
		}
		first, second := bind("rows_first"), bind("rows_second")
		firstResource, secondResource := first.Resource, second.Resource
		firstRoot, secondRoot := firstResource.Root, secondResource.Root
		firstFolds, secondFolds := *first.Folds, *second.Folds
		accepted := owner.Current()
		if firstRoot == secondRoot || firstResource.Closer == nil || secondResource.Closer == nil || firstResource.Closer == secondResource.Closer {
			t.Fatal("independent portals shared mutable producer")
		}
		var sample *plan.TableSample
		plan.VisitCTEQuery(firstRoot, func(node plan.Node) {
			if source, ok := node.(*plan.SourceNode); ok && source.Sample != nil {
				sample = source.Sample
			}
		}, func(expr.Expr) {}, true)
		if sample == nil {
			t.Fatal("prepared graph lost sampling")
		}
		count := sample.Argument
		captured := false
		for _, dep := range analysis.Dependencies {
			if dep.Ref == sample.Handler {
				captured = dep.Purpose == catalog.DefinitionDependency && dep.Revision == sample.Procedure.Revision()
			}
		}
		if !captured {
			t.Fatal("handler absent from executable dependency manifest")
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("rows_definition") })
		if owner.Live() || firstResource.Root != firstRoot || secondResource.Root != secondRoot {
			t.Fatal("definition retirement changed accepted portal graph")
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("rows_first") })
		if firstResource.Root != nil || first.Folds != nil || !first.Retired || secondResource.Root != secondRoot {
			t.Fatal("portal close leaked or detached sibling ownership")
		}
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("rows_second") })
		if secondResource.Root != nil || second.Folds != nil || !second.Retired {
			t.Fatal("second portal retained live graph/group")
		}
		if !reflect.DeepEqual(sample.Argument, count) || !reflect.DeepEqual(firstFolds.Entries, secondFolds.Entries) || !reflect.DeepEqual(firstFolds.Entries, accepted.Entries) {
			t.Fatal("close mutated immutable accepted backing")
		}
		enginetest.SessionSQL(t, s, "ROLLBACK")
		if err := engine.Reset(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(sample.Argument, count) || firstRoot.Schema()[0].Usage.Type.ID() != sqltype.Text {
			t.Fatal("Reset mutated retained accepted graph/schema")
		}
	})
}

func timeRewindOwnership(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION tsm_system_time")
	query, input := enginetest.QueryFixture(t, e, "SELECT * FROM t TABLESAMPLE system_time('Infinity')")
	project := query.Root.(*plan.ProjectNode)
	leaf := project.Child.(*plan.SourceNode)
	node, sample := *leaf, *leaf.Sample
	evaluations, closes := 0, 0
	initialArgument, err := leaf.Sample.Argument.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil {
		t.Fatal(err)
	}
	argument := initialArgument
	probe := enginetest.QueryProbe(leaf.Sample.Argument.ResultUsage(), func(*exec.Context) (sqltype.Value, error) {
		evaluations++
		return argument, nil
	}).(enginetest.QueryProbeExpression)
	sample.Argument = &probe
	node.Sample = &sample
	accepted := sample.Argument
	changed := false
	inner := exec.Stage{
		Source: input, Columns: project.Schema(), Expressions: project.Expressions,
		Evaluation: exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}},
		Finish:     func() error { closes++; return nil },
		Reset:      func(context.Context, *exec.Stage) (bool, error) { return changed, nil },
	}
	bridge := &exec.Source{View: input.View, Ref: input.Ref, NestedRoot: project, NestedInputs: input, NestedStage: &inner, Materialized: true, FlatWidth: len(leaf.Schema())}
	stage, err := exec.OpenNode(context.Background(), &exec.State{Source: bridge}, &node)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if stage.Count() != input.Count() || evaluations != 1 {
		t.Fatal("argument evaluated per candidate", stage.Count(), evaluations)
	}
	selection := append([]int(nil), stage.Selection...)
	if fresh, err := stage.Rewind(context.Background()); fresh || err != nil || evaluations != 1 || !reflect.DeepEqual(selection, stage.Selection) {
		t.Fatal("unchanged rewind resampled", fresh, err, evaluations)
	}
	changed = true
	argument, err = sqltype.Float(argument.Type(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if fresh, err := stage.Rewind(context.Background()); !fresh || err != nil || evaluations != 2 || stage.Count() != 0 {
		t.Fatal("changed invocation failed zero budget", fresh, err, evaluations, stage.Count())
	}
	argument = initialArgument
	if fresh, err := stage.Rewind(context.Background()); !fresh || err != nil || evaluations != 3 || stage.Count() != input.Count() {
		t.Fatal("changed invocation retained expired selection", fresh, err, evaluations, stage.Count())
	}
	if err := stage.Close(); err != nil || closes != 1 || stage.Source != nil || stage.Selection != nil || stage.Reset != nil || stage.Finish != nil || stage.Produce != nil || inner.Evaluation.Context != nil {
		t.Fatal("rewind invocation retained resources", err, closes)
	}
	if sample.Argument != accepted || node.Sample != &sample || leaf.Sample == &sample {
		t.Fatal("execution changed immutable argument/plan")
	}
}

func timeCleanupFailure(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION tsm_system_time")
	query, input := enginetest.QueryFixture(t, e, "SELECT * FROM t TABLESAMPLE system_time('Infinity')")
	project := query.Root.(*plan.ProjectNode)
	cleanup := errors.New("time child close failure")
	closes := 0
	inner := exec.Stage{
		Source: input, Columns: project.Schema(), Expressions: project.Expressions,
		Evaluation: exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}},
		Finish:     func() error { closes++; return cleanup },
	}
	bridge := &exec.Source{View: input.View, Ref: input.Ref, NestedRoot: project, NestedInputs: input, NestedStage: &inner, Materialized: true, FlatWidth: len(project.Schema())}
	rows, count, err := exec.Finalize(context.Background(), project, bridge, nil)
	if rows != nil || count != 0 || !errors.Is(err, cleanup) || closes != 1 || inner.Source != nil || bridge.NestedStage != nil {
		t.Fatal("unopposed cleanup failure lost or leaked child", rows, count, err, closes)
	}
}

func TestSepgsqlSimulationTimeFactory(t *testing.T) {
	t.Run("actual_factory_argument_errors_and_restore", func(t *testing.T) {
		e := sepgsqlSupportEngine(t)
		for _, query := range []string{
			"CREATE EXTENSION tsm_system_time",
			"CREATE TABLE sampled_time(v integer)",
			"INSERT INTO sampled_time VALUES(1),(2)",
			"SECURITY LABEL ON FUNCTION system_time(internal) IS 'S'",
			"CREATE FUNCTION sample_millis() RETURNS double precision LANGUAGE sql VOLATILE AS $$SELECT CASE WHEN sepgsql_getcon()='R' THEN 'Infinity'::float8 ELSE -1::float8 END$$",
		} {
			enginetest.Run(t, e, query)
		}
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		// S denies execute in this policy, but direct fmgr has no executor
		// execute hook. Its returned duration method must run after restoration.
		sepgsqlPhaseRows(t, e, op, "SELECT count(*) FROM sampled_time TABLESAMPLE system_time(sample_millis())", [][]driver.Value{{int64(2)}})
		sepgsqlPhaseRows(t, e, op, "SELECT count(*) FROM sampled_time TABLESAMPLE system_time(0)", [][]driver.Value{{int64(0)}})
		for _, invalid := range []struct{ argument, message string }{
			{"NULL", "TABLESAMPLE parameter cannot be null"},
			{"-1", "sample collection time must not be negative"},
			{"'NaN'::float8", "sample collection time must not be negative"},
		} {
			enginetest.RoleError(t, e, op, "SELECT count(*) FROM sampled_time TABLESAMPLE system_time("+invalid.argument+")", "2202H", invalid.message, "")
			sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_getcon()", [][]driver.Value{{"R"}})
		}
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon(NULL)")
		enginetest.RoleExecute(t, e, op, "ALTER FUNCTION system_time(internal) RENAME TO sampled_time_method")
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		sepgsqlPhaseRows(t, e, op, "SELECT count(*) FROM sampled_time TABLESAMPLE sampled_time_method(sample_millis())", [][]driver.Value{{int64(2)}})
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon(NULL)")
		enginetest.RoleExecute(t, e, op, "SECURITY LABEL ON FUNCTION sampled_time_method(internal) IS 'U'")
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		for _, argument := range []string{"NULL", "-1", "'Infinity'::float8"} {
			enginetest.RoleError(t, e, op, "SELECT count(*) FROM sampled_time TABLESAMPLE sampled_time_method("+argument+")", "42501", "SELinux: security policy violation", "")
			sepgsqlPhaseRows(t, e, op, "SELECT sepgsql_getcon()", [][]driver.Value{{"R"}})
		}
	})
	t.Run("genuine_source_receipt_not_ambient_or_other_owner", func(t *testing.T) {
		e := sepgsqlSupportEngine(t)
		for _, query := range []string{
			"CREATE EXTENSION tsm_system_time",
			"CREATE TABLE sampled_time(v integer)",
			"SECURITY LABEL ON FUNCTION system_time(internal) IS 'S'",
		} {
			enginetest.Run(t, e, query)
		}
		op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT v FROM sampled_time TABLESAMPLE system_time(0)")
		op.Session.Sepgsql.Set("R")
		source := project.Child.(*plan.SourceNode)
		site := exec.DirectRoutineInitializationSite{Routine: source.Sample.Procedure, Reference: source.Sample.Handler, Purpose: exec.DirectSample, Source: source.Ref, Occurrence: uint32(source.Occurrence)}
		if err := exec.BeginDirectRoutineInitialization(evaluation, site); err != nil {
			t.Fatal(err)
		}
		token, err := evaluation.EnterInitializedDirectRoutine(site)
		if err != nil || op.Session.Sepgsql.Current() != "T" {
			t.Fatal("actual initialized factory did not enter trusted subject", err, op.Session.Sepgsql.Current())
		}
		token.Leave()
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("factory token did not restore caller")
		}
		for _, field := range []string{"source", "occurrence", "purpose", "reference", "routine"} {
			wrong := site
			switch field {
			case "source":
				wrong.Source.Epoch++
			case "occurrence":
				wrong.Occurrence++
			case "purpose":
				wrong.Purpose = exec.DirectAccessMethod
			case "reference":
				wrong.Reference.Epoch++
			case "routine":
				copy := *wrong.Routine
				copy.ObjectRevision++
				wrong.Routine = &copy
			}
			if _, err := evaluation.EnterInitializedDirectRoutine(wrong); err == nil {
				t.Fatal("foreign factory selection adopted receipt", field)
			}
		}
		other, err := core.HostOf(scope).NewRoutineInitialization()
		if err != nil {
			t.Fatal(err)
		}
		for _, owner := range []exec.RoutineInitialization{{}, other} {
			copy := *evaluation
			copy.RoutineInitialization = owner
			if _, err := copy.EnterInitializedDirectRoutine(site); err == nil {
				t.Fatal("missing or sibling owner adopted factory receipt")
			}
		}
		copy := *evaluation
		copy.Catalog = catalog.Read{}
		if _, err := copy.EnterInitializedDirectRoutine(site); err == nil {
			t.Fatal("missing source catalog adopted live scope")
		}
		scope.Finish(nil)
		if _, err := evaluation.EnterInitializedDirectRoutine(site); err == nil {
			t.Fatal("finished scope retained callable factory owner")
		}
	})
}

func TestSepgsqlSimulationTimeFactoryRescan(t *testing.T) {
	e := sepgsqlSupportEngine(t)
	for _, query := range []string{
		"CREATE EXTENSION tsm_system_time",
		"CREATE TABLE sampled_time(v integer)",
		"INSERT INTO sampled_time VALUES(1),(2)",
		"SECURITY LABEL ON FUNCTION system_time(internal) IS 'S'",
	} {
		enginetest.Run(t, e, query)
	}
	op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT v FROM sampled_time TABLESAMPLE system_time(0)",
		"SET client_min_messages=log", "SET sepgsql.debug_audit=on", "SELECT sepgsql_setcon('R')")
	if err := view.ValidateQueryJoinExecution(evaluation, project, evaluation.Catalog, 0); err != nil {
		t.Fatal(err)
	}
	// Publish the genuine initializer and catalog on the captured source header
	// before either the child stage or the sampling bridge retains evaluators.
	source := sepgsqlInitializationSource(t, scope, evaluation, "sampled_time")
	if err := exec.SelectQuerySources(op.Context(), source); err != nil {
		t.Fatal(err)
	}
	exec.AdmitScan(project, source)
	leaf := project.Child.(*plan.SourceNode)
	plain := *leaf
	plain.Sample = nil
	state := &exec.State{Source: source, SourceMode: exec.SourceBorrowed, Retain: true, Input: scope.Input, Output: scope.Output}
	inner, err := exec.OpenNode(op.Context(), state, &plain)
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	changed := false
	inner.Reset = func(context.Context, *exec.Stage) (bool, error) { return changed, nil }
	bridge := &exec.Source{Ref: source.Ref, Authority: source.Authority, NestedRoot: project, NestedInputs: source, NestedStage: &inner, Materialized: true, FlatWidth: len(leaf.Schema())}
	defer bridge.Close()
	node, sample := *leaf, *leaf.Sample
	node.Sample = &sample
	argument, err := sqltype.Float(sample.Argument.ResultUsage().Type, math.Inf(1))
	if err != nil {
		t.Fatal(err)
	}
	evaluations := 0
	sample.Argument = enginetest.QueryProbe(sample.Argument.ResultUsage(), func(*exec.Context) (sqltype.Value, error) {
		evaluations++
		if op.Session.Sepgsql.Current() != "R" {
			t.Fatal("argument evaluation inherited factory trusted context")
		}
		return argument, nil
	})
	entries := func() int {
		n := 0
		for _, notice := range op.Notices {
			if strings.Contains(notice.Message, "tcontext=S tclass=db_procedure") && strings.Contains(notice.Message, "entrypoint") {
				n++
			}
		}
		return n
	}
	before := entries()
	factoryState := &exec.State{Source: bridge, Input: scope.Input, Output: scope.Output}
	stage, err := exec.OpenNode(op.Context(), factoryState, &node)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if stage.Count() != 2 || evaluations != 1 || entries() != before+1 || factoryState.RoutineInitialization != evaluation.RoutineInitialization {
		t.Fatal("real time factory did not produce duration table and rows", stage.Count(), evaluations, op.Notices)
	}
	initializations := scope.MarkSepgsqlObservations().Initializations
	if fresh, err := stage.Rewind(op.Context()); fresh || err != nil || stage.Count() != 2 || evaluations != 1 || entries() != before+1 {
		t.Fatal("unchanged scan repeated argument/factory", fresh, err, evaluations)
	}
	changed = true
	argument, err = sqltype.Float(argument.Type(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if fresh, err := stage.Rewind(op.Context()); !fresh || err != nil || stage.Count() != 0 || evaluations != 2 {
		t.Fatal("retained duration method failed zero rescan", fresh, err, stage.Count(), evaluations)
	}
	argument, err = sqltype.Float(argument.Type(), math.Inf(1))
	if err != nil {
		t.Fatal(err)
	}
	if fresh, err := stage.Rewind(op.Context()); !fresh || err != nil || stage.Count() != 2 || evaluations != 3 {
		t.Fatal("retained duration method failed full rescan", fresh, err, stage.Count(), evaluations)
	}
	if entries() != before+1 || scope.MarkSepgsqlObservations().Initializations != initializations ||
		factoryState.RoutineInitialization != evaluation.RoutineInitialization || source.Authority.RoutineInitialization != evaluation.RoutineInitialization ||
		op.Session.Sepgsql.Current() != "R" || node.Sample != &sample || leaf.Sample == &sample {
		t.Fatal("rescan repeated factory or mutated accepted source", op.Notices)
	}
	if err := stage.Close(); err != nil {
		t.Fatal("time rescan stage cleanup failed", err)
	}
	// OpenNode owns only the local stage. Unlike OpenQueryProjectStage, it
	// does not attach the capture owner's completion to that stage's Close.
	if err := factoryState.FinishSources(nil); err != nil {
		t.Fatal("time rescan source completion failed", err)
	}
	bridge.Close()
	if bridge.NestedStage != nil || bridge.NestedInputs != nil || bridge.NestedRoot != nil || bridge.Flat != nil || bridge.Authority != nil ||
		source.View != nil || source.Authority != nil || inner.Source != nil || inner.Reset != nil ||
		stage.Source != nil || stage.Reset != nil || stage.Finish != nil || stage.Selection != nil {
		t.Fatal("time rescan retained borrowed graph")
	}
}
