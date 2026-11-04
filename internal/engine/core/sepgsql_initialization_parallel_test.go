package core_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
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

// Preserve the actual selected call metadata while changing only its attempted
// entry. This exercises invoke's private source, not a test permission provider.
type sepgsqlAggregateCallProbe struct {
	expr.Info
	run func(*exec.Context) (sqltype.Value, error)
}

func (p sepgsqlAggregateCallProbe) Eval(c expr.Context) (sqltype.Value, error) {
	return p.run(c.(*exec.Context))
}

func sepgsqlInitializationSource(t *testing.T, scope *core.StatementScope, evaluation *exec.Context, table string) *exec.Source {
	t.Helper()
	// Capture the actual admitted physical relation, not a SELECT result whose
	// empty projection may legitimately have no retained Source at all.
	e := scope.Engine()
	ref := enginetest.CatalogRef(t, e, evaluation.Catalog, table)
	if err := scope.AcquireRelation(enginetest.Statement(t, "SELECT * FROM "+table), true); err != nil {
		t.Fatal(err)
	}
	captured, err := scope.AcquireDataView(ref)
	if err != nil {
		t.Fatal(err)
	}
	source, err := exec.CaptureSource(evaluation.Context, captured, ref)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Close)
	source.Host = core.HostOf(scope)
	source.Authority = &exec.Authority{Host: core.HostOf(scope), RoutineInitialization: evaluation.RoutineInitialization, RoutineCatalog: evaluation.Catalog}
	source.PlannerCosts = plan.DefaultPlannerCosts()
	source.PlannerCosts.ParallelSetup, source.PlannerCosts.ParallelTuple, source.PlannerCosts.MinParallelTableBlocks = 0, 0, 0
	source.PlannerRows, source.PlannerColumns = 1000, len(evaluation.Catalog.MustRelation(ref.Address).Columns)
	return source
}

