package core_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestCheckProgramInvokerAndRIOwnerPhases(t *testing.T) {
	for _, folded := range []bool{false, true} {
		for _, firstRI := range []bool{false, true} {
			for _, sameActor := range []bool{false, true} {
				t.Run(map[bool]string{false: "row", true: "folded"}[folded]+"/"+map[bool]string{false: "invoker_first", true: "ri_first"}[firstRI]+"/"+map[bool]string{false: "different_actor", true: "same_actor"}[sameActor], func(t *testing.T) {
					e := engine.New()
					admin, finish := enginetest.RoleOperation(t, e, "postgres")
					defer finish()
					owner := "check_phase_owner"
					if sameActor {
						owner = "check_phase_caller"
					}
					for _, q := range []string{
						"CREATE ROLE check_phase_caller LOGIN",
						"CREATE ROLE check_phase_owner LOGIN",
						"CREATE SEQUENCE public.check_phase_steps START 1",
						"CREATE TABLE public.check_phase_child(j JSON,CONSTRAINT a_call CHECK(" + checkProgramExpression(folded) + "),CONSTRAINT z_truth CHECK(nextval('public.check_phase_steps'::regclass)>0))",
						"ALTER TABLE public.check_phase_child OWNER TO " + owner,
						"GRANT USAGE ON SCHEMA public TO check_phase_caller,check_phase_owner",
						"GRANT USAGE ON SEQUENCE public.check_phase_steps TO check_phase_caller,check_phase_owner",
						"REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC",
					} {
						enginetest.RoleExecute(t, e, admin, q)
					}
					allowed := "check_phase_caller"
					if firstRI {
						allowed = owner
					}
					enginetest.RoleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO "+allowed)
					caller, callerDone := enginetest.RoleOperation(t, e, "check_phase_caller")
					defer callerDone()
					scope, err := e.StartStatement(caller)
					if err != nil {
						t.Fatal(err)
					}
					defer scope.Finish(nil)
					r, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
					if err != nil {
						t.Fatal(err)
					}
					if r.Rows != nil {
						r.Rows.Close()
					}
					e.Mu.Lock()
					auth, err := e.AuthorizationLocked(caller)
					e.Mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					view := auth.Catalog
					ref := enginetest.CatalogRef(t, e, view, "check_phase_child")
					child := view.MustRelation(ref.Address)
					ownerAuth := core.AuthorizationActingAs(auth, child.Owner)
					value, err := child.Columns[0].Usage.Type.Input(context.Background(), "[1,2]", -1)
					if err != nil {
						t.Fatal(err)
					}
					invoker := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope), Catalog: view, Input: scope.Input, Output: scope.Output}}
					local := invoker
					local.RoutineAuthority = &ownerAuth
					check := func(c *exec.Context) error {
						if c == &local {
							scope.ForeignActionAuth = &ownerAuth
							defer func() { scope.ForeignActionAuth = nil }()
						}
						failed, err := table.ValidateCheckRow(child, []sqltype.Value{value}, c)
						if failed != nil {
							t.Fatal("unexpected CHECK violation", failed.Name)
						}
						return err
					}
					first, second := &invoker, &local
					if firstRI {
						first, second = second, first
					}
					if err := check(first); err != nil {
						t.Fatal(err)
					}
					// Borrow the actual cache from the first initialized context. No test
					// assumes a future cache value layout or allocates a fake certificate.
					second.PreparedChecks = first.PreparedChecks
					if sameActor {
						if err := check(second); err != nil {
							t.Fatal(err)
						}
						seen := [2]bool{}
						for _, programme := range core.ScopeRoutinePrograms(scope) {
							for _, call := range core.CommandRoutineProgramAdmission(&programme).Calls {
								if call.Consumer == child && call.Actor == auth.State.CurrentUser {
									if core.CommandRoutineProgramRi(&programme) {
										seen[1] = true
									} else {
										seen[0] = true
									}
								}
							}
						}
						if !seen[0] || !seen[1] {
							t.Error("equal-principal invoker and trusted RI phases were conflated", seen)
						}
					} else {
						nullCountUnitDiagnostic(t, check(second), sqlstate.Error{Code: "42501", Message: "permission denied for function json_array_length"})
					}
					sequence := enginetest.CatalogRef(t, e, view, "check_phase_steps")
					e.Mu.Lock()
					before := *e.Storage.Sequences[sequence.Address]
					e.Mu.Unlock()
					want := int64(1)
					if sameActor {
						want = 2
					}
					if before.Last != want {
						t.Fatal("denied phase evaluated volatile truth", before.Last, want)
					}
					if scope.RoutineAuth.State.CurrentUser != auth.State.CurrentUser {
						t.Fatal("RI phase replaced outer invoker")
					}
					enginetest.RoleExecute(t, e, admin, "REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM "+allowed)
					e.Mu.Lock()
					current, err := e.AuthorizationLocked(caller)
					e.Mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					nullCountUnitDiagnostic(t, core.ScopeReadmitPreparedPrograms(scope, current), sqlstate.Error{Code: "42501", Message: "permission denied for function json_array_length"})
					e.Mu.Lock()
					after := *e.Storage.Sequences[sequence.Address]
					e.Mu.Unlock()
					if after != before {
						t.Fatal("actor readmission replayed truth", before, after)
					}
				})
			}
		}
	}
}

