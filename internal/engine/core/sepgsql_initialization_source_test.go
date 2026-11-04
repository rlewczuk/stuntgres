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
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// This forwards to the real statement guard. It substitutes only an attempted
// body-source entry or invalidates the real scope, never grants permissions.
type sepgsqlAggregateBodyOwnerProbe struct {
	exec.Host
	validate func(exec.RoutineInitialization, catalog.Read) error
}

func (p sepgsqlAggregateBodyOwnerProbe) ValidateRoutineInitializationOwner(owner exec.RoutineInitialization, read catalog.Read) error {
	return p.validate(owner, read)
}

func TestSepgsqlSimulationInitializationSource(t *testing.T) {
	t.Run("inactive_aggregate_body_source_rejects_substitution", func(t *testing.T) {
		for _, name := range []string{
			"self_copy", "missing_read", "foreign_read", "copied_read_root", "missing_reference",
			"reference", "foreign_reference", "recipe", "occurrence", "source",
			"membership", "foreign_aggregate", "missing_manifest", "purpose", "unrelated_phase",
		} {
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				enginetest.Run(t, e, "CREATE TABLE init_owner_rows(v integer)")
				enginetest.Run(t, e, "CREATE VIEW init_owner_recipe AS SELECT abs(v) AS v FROM init_owner_rows")
				enginetest.Run(t, e, "CREATE AGGREGATE io_inactive_pad(integer) (SFUNC=pg_catalog.lpad,STYPE=text,INITCOND='x')")
				enginetest.Run(t, e, "CREATE AGGREGATE io_other_text(text) (SFUNC=pg_catalog.textcat,STYPE=text,INITCOND='x')")
				_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT io_inactive_pad(v) FROM init_owner_rows")
				if err := view.ValidateQueryJoinExecution(evaluation, project, evaluation.Catalog, 0); err != nil {
					t.Fatal(err)
				}
				original := project.Child.(*plan.AggregateNode).Calls[0].Provider.(*exec.TransitionAggregate)
				selected := *original
				if selected.Support.RoutineOID() != 879 || len(selected.Support.Body) != 1 || selected.Support.Body[0].Routine.RoutineOID() != 873 || evaluation.RoutineInitialization.Active() {
					t.Fatal("inactive fixture lost genuine body or acquired a MAC owner")
				}
				var foreign *exec.Context
				var foreignProvider *exec.TransitionAggregate
				if name == "foreign_read" || name == "foreign_reference" || name == "foreign_aggregate" {
					other := engine.New()
					enginetest.Run(t, other, "CREATE TABLE init_owner_rows(v integer)")
					enginetest.Run(t, other, "CREATE AGGREGATE io_inactive_pad(integer) (SFUNC=pg_catalog.lpad,STYPE=text,INITCOND='x')")
					_, _, foreignProject, otherEvaluation := sepgsqlInitializationBinding(t, other, "SELECT io_inactive_pad(v) FROM init_owner_rows")
					foreign = otherEvaluation
					foreignProvider = foreignProject.Child.(*plan.AggregateNode).Calls[0].Provider.(*exec.TransitionAggregate)
				}
				switch name {
				case "membership":
					_, _, otherProject, _ := sepgsqlInitializationBinding(t, e, "SELECT io_other_text(v::text) FROM init_owner_rows")
					selected.Aggregate = otherProject.Child.(*plan.AggregateNode).Calls[0].Procedure
				case "foreign_aggregate":
					selected.Aggregate = foreignProvider.Aggregate
				}
				call := original.Call
				info := expr.InfoOf(call)
				probeInfo := info
				if name == "missing_reference" {
					probeInfo.RoutineReference = nil
				}
				attempts := 0
				var retained *exec.Context
				selected.Call = sepgsqlAggregateCallProbe{Info: probeInfo, run: func(c *exec.Context) (sqltype.Value, error) {
					attempts++
					retained = c
					switch name {
					case "self_copy":
						copy := *c
						return call.Eval(&copy)
					case "missing_read":
						c.Catalog = catalog.Read{}
					case "foreign_read":
						c.Catalog = foreign.Catalog
					case "copied_read_root":
						root := *c.Catalog.State()
						c.Catalog = catalog.NewRead(c.Catalog.Owner(), &root)
					case "missing_manifest":
						c.RoutineRecipe, c.RoutineChecks = true, []catalog.ExpressionDependency{}
					case "purpose":
						c.RoutineRecipe = true
						c.RoutineChecks = append([]catalog.ExpressionDependency(nil), scope.RoutineDependencies...)
						for i := range c.RoutineChecks {
							if c.RoutineChecks[i].Ref.Address == selected.Support.Address() {
								c.RoutineChecks[i].Purpose = catalog.DefinitionDependency
							}
						}
					case "unrelated_phase":
						c.ClearNativeBodySource()
						c.InitializationPhase = exec.DomainCheckInitialization
					}
					switch name {
					case "reference", "foreign_reference", "recipe", "occurrence", "source":
						inputs := info.RoutineBodyInputs(c.Parameters)
						reference, recipe := info.RoutineReference, info.RoutineRecipeReference
						switch name {
						case "reference":
							reference = selected.Aggregate
						case "foreign_reference":
							reference = foreignProvider.Support
						case "recipe":
							recipe = evaluation.Catalog.MustRelation(enginetest.CatalogRef(t, e, evaluation.Catalog, "init_owner_recipe").Address)
						case "occurrence":
							inputs.Occurrence++
						case "source":
							inputs.Source.Frame++
						}
						token, err := c.FunctionEnterRoutine(reference, recipe, inputs)
						if err != nil {
							return sqltype.Value{}, err
						}
						token.Leave()
						return sqltype.String(selected.StateUsage.Type, "unexpected"), nil
					}
					return call.Eval(c)
				}}
				state, err := selected.InitWithContext(evaluation.Context, evaluation)
				if err != nil {
					t.Fatal(err)
				}
				three, err := sqltype.Integer(selected.Inputs[1].Type, 3)
				if err != nil {
					t.Fatal(err)
				}
				if err = selected.Advance(evaluation.Context, state, []sqltype.Value{three}, evaluation); err == nil {
					t.Fatal("inactive substituted source reached child output")
				}
				wantAttempts := 1
				if name == "missing_reference" {
					wantAttempts = 0
				}
				if attempts != wantAttempts {
					t.Fatal("inactive source rejection occurred at the wrong boundary", attempts, wantAttempts)
				}
				for _, runtime := range []*exec.Context{evaluation, retained} {
					if runtime != nil {
						fields := reflect.ValueOf(runtime).Elem()
						if !fields.FieldByName("nativeAggregate").IsZero() || fields.FieldByName("nativeAggregateEntered").Bool() {
							t.Fatal("inactive terminal source residue")
						}
					}
				}
				result, err := selected.Finalize(evaluation.Context, state)
				text, textErr := result.Text()
				if err != nil || textErr != nil || text != "x" {
					t.Fatal("inactive source failure committed transition state", text, err, textErr)
				}
				selected.Aggregate, selected.Call = original.Aggregate, original.Call
				if err = selected.Advance(evaluation.Context, state, []sqltype.Value{three}, evaluation); err != nil {
					t.Fatal("inactive source failure contaminated genuine caller", err)
				}
				result, err = selected.Finalize(evaluation.Context, state)
				text, textErr = result.Text()
				if err != nil || textErr != nil || text != "  x" || evaluation.RoutineInitialization.Active() || scope.MarkSepgsqlObservations().Initializations != 0 {
					t.Fatal("inactive genuine body retry or MAC ownership", text, err, textErr)
				}
				fields := reflect.ValueOf(scope).Elem()
				for _, field := range []string{"routineInitializationOwners", "routineInitializations", "nativeBodyInitializations"} {
					if !fields.FieldByName(field).IsNil() {
						t.Fatal("inactive body allocated MAC owners/receipts", field)
					}
				}
			})
		}
	})
	t.Run("aggregate_body_revalidates_original_source", func(t *testing.T) {
		for _, name := range []string{"zero_owner", "foreign_owner", "missing_read", "foreign_read", "cancel", "finished"} {
			t.Run(name, func(t *testing.T) {
				e := sepgsqlInitializationEngine(t)
				enginetest.Run(t, e, "CREATE AGGREGATE io_guard_pad(integer) (SFUNC=pg_catalog.lpad,STYPE=text,INITCOND='x')")
				op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT io_guard_pad(v) FROM init_owner_rows")
				if err := view.ValidateQueryJoinExecution(evaluation, project, evaluation.Catalog, 0); err != nil {
					t.Fatal(err)
				}
				selected := project.Child.(*plan.AggregateNode).Calls[0].Provider.(*exec.TransitionAggregate)
				if selected.Support.RoutineOID() != 879 || len(selected.Support.Body) != 1 || selected.Support.Body[0].Routine.RoutineOID() != 873 {
					t.Fatal("not a genuine selected native body")
				}
				host := core.HostOf(scope)
				var foreign *exec.Context
				if name == "foreign_owner" || name == "foreign_read" {
					foreignEngine := sepgsqlInitializationEngine(t)
					_, _, _, foreign = sepgsqlInitializationBinding(t, foreignEngine, "SELECT v FROM init_owner_rows")
				}
				calls := 0
				saved := op.Ctx
				defer func() { op.Ctx = saved }()
				evaluation.Host = sepgsqlAggregateBodyOwnerProbe{Host: host, validate: func(owner exec.RoutineInitialization, read catalog.Read) error {
					calls++
					if owner != evaluation.RoutineInitialization || read != evaluation.Catalog {
						t.Fatal("body adopted a replacement source instead of its exact parent")
					}
					if calls == 2 {
						switch name {
						case "zero_owner":
							owner = exec.RoutineInitialization{}
						case "foreign_owner":
							owner = foreign.RoutineInitialization
						case "missing_read":
							read = catalog.Read{}
						case "foreign_read":
							read = foreign.Catalog
						case "cancel":
							canceled, cancel := context.WithCancel(saved)
							cancel()
							op.Ctx = canceled
						}
					}
					return host.ValidateRoutineInitializationOwner(owner, read)
				}}
				state, err := selected.InitWithContext(evaluation.Context, evaluation)
				if err != nil {
					t.Fatal(err)
				}
				three, err := sqltype.Integer(selected.Inputs[1].Type, 3)
				if err != nil {
					t.Fatal(err)
				}
				mark := scope.MarkSepgsqlObservations()
				wantCalls := 2
				if name == "finished" {
					// Finish owns the terminal lifetime after all callbacks
					// unwind. Resume the retained frame only after that.
					if err = scope.Finish(nil); err != nil {
						t.Fatal(err)
					}
					wantCalls = 1
				}
				err = selected.Advance(evaluation.Context, state, []sqltype.Value{three}, evaluation)
				if err == nil || calls != wantCalls {
					t.Fatal("retired or substituted source reached child output", calls, wantCalls, err)
				}
				if name == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("body source cancellation changed identity", err)
				}
				op.Ctx = saved
				result, failure := selected.Finalize(evaluation.Context, state)
				text, textErr := result.Text()
				if failure != nil || textErr != nil || text != "x" || op.Session.Sepgsql.Current() != "A" {
					t.Fatal("failed body source committed transition state or subject", text, failure, textErr)
				}
				if name != "finished" {
					if scope.MarkSepgsqlObservations() != mark {
						t.Fatal("rejected body source minted a receipt")
					}
					evaluation.Host = host
					if err = selected.Advance(evaluation.Context, state, []sqltype.Value{three}, evaluation); err != nil {
						t.Fatal("failed body source contaminated original caller", err)
					}
					result, err = selected.Finalize(evaluation.Context, state)
					text, textErr = result.Text()
					if err != nil || textErr != nil || text != "  x" {
						t.Fatal("genuine body source retry", text, err, textErr)
					}
				}
			})
		}
	})
	e := sepgsqlInitializationEngine(t)
	_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT abs($1::integer)")
	node := project.Expressions[0]
	value, err := sqltype.Integer(node.ResultUsage().Type, -7)
	if err != nil {
		t.Fatal(err)
	}
	evaluation.Parameters = []sqltype.Value{value}
	if err := exec.AdmitExpression(evaluation, node, evaluation.Catalog); err != nil {
		t.Fatal(err)
	}
	source := &exec.Source{Singleton: true, Authority: &exec.Authority{Host: core.HostOf(scope), RoutineInitialization: evaluation.RoutineInitialization, RoutineCatalog: evaluation.Catalog}}
	mark := scope.MarkSepgsqlObservations()
	for range 3 {
		state := exec.State{Source: source, Parameters: []sqltype.Value{value}}
		frame := state.Evaluation(evaluation.Context)
		if frame.Catalog != evaluation.Catalog || frame.RoutineInitialization != evaluation.RoutineInitialization {
			t.Fatal("genuine no-View source lost explicit catalog/owner transfer")
		}
		result, err := node.Eval(&frame)
		if err != nil {
			t.Fatal("no-View actual initialized call", err)
		}
		got, err := result.Integer()
		if err != nil || got != 7 {
			t.Fatal(got, err)
		}
	}
	if scope.MarkSepgsqlObservations() != mark {
		t.Fatal("no-View pull minted another initializer")
	}
	missing := *source.Authority
	missing.RoutineCatalog = catalog.Read{}
	state := exec.State{Source: &exec.Source{Singleton: true, Authority: &missing}, Parameters: []sqltype.Value{value}}
	frame := state.Evaluation(evaluation.Context)
	if _, err = node.Eval(&frame); err == nil {
		t.Fatal("missing no-View catalog recovered ambient live Host authority")
	}
	foreignEngine := sepgsqlInitializationEngine(t)
	foreign := *source.Authority
	foreign.RoutineCatalog = foreignEngine.CatalogRead()
	state = exec.State{Source: &exec.Source{Singleton: true, Authority: &foreign}, Parameters: []sqltype.Value{value}}
	frame = state.Evaluation(evaluation.Context)
	if _, err = node.Eval(&frame); err == nil {
		t.Fatal("foreign no-View catalog adopted original source nonce")
	}
	state = exec.State{Source: source, Outer: &exec.Context{EvalStatement: exec.EvalStatement{Catalog: foreignEngine.CatalogRead()}}, Parameters: []sqltype.Value{value}}
	frame = state.Evaluation(evaluation.Context)
	if _, err = node.Eval(&frame); err == nil {
		t.Fatal("source header silently overrode incompatible lexical domain")
	}
	missing = *source.Authority
	missing.RoutineInitialization = exec.RoutineInitialization{}
	state = exec.State{Source: &exec.Source{Singleton: true, Authority: &missing}, Parameters: []sqltype.Value{value}}
	frame = state.Evaluation(evaluation.Context)
	if _, err = node.Eval(&frame); err == nil {
		t.Fatal("active no-View source minted missing owner on pull")
	}
}

