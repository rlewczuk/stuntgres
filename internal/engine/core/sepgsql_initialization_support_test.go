package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSepgsqlSimulationInitializationSupport(t *testing.T) {
	t.Run("array_selected_entry_keeps_ordinary_and_closed_owners", func(t *testing.T) {
		e := sepgsqlInitializationEngine(t)
		_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT array_append($1::integer[],$2::integer),cardinality($1::integer[])")
		arrayType := project.Expressions[0].ResultUsage().Type
		array, err := arrayType.InputWithEnvironment(evaluation.Context, "{1}", -1, evaluation.FunctionInput())
		if err != nil {
			t.Fatal(err)
		}
		evaluation.Parameters = []sqltype.Value{array, sqltype.Null(arrayType.Element())}
		for _, node := range project.Expressions {
			if err = exec.AdmitExpression(evaluation, node, evaluation.Catalog); err != nil {
				t.Fatal(err)
			}
		}
		proof, err := core.HostOf(scope).SealRoutineInitialization(evaluation.RoutineInitialization, evaluation.Catalog)
		if err != nil {
			t.Fatal(err)
		}
		closed := *evaluation
		closed.Host = nil
		closed.ClosedEvaluation = &exec.EvaluationCapture{Routines: proof}
		mark := scope.MarkSepgsqlObservations()
		for _, runtime := range []*exec.Context{evaluation, &closed} {
			result, err := project.Expressions[0].Eval(runtime)
			if err != nil || result.Type() != arrayType || result.ArrayLen() != 2 || !result.ArrayElement(1).IsNull() {
				t.Fatal("genuine nonstrict array entry lost NULL element", result, err)
			}
			first, err := result.ArrayElement(0).Integer()
			if err != nil || first != 1 {
				t.Fatal("array entry changed its original element", first, err)
			}
			result, err = project.Expressions[1].Eval(runtime)
			if err != nil {
				t.Fatal("genuine array inquiry entry", err)
			}
			count, err := result.Integer()
			if err != nil || count != 1 {
				t.Fatal("array inquiry mutated the borrowed original array", count, err)
			}
			for _, node := range project.Expressions {
				lost := *runtime
				lost.RoutineInitialization = exec.RoutineInitialization{}
				if _, err = node.Eval(&lost); err == nil {
					t.Fatal("selected array kernel executed without its actual owner")
				}
				lost = *runtime
				lost.Catalog = catalog.Read{}
				if _, err = node.Eval(&lost); err == nil {
					t.Fatal("selected array kernel adopted ambient catalog authority")
				}
			}
		}
		if scope.MarkSepgsqlObservations() != mark {
			t.Fatal("original fmgr array entry fabricated a receipt or FHET observation")
		}
	})
	t.Run("native_strict_null_and_nonstrict_demand", func(t *testing.T) {
		for _, name := range []string{"strict_seed", "strict_unseeded", "strict_final_null", "nonstrict_null"} {
			t.Run(name, func(t *testing.T) {
				e := sepgsqlInitializationEngine(t)
				definition := "SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='0'"
				switch name {
				case "strict_unseeded", "strict_final_null":
					definition = "SFUNC=pg_catalog.int4pl,STYPE=integer,FINALFUNC=pg_catalog.abs"
				case "nonstrict_null":
					definition = "SFUNC=pg_catalog.array_append,STYPE=integer[],INITCOND='{}',FINALFUNC=pg_catalog.cardinality"
				}
				enginetest.Run(t, e, "CREATE AGGREGATE io_null_demand(integer) ("+definition+")")
				_, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT io_null_demand(v) FROM init_owner_rows")
				if err := view.ValidateQueryJoinExecution(evaluation, project, evaluation.Catalog, 0); err != nil {
					t.Fatal(err)
				}
				selected := *project.Child.(*plan.AggregateNode).Calls[0].Provider.(*exec.TransitionAggregate)
				calls, finals := 0, 0
				original := selected.Call
				selected.Call = sepgsqlAggregateCallProbe{Info: expr.InfoOf(original), run: func(c *exec.Context) (sqltype.Value, error) {
					calls++
					if name == "nonstrict_null" && (selected.Support.Strict || len(c.Parameters) != 2 || !c.Parameters[1].IsNull()) {
						t.Fatal("real nonstrict selected NULL facts changed", c.Parameters)
					}
					return original.Eval(c)
				}}
				if selected.FinalCall != nil {
					final := selected.FinalCall
					selected.FinalCall = sepgsqlAggregateCallProbe{Info: expr.InfoOf(final), run: func(c *exec.Context) (sqltype.Value, error) {
						finals++
						return final.Eval(c)
					}}
				}
				state, err := selected.InitWithContext(evaluation.Context, evaluation)
				if err != nil {
					t.Fatal(err)
				}
				mark := scope.MarkSepgsqlObservations()
				if name != "strict_final_null" {
					if err = selected.Advance(evaluation.Context, state, []sqltype.Value{sqltype.Null(selected.Inputs[1].Type)}, evaluation); err != nil {
						t.Fatal(err)
					}
				}
				if name == "strict_unseeded" {
					seven, err := sqltype.Integer(selected.Inputs[1].Type, 7)
					if err != nil {
						t.Fatal(err)
					}
					if err = selected.Advance(evaluation.Context, state, []sqltype.Value{seven}, evaluation); err != nil {
						t.Fatal(err)
					}
				}
				result, err := selected.FinalizeWithContext(evaluation.Context, state, nil, evaluation)
				if err != nil || result.Type().ID() != sqltype.Int4 {
					t.Fatal("selected native NULL final result", result, err)
				}
				if name == "strict_final_null" {
					if !result.IsNull() || calls != 0 || finals != 0 {
						t.Fatal("strict empty NULL demanded a fake support body", result, calls, finals)
					}
				} else {
					want, wantCalls, wantFinals := int64(0), 0, 0
					if name == "strict_unseeded" {
						want, wantFinals = 7, 1
					} else if name == "nonstrict_null" {
						want, wantCalls, wantFinals = 1, 1, 1
					}
					got, err := result.Integer()
					if err != nil || got != want || calls != wantCalls || finals != wantFinals {
						t.Fatal("native strictness/demand", got, calls, finals, err)
					}
				}
				if scope.MarkSepgsqlObservations() != mark {
					t.Fatal("original fmgr support fabricated receipt or FHET work")
				}
			})
		}
	})
	t.Run("native_installed_aggregate_support_is_not_explicit_execute", func(t *testing.T) {
		e := sepgsqlSupportEngine(t)
		for _, query := range []string{
			"CREATE TABLE io_hstore_rows(h hstore)",
			"INSERT INTO io_hstore_rows VALUES('a=>1'),(NULL),('b=>2')",
			"CREATE AGGREGATE io_hstore_sum(hstore) (SFUNC=public.hs_concat,STYPE=hstore,COMBINEFUNC=public.hs_concat,FINALFUNC=public.hstore_to_array,INITCOND='',PARALLEL=SAFE)",
			"SECURITY LABEL ON FUNCTION public.hs_concat(hstore,hstore) IS 'S'",
			"SECURITY LABEL ON FUNCTION public.hstore_to_array(hstore) IS 'S'",
		} {
			enginetest.Run(t, e, query)
		}
		op, done := enginetest.RoleOperation(t, e, "postgres")
		t.Cleanup(done)
		enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
		enginetest.RoleError(t, e, op, "SELECT public.hs_concat('a=>1'::hstore,'b=>2'::hstore)", "42501", "SELinux: security policy violation", "")
		for _, witness := range []struct {
			query string
			want  string
		}{
			{"SELECT io_hstore_sum(h)::text FROM io_hstore_rows", "{a,1,b,2}"},
			{"SELECT io_hstore_sum(h)::text FROM io_hstore_rows WHERE false", "{}"},
			{"SELECT io_hstore_sum(h)::text FROM io_hstore_rows WHERE h IS NULL", "{}"},
		} {
			if got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, witness.query)); !reflect.DeepEqual(got, [][]driver.Value{{witness.want}}) {
				t.Fatal("native aggregate borrowed explicit-call execute", witness.query, got)
			}
			if op.Session.Sepgsql.Current() != "R" {
				t.Fatal("native aggregate support leaked trusted subject")
			}
		}
		for _, helper := range []string{"public.hs_concat(hstore,hstore)", "public.hstore_to_array(hstore)"} {
			enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION "+helper+" IS 'U'")
			enginetest.RoleError(t, e, op, "SELECT io_hstore_sum(h)::text FROM io_hstore_rows", "42501", "SELinux: security policy violation", "")
			if op.Session.Sepgsql.Current() != "R" {
				t.Fatal("native aggregate denial leaked trusted subject")
			}
			enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION "+helper+" IS 'S'")
			if got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT io_hstore_sum(h)::text FROM io_hstore_rows")); !reflect.DeepEqual(got, [][]driver.Value{{"{a,1,b,2}"}}) {
				t.Fatal("denied native support published partial state or poisoned retry", got)
			}
		}
	})
	t.Run("native_installed_combine_uses_its_actual_entry", func(t *testing.T) {
		for _, label := range []string{"S", "U"} {
			t.Run(label, func(t *testing.T) {
				e := sepgsqlSupportEngine(t)
				enginetest.Run(t, e, "CREATE TABLE io_combine_rows(h hstore)")
				enginetest.Run(t, e, "CREATE AGGREGATE io_native_combine(hstore) (SFUNC=public.hs_concat,STYPE=hstore,COMBINEFUNC=public.hs_concat,INITCOND='',PARALLEL=SAFE)")
				enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION public.hs_concat(hstore,hstore) IS '"+label+"'")
				op, done := enginetest.RoleOperation(t, e, "postgres")
				t.Cleanup(done)
				enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { scope.Finish(nil) })
				raw := enginetest.Statement(t, "SELECT io_native_combine(h) FROM io_combine_rows")
				if err = scope.Gate(raw); err != nil {
					t.Fatal(err)
				}
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				var project *plan.ProjectNode
				if err == nil {
					binding, failure := core.Describe(op.Context(), e.AnalysisHost(op), raw, auth, ids.ObjectRef{}, scope.Input, nil, nil)
					err = failure
					if err == nil {
						project = binding.Query.Root.(*plan.ProjectNode)
						scope.RoutineAuth, scope.RoutineDependencies = auth, binding.Dependencies
					}
				}
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				evaluation := &exec.Context{
					EvalFrame:     exec.EvalFrame{Scope: 1, Frame: project.Frame},
					EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output},
				}
				if err = exec.BeginRoutineInitialization(evaluation); err != nil {
					t.Fatal(err)
				}
				if err = view.ValidateQueryJoinExecution(evaluation, project, evaluation.Catalog, 0); err != nil {
					t.Fatal(err)
				}
				selected := project.Child.(*plan.AggregateNode).Calls[0].Provider.(*exec.TransitionAggregate)
				left, err := selected.InitWithContext(evaluation.Context, evaluation)
				if err != nil {
					t.Fatal(err)
				}
				right, err := selected.InitWithContext(evaluation.Context, evaluation)
				if err != nil {
					t.Fatal(err)
				}
				want := ""
				if label == "S" {
					for i, text := range []string{"a=>1", "b=>2"} {
						value, err := selected.Inputs[1].Type.InputWithEnvironment(evaluation.Context, text, -1, evaluation.FunctionInput())
						if err != nil {
							t.Fatal(err)
						}
						state := left
						if i == 1 {
							state = right
						}
						if err = selected.Advance(evaluation.Context, state, []sqltype.Value{value}, evaluation); err != nil {
							t.Fatal("execute-denied native transition", err)
						}
					}
					want = `"a"=>"1", "b"=>"2"`
				}
				err = selected.Combine(evaluation.Context, left, right, evaluation)
				if label == "U" {
					enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "SELinux: security policy violation"})
				} else if err != nil {
					t.Fatal("genuine native combine acquired explicit-call execute", err)
				}
				result, err := selected.Finalize(evaluation.Context, left)
				if err != nil {
					t.Fatal(err)
				}
				got, err := result.Type().OutputWithEnvironment(evaluation.Context, result, evaluation.FunctionOutput())
				if err != nil || got != want || op.Session.Sepgsql.Current() != "R" {
					t.Fatal("combine entry/state/unwind", got, want, err, op.Session.Sepgsql.Current())
				}
			})
		}
	})
	e := sepgsqlInitializationEngine(t)
	op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT v FROM init_owner_rows ORDER BY hstore(v::text,v::text)")
	sort, ok := project.Child.(*plan.SortNode)
	if !ok || len(sort.Keys) != 1 {
		t.Fatal("missing actual selected sort")
	}
	var support sqltype.IndexSupport
	var slot uint16
	count := 0
	if err := sort.Keys[0].Comparison.VisitIndexSupports(func(selected sqltype.IndexSupport, role uint16) error {
		support, slot = selected, role
		count++
		return exec.InitializeIndexSupport(evaluation, selected, role)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 || slot != 1 {
		t.Fatal("selected comparator did not identify exactly its real role", count, slot)
	}
	callback, err := catalog.CaptureIndexRoutineCallback(evaluation.Catalog, support, slot)
	if err != nil {
		t.Fatal(err)
	}
	site := exec.DirectRoutineInitializationSite{Routine: callback.Helper, Reference: callback.Reference, Purpose: exec.DirectIndexSupport, Source: callback.Source, Ordinal: callback.Slot}
	host := core.HostOf(scope)
	owner := evaluation.RoutineInitialization
	if err = host.ValidateDirectRoutineInitialization(owner, site); err != nil {
		t.Fatal("selected support source not initialized", err)
	}
	// pg_amproc has no SQL owner. Its real family supplies ordinary source
	// ownership, but only for the exact selected member/helper/role association.
	beforeInvalid := scope.MarkSepgsqlObservations()
	for name, mutate := range map[string]func(*exec.DirectRoutineInitializationSite){
		"family_is_not_member": func(candidate *exec.DirectRoutineInitializationSite) {
			candidate.Source = evaluation.Catalog.Domain().Ref(evaluation.Catalog.Epoch(), callback.Family.Address())
		},
		"procedure_is_not_member": func(candidate *exec.DirectRoutineInitializationSite) {
			candidate.Source = candidate.Reference
		},
		"member_column_is_not_source": func(candidate *exec.DirectRoutineInitializationSite) {
			candidate.Source.Address.SubID = 1
		},
		"missing_member": func(candidate *exec.DirectRoutineInitializationSite) {
			candidate.Source.Address.ObjectID = ids.OID(1 << 31)
		},
		"stale_member_epoch": func(candidate *exec.DirectRoutineInitializationSite) {
			candidate.Source.Epoch++
		},
		"wrong_support_role": func(candidate *exec.DirectRoutineInitializationSite) {
			candidate.Ordinal++
		},
		"wrong_helper_reference": func(candidate *exec.DirectRoutineInitializationSite) {
			candidate.Reference.Address.ObjectID++
		},
	} {
		candidate := site
		mutate(&candidate)
		if err = host.InitializeDirectRoutine(owner, candidate); err == nil {
			t.Fatal(name, "initialized a substituted support source")
		}
		if err = host.ValidateDirectRoutineInitialization(owner, candidate); err == nil {
			t.Fatal(name, "reused a support source certificate")
		}
	}
	if scope.MarkSepgsqlObservations() != beforeInvalid {
		t.Fatal("rejected support source changed initialization receipts")
	}
	mark := scope.MarkSepgsqlObservations()
	if err = exec.InitializeIndexSupport(evaluation, support, slot); err != nil {
		t.Fatal(err)
	}
	if scope.MarkSepgsqlObservations() != mark {
		t.Fatal("same selected support minted a per-comparison certificate")
	}
	proof, err := host.SealRoutineInitialization(owner, evaluation.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	closed := *evaluation
	closed.Host = nil
	closed.ClosedEvaluation = &exec.EvaluationCapture{Routines: proof}
	body, err := closed.EnterIndexSupport(support, slot)
	if err != nil {
		t.Fatal("closed selected comparator could not consume genuine proof", err)
	}
	body.Leave()
	other, err := host.NewRoutineInitialization()
	if err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string]exec.RoutineInitialization{"zero": {}, "different_actual_frame": other} {
		copy := closed
		copy.RoutineInitialization = candidate
		if _, err = copy.EnterIndexSupport(support, slot); err == nil {
			t.Fatal(name, "adopted selected support proof")
		}
	}
	wrong := site
	wrong.Source = site.Reference
	if err = proof.CheckDirect(owner, wrong, evaluation.Catalog); err == nil {
		t.Fatal("procedure ref substituted for selected family-member source")
	}
	wrong = site
	wrong.Ordinal++
	if err = proof.CheckDirect(owner, wrong, evaluation.Catalog); err == nil {
		t.Fatal("wrong callback role adopted certificate")
	}
	wrong = site
	wrong.Purpose = exec.DirectHash
	if err = proof.CheckDirect(owner, wrong, evaluation.Catalog); err == nil {
		t.Fatal("hash purpose adopted comparator certificate")
	}
	missing := closed
	missing.Catalog = catalog.Read{}
	if _, err = missing.EnterIndexSupport(support, slot); err == nil {
		t.Fatal("copied support context lost catalog but adopted live authority")
	}
	foreignEngine := sepgsqlInitializationEngine(t)
	_, _, foreignProject, foreignEvaluation := sepgsqlInitializationBinding(t, foreignEngine, "SELECT v FROM init_owner_rows ORDER BY hstore(v::text,v::text)")
	foreignSort := foreignProject.Child.(*plan.SortNode)
	if err = foreignSort.Keys[0].Comparison.VisitIndexSupports(func(selected sqltype.IndexSupport, role uint16) error {
		foreignCallback, failure := catalog.CaptureIndexRoutineCallback(foreignEvaluation.Catalog, selected, role)
		if failure != nil {
			return failure
		}
		candidate := site
		candidate.Source = foreignCallback.Source
		if failure = host.InitializeDirectRoutine(owner, candidate); failure == nil {
			t.Fatal("foreign member supplied source ownership for local helper")
		}
		if failure = host.ValidateDirectRoutineInitialization(owner, candidate); failure == nil {
			t.Fatal("foreign member reused local helper certificate")
		}
		candidate = site
		candidate.Reference = foreignCallback.Reference
		if failure = host.InitializeDirectRoutine(owner, candidate); failure == nil {
			t.Fatal("foreign helper reference initialized a local support source")
		}
		if failure = host.ValidateDirectRoutineInitialization(owner, candidate); failure == nil {
			t.Fatal("foreign helper reference reused a local support certificate")
		}
		// IndexSupport is inert metadata, not a catalog/domain owner. Equal
		// tokens resolve locally; the captured callback and runtime source
		// carry the actual provenance that must not cross catalogs.
		if selected != support || role != slot {
			t.Fatal("fixture no longer selects equivalent inert support metadata")
		}
		localBody, failure := closed.EnterIndexSupport(selected, role)
		if failure != nil {
			t.Fatal("equivalent token lost its genuine local sealed source", failure)
		}
		localBody.Leave()
		if failure = foreignCallback.Validate(evaluation.Catalog); failure == nil {
			t.Fatal("foreign captured callback adopted local catalog provenance")
		}
		foreignRead := closed
		foreignRead.Catalog = foreignEvaluation.Catalog
		if _, failure = foreignRead.EnterIndexSupport(selected, role); failure == nil {
			t.Fatal("foreign read adopted local selected support proof")
		}
		foreignOwner := closed
		foreignOwner.RoutineInitialization = foreignEvaluation.RoutineInitialization
		if _, failure = foreignOwner.EnterIndexSupport(selected, role); failure == nil {
			t.Fatal("foreign owner adopted local selected support proof")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	assertInertInitializationPayload(t, proof)
	// The kernel, not just a certificate lookup, must consume that exact callback.
	typ, ok := evaluation.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(support.Left)})
	if !ok {
		t.Fatal("missing real comparator operand type")
	}
	a, err := typ.SQLType.InputWithEnvironment(evaluation.Context, "a=>1", -1, evaluation.FunctionInput())
	if err != nil {
		t.Fatal(err)
	}
	b, err := typ.SQLType.InputWithEnvironment(evaluation.Context, "b=>1", -1, evaluation.FunctionInput())
	if err != nil {
		t.Fatal(err)
	}
	beforeComparisons := scope.MarkSepgsqlObservations()
	for range 2 {
		sign, failure := sort.Keys[0].Comparison.Compare(evaluation.Context, a, b, closed.FunctionInput())
		if failure != nil || sign >= 0 {
			t.Fatal("actual sealed hstore comparison", sign, failure)
		}
	}
	if scope.MarkSepgsqlObservations() != beforeComparisons || closed.RoutineInitialization != owner {
		t.Fatal("repeated sealed comparisons minted or changed their source owner")
	}
	saved := op.Ctx
	canceled, cancel := context.WithCancel(saved)
	cancel()
	op.Ctx = canceled
	initializeErr := host.InitializeDirectRoutine(owner, site)
	validateErr := host.ValidateDirectRoutineInitialization(owner, site)
	op.Ctx = saved
	if !errors.Is(initializeErr, context.Canceled) || !errors.Is(validateErr, context.Canceled) {
		t.Fatal("canceled selected source remained initialized", initializeErr, validateErr)
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if err = host.InitializeDirectRoutine(owner, site); err == nil {
		t.Fatal("finished source minted support initialization")
	}
	if err = host.ValidateDirectRoutineInitialization(owner, site); err == nil {
		t.Fatal("finished source retained live support authority")
	}
}