func TestCheckProgramOriginalManifestRetention(t *testing.T) {
	for _, folded := range []bool{false, true} {
		t.Run(map[bool]string{false: "row", true: "folded"}[folded], func(t *testing.T) {
			e := engine.New()
			admin, finish := enginetest.RoleOperation(t, e, "postgres")
			defer finish()
			for _, q := range []string{
				"CREATE ROLE check_manifest_reader LOGIN",
				"CREATE SEQUENCE public.check_manifest_steps START 1",
				"CREATE TABLE public.check_manifest_rows(j JSON,CONSTRAINT a_call CHECK(" + checkProgramExpression(folded) + "),CONSTRAINT z_truth CHECK(nextval('public.check_manifest_steps'::regclass)>0))",
				"GRANT USAGE ON SCHEMA public TO check_manifest_reader",
				"GRANT USAGE ON SEQUENCE public.check_manifest_steps TO check_manifest_reader",
				"REVOKE EXECUTE ON FUNCTION pg_catalog.json_array_length(json) FROM PUBLIC",
			} {
				enginetest.RoleExecute(t, e, admin, q)
			}
			reader, readerDone := enginetest.RoleOperation(t, e, "check_manifest_reader")
			defer readerDone()
			scope, err := e.StartStatement(reader)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			r, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if r.Rows != nil {
				r.Rows.Close()
			}
			view := e.CatalogRead()
			ref := enginetest.CatalogRef(t, e, view, "check_manifest_rows")
			d := view.MustRelation(ref.Address)
			value, err := d.Columns[0].Usage.Type.Input(context.Background(), "[1,2]", -1)
			if err != nil {
				t.Fatal(err)
			}
			trees := func() string {
				var text strings.Builder
				for _, c := range d.Constraints.Check {
					if err := plan.AppendBoundTree(context.Background(), &text, c.Expression); err != nil {
						t.Fatal(err)
					}
				}
				return text.String()
			}
			beforeTree := trees()
			dependencies := make([][]catalog.ExpressionDependency, len(d.Constraints.Check))
			for i, c := range d.Constraints.Check {
				dependencies[i] = append([]catalog.ExpressionDependency(nil), c.Dependencies...)
			}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope), Catalog: view, Input: scope.Input, Output: scope.Output}}
			evaluation.RoutineChecks = d.Constraints.Check[1].Dependencies
			// An unrelated borrowed manifest must never replace either stored
			// constraint's genuine original dependencies.
			for range 2 {
				_, err := table.ValidateCheckRow(d, []sqltype.Value{value}, &evaluation)
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function json_array_length"})
				if _, ok := evaluation.PreparedChecks[d]; ok {
					t.Fatal("failed preflight cached a partial CHECK set")
				}
				if len(core.ScopeRoutinePrograms(scope)) != 0 {
					t.Fatal("failed preflight registered a partial consumed graph")
				}
			}
			enginetest.RoleExecute(t, e, admin, "GRANT EXECUTE ON FUNCTION pg_catalog.json_array_length(json) TO check_manifest_reader")
			e.Mu.Lock()
			current, err := e.AuthorizationLocked(reader)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			evaluation.RoutineAuthority = &current
			calls := func() int {
				count := 0
				for _, programme := range core.ScopeRoutinePrograms(scope) {
					count += len(core.CommandRoutineProgramAdmission(&programme).Calls)
				}
				return count
			}
			firstCalls := 0
			for i := range 2 {
				failed, err := table.ValidateCheckRow(d, []sqltype.Value{value}, &evaluation)
				if err != nil || failed != nil {
					t.Fatal(failed, err)
				}
				if i == 0 {
					firstCalls = calls()
				} else if calls() != firstCalls {
					t.Fatal("same phase duplicated consumed original calls")
				}
			}
			if trees() != beforeTree {
				t.Fatal("CHECK cooking mutated immutable original expression")
			}
			for i, c := range d.Constraints.Check {
				if !reflect.DeepEqual(c.Dependencies, dependencies[i]) {
					t.Fatal("CHECK cooking mutated original manifest", c.Name)
				}
			}
			if err := table.ValidateChecks(view, d, map[string]bool{}); err != nil {
				t.Fatal("original references became invalid", err)
			}
			seenJSON, seenSequence := false, false
			for _, programme := range core.ScopeRoutinePrograms(scope) {
				for _, call := range core.CommandRoutineProgramAdmission(&programme).Calls {
					if call.Consumer != d || call.Actor != current.State.CurrentUser {
						t.Fatal("lost genuine CHECK consumer/actor", call)
					}
					found := false
					for _, deps := range dependencies {
						for _, dep := range deps {
							if dep.Ref == call.Reference {
								found = true
							}
						}
					}
					if !found {
						t.Fatal("call is not from an original constraint manifest", call)
					}
					seenJSON = seenJSON || call.Routine.Name() == "json_array_length"
					seenSequence = seenSequence || call.Routine.Name() == "nextval"
				}
			}
			if !seenJSON || !seenSequence {
				t.Fatal("ordinary CHECK lost folded/row or volatile original call", seenJSON, seenSequence)
			}
			sequence := enginetest.CatalogRef(t, e, view, "check_manifest_steps")
			e.Mu.Lock()
			last := e.Storage.Sequences[sequence.Address].Last
			e.Mu.Unlock()
			if last != 2 {
				t.Fatal("failed preflight or reused preparation replayed truth", last)
			}
		})
	}
}