func TestSepgsqlSimulationInitializationSourceAttachments(t *testing.T) {
	e := sepgsqlInitializationEngine(t)
	_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT abs($1::integer)")
	node := project.Expressions[0]
	value, err := sqltype.Integer(node.ResultUsage().Type, -7)
	if err != nil {
		t.Fatal(err)
	}
	evaluation.Parameters = []sqltype.Value{value}
	if err = exec.AdmitExpression(evaluation, node, evaluation.Catalog); err != nil {
		t.Fatal(err)
	}
	header := &exec.Authority{Host: core.HostOf(scope), RoutineInitialization: evaluation.RoutineInitialization, RoutineCatalog: evaluation.Catalog}
	source := &exec.Source{Singleton: true, Authority: header}
	state := exec.State{Source: source, Parameters: evaluation.Parameters}
	frame := state.Evaluation(evaluation.Context)
	stage := exec.Stage{Source: source, Columns: project.Schema(), Expressions: project.Expressions, Evaluation: frame, Retain: true}
	defer stage.Close()
	read := func() {
		t.Helper()
		row := make([]sqltype.Value, 1)
		present, failure := stage.Next(evaluation.Context, row)
		if failure != nil || !present {
			t.Fatal("genuine retained attachment", present, failure)
		}
		got, failure := row[0].Integer()
		if failure != nil || got != 7 || stage.Evaluation.RoutineInitialization != header.RoutineInitialization {
			t.Fatal("retained evaluator lost same source owner", got, failure)
		}
	}
	mark := scope.MarkSepgsqlObservations()
	read()
	if _, err = stage.Rewind(evaluation.Context); err != nil {
		t.Fatal(err)
	}
	read()
	if scope.MarkSepgsqlObservations() != mark {
		t.Fatal("repeated source row minted another initializer")
	}
	// A second genuine initializer has its own completed receipt. Updating the
	// explicit attachment header, not the retained evaluator, transports it.
	next := *evaluation
	next.RoutineInitialization, err = core.HostOf(scope).NewRoutineInitialization()
	if err != nil {
		t.Fatal(err)
	}
	if err = exec.AdmitExpression(&next, node, next.Catalog); err != nil {
		t.Fatal(err)
	}
	state.DetachRoutineInitialization()
	if err = state.AttachRoutineInitialization(next.RoutineInitialization, next.Catalog); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []exec.RoutineInitialization{{}, next.RoutineInitialization} {
		lost := stage
		lost.Evaluation.RoutineInitialization = owner
		lost.Position = 0
		if _, failure := lost.Next(evaluation.Context, make([]sqltype.Value, 1)); failure == nil {
			t.Fatal("retained source repaired a lost or replaced local owner")
		}
	}
	mark = scope.MarkSepgsqlObservations()
	if _, err = stage.Rewind(evaluation.Context); err != nil {
		t.Fatal(err)
	}
	read()
	if scope.MarkSepgsqlObservations() != mark {
		t.Fatal("attachment refresh reminted execute authorization")
	}
	foreignEngine := sepgsqlInitializationEngine(t)
	for _, read := range []catalog.Read{{}, foreignEngine.CatalogRead()} {
		lost := stage
		lost.Evaluation.Catalog = read
		lost.Position = 0
		if _, failure := lost.Next(evaluation.Context, make([]sqltype.Value, 1)); failure == nil {
			t.Fatal("retained source repaired a lost or foreign local catalog")
		}
	}
	uninitialized, err := core.HostOf(scope).NewRoutineInitialization()
	if err != nil {
		t.Fatal(err)
	}
	header.RoutineInitialization = uninitialized
	unadmitted := stage
	unadmitted.Position = 0
	if _, failure := unadmitted.Next(evaluation.Context, make([]sqltype.Value, 1)); failure == nil {
		t.Fatal("new attachment owner adopted another initializer's receipt")
	}
	header.RoutineInitialization = next.RoutineInitialization
	state.DetachRoutineInitialization()
	detached := stage
	detached.Position = 0
	if _, failure := detached.Next(evaluation.Context, make([]sqltype.Value, 1)); failure == nil {
		t.Fatal("detached retained source borrowed its incidental live host")
	}
	header.RoutineInitialization, header.RoutineCatalog = next.RoutineInitialization, next.Catalog
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	stage.Position = 0
	if _, failure := stage.Next(evaluation.Context, make([]sqltype.Value, 1)); failure == nil {
		t.Fatal("finished retained source reused its completed receipt")
	}
}