func TestSepgsqlSimulationInitializationParallel(t *testing.T) {
	t.Run("arithmetic_keeps_real_fanout", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		enginetest.Run(t, e, "INSERT INTO init_owner_rows VALUES(1),(2),(3),(4)")
		_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT v+1 FROM init_owner_rows")
		source := sepgsqlInitializationSource(t, scope, evaluation, "init_owner_rows")
		if _, _, ok := exec.ParallelQueryPlan(project, source); !ok {
			t.Fatal("ordinary arithmetic lost worker eligibility")
		}
		ctx, observation := exec.Observe(evaluation.Context, false)
		rows, count, err := exec.FinalizeSelect(ctx, project, source, nil, 2)
		if err != nil || count != 4 {
			t.Fatal("actual coordinator/worker execution", count, err)
		}
		got := map[int64]int{}
		for _, value := range parallelTestIDs(t, rows) {
			got[value]++
		}
		if !reflect.DeepEqual(got, map[int64]int{2: 1, 3: 1, 4: 1, 5: 1}) {
			t.Fatal("partitioned arithmetic bag", got)
		}
		if actual := observation.Actual(project); actual.Loops != 2 {
			t.Fatal("arithmetic silently selected serial execution", actual)
		}
		if scope.MarkSepgsqlObservations().Initializations == 0 {
			t.Fatal("coordinator failed to initialize selected arithmetic")
		}
	})
	t.Run("selected_ordering_support_is_sealed_before_workers", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		enginetest.Run(t, e, "CREATE TABLE init_sort_rows(h hstore)")
		enginetest.Run(t, e, "INSERT INTO init_sort_rows VALUES('d=>1'),('b=>1'),('c=>1'),('a=>1')")
		_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT h FROM init_sort_rows ORDER BY h")
		source := sepgsqlInitializationSource(t, scope, evaluation, "init_sort_rows")
		if _, _, ok := exec.ParallelQueryPlan(project, source); !ok {
			t.Fatal("selected physical ordering lost worker eligibility")
		}
		ctx, observation := exec.Observe(evaluation.Context, false)
		rows, count, err := exec.FinalizeSelect(ctx, project, source, nil, 2)
		if err != nil || count != 4 {
			t.Fatal("selected support coordinator proof", count, err)
		}
		defer rows.Close()
		values := make([]sqltype.Value, len(rows.Schema()))
		for _, want := range []string{`"a"=>"1"`, `"b"=>"1"`, `"c"=>"1"`, `"d"=>"1"`} {
			present, err := rows.Next(context.Background(), values)
			if err != nil || !present {
				t.Fatal("ordered worker result", present, err)
			}
			text, err := values[0].Type().OutputWithEnvironment(context.Background(), values[0], evaluation.FunctionOutput())
			if err != nil || text != want {
				t.Fatal("genuine hstore order", text, want, err)
			}
		}
		if actual := observation.Actual(project); actual.Loops != 2 {
			t.Fatal("selected support unexpectedly disabled fanout", actual)
		}
	})
	t.Run("conditional_null_body_stays_serial", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		enginetest.Run(t, e, "INSERT INTO init_owner_rows VALUES(NULL),(NULL),(NULL),(NULL)")
		enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.round(numeric,integer) IS 'D'")
		// There are no trusted transitions in this policy. Only the graph's genuine
		// entered-body initialization requirement may select the serial owner.
		op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT round(v::numeric) FROM init_owner_rows")
		if err := view.ValidateQueryJoinExecution(evaluation, project, evaluation.Catalog, 0); err != nil {
			t.Fatal(err)
		}
		source := sepgsqlInitializationSource(t, scope, evaluation, "init_owner_rows")
		ctx, observation := exec.Observe(evaluation.Context, false)
		rows, count, err := exec.FinalizeSelect(ctx, project, source, nil, 2)
		if err != nil || count != 4 {
			t.Fatal("strict NULL prematurely demanded entered inner routine", count, err)
		}
		defer rows.Close()
		values := make([]sqltype.Value, 1)
		for range 4 {
			present, err := rows.Next(ctx, values)
			if err != nil || !present || !values[0].IsNull() {
				t.Fatal("strict NULL body result", present, values, err)
			}
		}
		if actual := observation.Actual(project); actual.Loops != 1 {
			t.Fatal("conditional entered-body graph fanned out", actual)
		}
		if op.Session.Sepgsql.Current() != "A" {
			t.Fatal("suppressed body changed caller subject")
		}
	})
	t.Run("native_aggregate_keeps_sealed_fanout", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		enginetest.Run(t, e, "INSERT INTO init_owner_rows VALUES(1),(2),(3),(4)")
		enginetest.Run(t, e, "CREATE AGGREGATE io_native_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,COMBINEFUNC=pg_catalog.int4pl,INITCOND='0',PARALLEL=SAFE)")
		_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT io_native_sum(v) FROM init_owner_rows")
		if err := view.ValidateQueryJoinExecution(evaluation, project, evaluation.Catalog, 0); err != nil {
			t.Fatal(err)
		}
		source := sepgsqlInitializationSource(t, scope, evaluation, "init_owner_rows")
		aggregate, ok := exec.ParallelAggregatePlan(project, source)
		if !ok {
			t.Fatal("native aggregate lost actual partial eligibility")
		}
		worker, err := exec.ParallelQuerySource(source)
		if err != nil {
			t.Fatal(err)
		}
		if worker.Host != nil || worker.LiveHost() != nil || worker.Authority == nil || worker.Authority.Host != nil || worker.Authority.RoutineInitialization != evaluation.RoutineInitialization {
			worker.Close()
			t.Fatal("active worker retained a live host or lost explicit inert source")
		}
		worker.Close()
		var entries [2]atomic.Int64
		selected := aggregate.Calls[0].Provider.(*exec.TransitionAggregate)
		for i, support := range []*exec.TransitionAggregate{selected, selected.CombinePlan} {
			call := support.Call
			support.Call = sepgsqlAggregateCallProbe{Info: expr.InfoOf(call), run: func(c *exec.Context) (sqltype.Value, error) {
				if c.ClosedEvaluation == nil || c.LiveHost() != nil || c.RoutineInitialization != evaluation.RoutineInitialization {
					return sqltype.Value{}, errors.New("native aggregate did not use its genuine closed source")
				}
				entries[i].Add(1)
				return call.Eval(c)
			}}
		}
		mark := scope.MarkSepgsqlObservations()
		ctx, observation := exec.Observe(evaluation.Context, false)
		rows, count, err := exec.FinalizeSelect(ctx, project, source, nil, 2)
		if err != nil || count != 1 {
			t.Fatal("native aggregate sealed coordinator", count, err)
		}
		if len(rows.Schema()) != 1 || rows.Schema()[0].Usage.Type.ID() != sqltype.Int4 {
			t.Fatal("native aggregate result lost INT4 identity", rows.Schema())
		}
		if got := parallelTestIDs(t, rows); !reflect.DeepEqual(got, []int64{10}) {
			t.Fatal("native aggregate result", got)
		}
		if actual := observation.Actual(aggregate.Child); actual.Loops != 2 {
			t.Fatal("native aggregate silently disabled worker fanout", actual)
		}
		if entries[0].Load() != 4 || entries[1].Load() != 2 {
			t.Fatal("native selected transition/combine demand", entries[0].Load(), entries[1].Load())
		}
		if scope.MarkSepgsqlObservations().Initializations != mark.Initializations {
			t.Fatal("native aggregate minted ordinary per-call receipts")
		}
	})
	t.Run("runtime_sql_support_selects_only_its_serial_owner", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		enginetest.Run(t, e, "INSERT INTO init_owner_rows VALUES(1),(2),(3),(4)")
		enginetest.Run(t, e, "CREATE FUNCTION io_parallel_step(s bigint,v integer) RETURNS bigint LANGUAGE sql IMMUTABLE AS $$SELECT s+v$$")
		enginetest.Run(t, e, "CREATE AGGREGATE io_parallel_sum(integer) (SFUNC=io_parallel_step,STYPE=bigint,COMBINEFUNC=pg_catalog.int8pl,INITCOND='0',PARALLEL=SAFE)")
		_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT io_parallel_sum(v) FROM init_owner_rows")
		source := sepgsqlInitializationSource(t, scope, evaluation, "init_owner_rows")
		aggregate, ok := exec.ParallelAggregatePlan(project, source)
		if !ok {
			t.Fatal("fixture no longer reaches the existing SQL-support worker alternative")
		}
		if err := view.ValidateQueryJoinExecution(evaluation, project, evaluation.Catalog, 0); err != nil {
			t.Fatal(err)
		}
		ctx, observation := exec.Observe(evaluation.Context, false)
		rows, count, err := exec.FinalizeSelect(ctx, project, source, nil, 2)
		if err != nil || count != 1 {
			t.Fatal("selected SQL support lost its real runtime child owner", count, err)
		}
		if got := parallelTestIDs(t, rows); !reflect.DeepEqual(got, []int64{10}) {
			t.Fatal("selected SQL support result", got)
		}
		if actual := observation.Actual(aggregate.Child); actual.Loops != 1 {
			t.Fatal("unsealable runtime SQL child initialized in closed workers", actual)
		}
	})
	t.Run("native_aggregate_body_continues_same_source_owner", func(t *testing.T) {
		for _, active := range []bool{true, false} {
			mode := "active"
			if !active {
				mode = "inactive"
			}
			t.Run(mode, func(t *testing.T) {
				for _, name := range []string{"round", "pad", "round_empty", "pad_empty", "pad_null"} {
					t.Run(name, func(t *testing.T) {
						var e *core.Engine
						if active {
							e = sepgsqlInitializationEngine(t)
						} else {
							e = engine.New()
						}
						enginetest.Run(t, e, "CREATE TABLE io_body_rows(n numeric,v integer)")
						roundDefinition := "SFUNC=pg_catalog.numeric_add,STYPE=numeric,INITCOND='0',FINALFUNC=pg_catalog.round"
						padDefinition := "SFUNC=pg_catalog.lpad,STYPE=text,INITCOND='x'"
						if active {
							roundDefinition += ",COMBINEFUNC=pg_catalog.numeric_add,PARALLEL=SAFE"
							padDefinition += ",COMBINEFUNC=pg_catalog.textcat,PARALLEL=SAFE"
						}
						enginetest.Run(t, e, "CREATE AGGREGATE io_body_round(numeric) ("+roundDefinition+")")
						enginetest.Run(t, e, "CREATE AGGREGATE io_body_pad(integer) ("+padDefinition+")")
						query, want, helperOID, innerOID, typ := "SELECT io_body_round(n) FROM io_body_rows", "4", uint32(1708), uint32(1707), sqltype.Numeric
						switch name {
						case "round_empty":
							want = "0"
						case "pad", "pad_empty", "pad_null":
							query, want, helperOID, innerOID, typ = "SELECT io_body_pad(v) FROM io_body_rows", "  x", 879, 873, sqltype.Text
							if name != "pad" {
								want = "x"
							}
						}
						if name == "round" || name == "pad" {
							enginetest.Run(t, e, "INSERT INTO io_body_rows VALUES(1.2,3),(2.3,3)")
						} else if name == "pad_null" {
							enginetest.Run(t, e, "INSERT INTO io_body_rows VALUES(NULL,NULL)")
						}
						_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, query)
						node := project.Child.(*plan.AggregateNode)
						selected := node.Calls[0].Provider.(*exec.TransitionAggregate)
						helper, call := selected.Final, selected.FinalCall
						if helper == nil {
							helper, call = selected.Support, selected.Call
						}
						if helper.RoutineOID() != helperOID || helper.RoutineDefinition() != nil || len(helper.Body) != 1 || helper.Body[0].Routine.RoutineOID() != innerOID || helper.DeclarationBody == nil {
							t.Fatal("not the genuine supported native wrapper and finite body", helper)
						}
						if err := view.ValidateQueryJoinExecution(evaluation, project, evaluation.Catalog, 0); err != nil {
							t.Fatal(err)
						}
						source := sepgsqlInitializationSource(t, scope, evaluation, "io_body_rows")
						if (name == "round_empty" || name == "pad_empty") && source.Count() != 0 {
							t.Fatal("empty-group fixture captured a nonempty physical source", source.Count())
						}
						if active {
							if _, ok := exec.ParallelAggregatePlan(project, source); !ok {
								t.Fatal("body fixture failed to reach the existing partial alternative")
							}
						} else if selected.CombinePlan != nil || evaluation.RoutineInitialization.Active() {
							t.Fatal("inactive public fixture acquired combine or MAC owner authority")
						}
						ctx, observation := exec.Observe(evaluation.Context, false)
						rows, count, err := exec.FinalizeSelect(ctx, project, source, nil, 2)
						if err != nil || count != 1 {
							t.Fatal("native wrapper aggregate source continuation", count, err)
						}
						defer rows.Close()
						values := make([]sqltype.Value, 1)
						present, err := rows.Next(ctx, values)
						if err != nil || !present || values[0].Type().ID() != typ {
							t.Fatal("native wrapper aggregate typed result", present, values, err)
						}
						got, err := values[0].Type().OutputWithEnvironment(ctx, values[0], evaluation.FunctionOutput())
						if err != nil || got != want {
							t.Fatal("native wrapper aggregate value", got, want, err)
						}
						if actual := observation.Actual(node.Child); actual.Loops != 1 {
							t.Fatal("dynamic body incorrectly escaped its serial owner", actual)
						}
						if active {
							if _, err = core.HostOf(scope).ValidateRoutineInitialization(evaluation.RoutineInitialization, sepgsqlInitializationSite(t, evaluation, call)); err == nil {
								t.Fatal("native aggregate body fabricated an ordinary parent receipt")
							}
						}
						if active && name != "pad_empty" && name != "pad_null" {
							info := expr.InfoOf(call)
							child := helper.Body[0].Routine
							site := exec.RoutineInitializationSite{
								Reference: evaluation.Catalog.Domain().Ref(evaluation.Catalog.Epoch(), child.Address()),
								Routine:   child, Body: exec.RoutineBodyContext{Outer: helper, Statement: true},
								Purpose: catalog.DefinitionDependency, Phase: exec.NativeBodyInitialization,
								Occurrence: info.Occurrence, Source: info.Source, ManifestOrdinal: 1,
							}
							if _, err = core.HostOf(scope).ValidateRoutineInitialization(evaluation.RoutineInitialization, site); err != nil {
								t.Fatal("genuine child did not retain the SAME aggregate source owner", err)
							}
							wrong := site
							wrong.ManifestOrdinal++
							if _, err = core.HostOf(scope).ValidateRoutineInitialization(evaluation.RoutineInitialization, wrong); err == nil {
								t.Fatal("child receipt authorized an unrelated body ordinal")
							}
						}
						if !active {
							if evaluation.RoutineInitialization.Active() || scope.MarkSepgsqlObservations().Initializations != 0 {
								t.Fatal("inactive aggregate native body minted MAC initialization")
							}
							fields := reflect.ValueOf(scope).Elem()
							for _, field := range []string{"routineInitializationOwners", "routineInitializations", "nativeBodyInitializations"} {
								if !fields.FieldByName(field).IsNil() {
									t.Fatal("inactive aggregate native body allocated MAC owner/receipt maps", field)
								}
							}
						}
					})
				}
			})
		}
	})
	t.Run("native_aggregate_immediate_source_rejects_substitution", func(t *testing.T) {
		for _, name := range []string{
			"self_copy", "zero_owner", "other_owner", "foreign_owner", "lost_catalog", "foreign_catalog",
			"copied_catalog_root", "wrong_catalog_epoch", "missing_aggregate",
			"reference", "recipe", "occurrence", "source", "conflicting_aggregate", "purpose",
			"membership", "foreign_aggregate", "foreign_helper", "foreign_helper_entry", "missing_closed_proof", "foreign_closed_proof",
			"canceled", "finished", "discarded_source", "skipped_entry", "nested_entry", "sibling_entry", "kernel_error", "panic",
		} {
			t.Run(name, func(t *testing.T) {
				e := sepgsqlInitializationEngine(t)
				enginetest.Run(t, e, "CREATE AGGREGATE io_source_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')")
				enginetest.Run(t, e, "CREATE AGGREGATE io_other_sum(integer) (SFUNC=pg_catalog.int4mi,STYPE=integer,INITCOND='0')")
				op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT io_source_sum(v) FROM init_owner_rows")
				if err := view.ValidateQueryJoinExecution(evaluation, project, evaluation.Catalog, 0); err != nil {
					t.Fatal(err)
				}
				node := project.Child.(*plan.AggregateNode)
				original := node.Calls[0].Provider.(*exec.TransitionAggregate)
				selected := *original
				call := selected.Call
				host := core.HostOf(scope)
				other, err := host.NewRoutineInitialization()
				if err != nil {
					t.Fatal(err)
				}
				foreignProof, err := host.SealRoutineInitialization(other, evaluation.Catalog)
				if err != nil {
					t.Fatal(err)
				}
				var foreign *exec.Context
				var foreignProvider *exec.TransitionAggregate
				if name == "foreign_owner" || name == "foreign_catalog" || name == "foreign_aggregate" || name == "foreign_helper" || name == "foreign_helper_entry" {
					foreignEngine := sepgsqlInitializationEngine(t)
					enginetest.Run(t, foreignEngine, "CREATE AGGREGATE io_source_sum(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0')")
					_, _, foreignProject, otherEvaluation := sepgsqlInitializationBinding(t, foreignEngine, "SELECT io_source_sum(v) FROM init_owner_rows")
					foreign = otherEvaluation
					foreignProvider = foreignProject.Child.(*plan.AggregateNode).Calls[0].Provider.(*exec.TransitionAggregate)
				}
				switch name {
				case "missing_aggregate":
					selected.Aggregate = nil
				case "membership":
					_, _, otherProject, _ := sepgsqlInitializationBinding(t, e, "SELECT io_other_sum(v) FROM init_owner_rows")
					selected.Aggregate = otherProject.Child.(*plan.AggregateNode).Calls[0].Procedure
				case "foreign_aggregate":
					selected.Aggregate = foreignProvider.Aggregate
				case "foreign_helper":
					selected.Support, call = foreignProvider.Support, foreignProvider.Call
				}
				info := expr.InfoOf(call)
				sentinel := errors.New("selected native aggregate kernel failure")
				var retained *exec.Context
				attempts := 0
				selected.Call = sepgsqlAggregateCallProbe{Info: info, run: func(c *exec.Context) (sqltype.Value, error) {
					retained = c
					attempts++
					switch name {
					case "self_copy":
						copy := *c
						return call.Eval(&copy)
					case "zero_owner":
						c.RoutineInitialization = exec.RoutineInitialization{}
					case "other_owner":
						c.RoutineInitialization = other
					case "foreign_owner":
						c.RoutineInitialization = foreign.RoutineInitialization
					case "lost_catalog":
						c.Catalog = catalog.Read{}
					case "copied_catalog_root":
						root := *c.Catalog.State()
						c.Catalog = catalog.NewRead(c.Catalog.Owner(), &root)
					case "wrong_catalog_epoch":
						c.Catalog = catalog.NewRead(c.Catalog.Owner(), catalog.NewState(c.Catalog.Epoch()+1))
					case "foreign_catalog":
						c.Catalog = foreign.Catalog
					case "purpose":
						c.RoutineRecipe = true
						c.RoutineChecks = append([]catalog.ExpressionDependency(nil), scope.RoutineDependencies...)
						for i := range c.RoutineChecks {
							if c.RoutineChecks[i].Ref.Address == selected.Support.Address() {
								c.RoutineChecks[i].Purpose = catalog.DefinitionDependency
							}
						}
					case "missing_closed_proof":
						c.ClosedEvaluation = &exec.EvaluationCapture{}
					case "foreign_closed_proof":
						c.ClosedEvaluation = &exec.EvaluationCapture{Routines: foreignProof}
					case "canceled":
						saved := op.Ctx
						canceled, cancel := context.WithCancel(saved)
						cancel()
						op.Ctx = canceled
						defer func() { op.Ctx = saved }()
					case "discarded_source":
						c.ClearNativeBodySource()
						return sqltype.Integer(info.Usage.Type, 99)
					case "skipped_entry":
						return sqltype.Integer(info.Usage.Type, 99)
					case "kernel_error":
						return sqltype.Value{}, sentinel
					case "panic":
						panic(sentinel)
					}
					switch name {
					case "reference", "foreign_helper_entry", "recipe", "occurrence", "source", "conflicting_aggregate", "nested_entry", "sibling_entry":
						inputs := info.RoutineBodyInputs(c.Parameters)
						reference, recipe := info.RoutineReference, info.RoutineRecipeReference
						switch name {
						case "reference":
							reference = selected.Aggregate
						case "foreign_helper_entry":
							reference = foreignProvider.Support
						case "recipe":
							recipe = evaluation.Catalog.MustRelation(enginetest.CatalogRef(t, e, evaluation.Catalog, "init_owner_recipe").Address)
						case "occurrence":
							inputs.Occurrence++
						case "source":
							inputs.Source.Frame++
						case "conflicting_aggregate":
							inputs.AggregateOwner = selected.Support
						}
						token, err := c.FunctionEnterRoutine(reference, recipe, inputs)
						if err != nil {
							return sqltype.Value{}, err
						}
						token.Leave()
						if name == "nested_entry" {
							return call.Eval(c)
						}
						if name == "sibling_entry" {
							copy := *c
							return call.Eval(&copy)
						}
						return sqltype.Integer(info.Usage.Type, 99)
					}
					return call.Eval(c)
				}}
				state, err := selected.InitWithContext(evaluation.Context, evaluation)
				if err != nil {
					t.Fatal(err)
				}
				value, err := sqltype.Integer(selected.Inputs[1].Type, 4)
				if err != nil {
					t.Fatal(err)
				}
				mark := scope.MarkSepgsqlObservations()
				if name == "finished" {
					// Resume only after the owner has retired; never finish
					// a statement from inside its in-progress body callback.
					if err = scope.Finish(nil); err != nil {
						t.Fatal(err)
					}
				}
				var panicked any
				func() {
					defer func() { panicked = recover() }()
					err = selected.Advance(evaluation.Context, state, []sqltype.Value{value}, evaluation)
				}()
				if name == "panic" {
					if panicked != sentinel {
						t.Fatal("native panic identity changed", panicked)
					}
				} else if panicked != nil || err == nil {
					t.Fatal("substituted source reached native output", panicked, err)
				}
				if name == "kernel_error" && !errors.Is(err, sentinel) {
					t.Fatal("missing-consumption diagnostic masked actual kernel error", err)
				}
				if name == "canceled" && !errors.Is(err, context.Canceled) {
					t.Fatal("native owner guard lost cancellation", err)
				}
				runtimes := []*exec.Context{evaluation}
				if name == "foreign_helper" {
					// Selected-call validation rejects this foreign
					// declaration before native evaluation or source entry.
					if attempts != 0 || retained != nil {
						t.Fatal("foreign declaration reached native evaluation", attempts)
					}
				} else {
					if attempts != 1 || retained == nil {
						t.Fatal("negative did not reach the actual selected native call", attempts)
					}
					runtimes = append(runtimes, retained)
				}
				for _, runtime := range runtimes {
					fields := reflect.ValueOf(runtime).Elem()
					if !fields.FieldByName("nativeAggregate").IsZero() || fields.FieldByName("nativeAggregateEntered").Bool() {
						t.Fatal("terminal native aggregate source residue")
					}
				}
				result, failure := selected.Finalize(evaluation.Context, state)
				got, integerErr := result.Integer()
				if failure != nil || integerErr != nil || got != 0 {
					t.Fatal("failed source committed partial transition state", got, failure, integerErr)
				}
				initial := result
				if name != "finished" && scope.MarkSepgsqlObservations() != mark {
					t.Fatal("native source failure minted an ordinary call receipt")
				}
				if name != "finished" {
					selected.Aggregate, selected.Support, selected.Call = original.Aggregate, original.Support, original.Call
					if err := selected.Advance(evaluation.Context, state, []sqltype.Value{value}, evaluation); err != nil {
						t.Fatal("rejected source poisoned original runtime retry", err)
					}
					result, err = selected.Finalize(evaluation.Context, state)
					got, integerErr = result.Integer()
					if err != nil || integerErr != nil || got != 4 {
						t.Fatal("genuine retry result", got, err, integerErr)
					}
					sibling := *evaluation
					sibling.Parameters = []sqltype.Value{initial, value}
					if _, err = original.Call.Eval(&sibling); err == nil {
						t.Fatal("aggregate retry granted an unrelated ordinary sibling call")
					}
				}
			})
		}
	})
}