func TestSepgsqlSimulationInitializationNativeLineage(t *testing.T) {
	for _, active := range []bool{false, true} {
		name := "inactive"
		if active {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			var e *core.Engine
			if active {
				e = sepgsqlInitializationEngine(t)
			} else {
				e = engine.New()
				enginetest.Run(t, e, "CREATE TABLE init_owner_rows(v integer)")
			}
			enginetest.Run(t, e, "COMMENT ON COLUMN init_owner_rows.v IS 'ordinary comment'")
			enginetest.Run(t, e, "ALTER TABLE init_owner_rows RENAME COLUMN v TO current_name")
			_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT col_description('init_owner_rows'::regclass,1)")
			snapshot := scope.Inquiry.CommentCatalog
			if snapshot.State() != evaluation.Catalog.State() || snapshot.Owner() != evaluation.Catalog.Owner() || snapshot.Domain() != evaluation.Catalog.Domain() || snapshot.Epoch() != evaluation.Catalog.Epoch() {
				t.Fatal("ordinary comment lost its actual statement catalog snapshot")
			}
			node := project.Expressions[0]
			outer, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil || outer == nil {
				t.Fatal(err)
			}
			// REL_18_6 system_functions.sql uses oid equality (twice) and
			// int4 equality. The existing finite executable manifest represents
			// those helpers independently of its parsed display-part indices.
			if outer.Address().ObjectID != 1216 || outer.DeclarationBody == nil || len(outer.Body) != 2 || outer.Body[0].Routine.Address().ObjectID != 184 || outer.Body[1].Routine.Address().ObjectID != 65 {
				t.Fatal("missing genuine dual-representation col_description source")
			}
			if err = exec.AdmitExpression(evaluation, node, evaluation.Catalog); err != nil {
				t.Fatal(err)
			}
			info := expr.InfoOf(node)
			body, err := exec.BeginNativeBody(evaluation, outer, info)
			if err != nil {
				t.Fatal("admitted original body source", err)
			}
			auth, dependencies, err := exec.RoutineInvocationAuthority(&body)
			if err != nil {
				t.Fatal(err)
			}
			var consumed exec.RoutineProgram
			if err = consumed.Record(evaluation, dependencies, outer); err != nil {
				t.Fatal("outer caller source", err)
			}
			body.RoutineProgram = &consumed
			for ordinal, edge := range outer.Body {
				inner, err := exec.BodyRoutineDescriptor(&body, auth, dependencies, edge)
				if err != nil {
					t.Fatal(err)
				}
				if err = exec.AdmitInitializedBodyRoutine(&body, auth, dependencies, inner, uint32(ordinal+1)); err != nil {
					t.Fatal("genuine finite Body ordinal", ordinal, err)
				}
				childInfo := info
				childInfo.RoutineReference = inner
				childInfo.Occurrence = uint32(ordinal + 1)
				if err = exec.InitializeNativeBodyCall(&body, inner, childInfo); err != nil {
					t.Fatal("same retained child expression", ordinal, err)
				}
				wrongSource := childInfo
				wrongSource.Source.Frame++
				if err = exec.InitializeNativeBodyCall(&body, inner, wrongSource); err == nil {
					t.Fatal("equivalent child adopted a foreign source")
				}
				wrongOrdinal := childInfo
				wrongOrdinal.Occurrence = uint32(2 - ordinal)
				if err = exec.InitializeNativeBodyCall(&body, inner, wrongOrdinal); err == nil {
					t.Fatal("canonical member replaced the exact child ordinal")
				}
				if err = exec.AdmitInitializedBodyRoutine(&body, auth, dependencies, inner, uint32(2-ordinal)); err == nil {
					t.Fatal("canonical member replaced the explicit manifest ordinal")
				}
				foreign := body
				foreign.InitializationPhase = exec.DomainCheckInitialization
				if err = exec.InitializeNativeBodyCall(&foreign, inner, childInfo); err == nil {
					t.Fatal("unrelated phase adopted entered-body lineage")
				}
			}
			if len(consumed.Calls) != 3 || consumed.Calls[0].Routine != outer {
				t.Fatal("outer caller or immediate children lost", len(consumed.Calls))
			}
			if active && (!body.RoutineInitialization.Active() || body.RoutineInitialization == evaluation.RoutineInitialization) {
				t.Fatal("body did not retain its actual derived owner")
			}
			if !active && (evaluation.RoutineInitialization.Active() || body.RoutineInitialization.Active()) {
				t.Fatal("inactive ordinary ACL initialization minted a simulation owner")
			}
			mark := scope.MarkSepgsqlObservations()
			if err = consumed.Readmit(auth, exec.ConsumedGraphRevalidation); err != nil {
				t.Fatal("actual consumed outer/body lifetime", err)
			}
			if scope.MarkSepgsqlObservations() != mark {
				t.Fatal("final ordinary ACL readmission replayed initialization")
			}
			foreignAuth := auth
			foreignAuth.Catalog = engine.New().CatalogRead()
			if err = consumed.Readmit(foreignAuth, exec.ConsumedGraphRevalidation); err == nil {
				t.Fatal("foreign catalog adopted consumed native lineage")
			}
			value, err := node.Eval(evaluation)
			if err != nil {
				t.Fatal("real ordinary comment kernel", err)
			}
			text, err := value.Text()
			if err != nil || text != "ordinary comment" {
				t.Fatal("current-name comment metadata", text, err)
			}
			scope.Finish(nil)
			if _, err = exec.BeginNativeBody(evaluation, outer, info); err == nil {
				t.Fatal("finished source adopted stale native lineage")
			}
		})
	}
}
