package views_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// Exercise the real delegated SQLRoutineExpr under its live root activation.
// A value copy is expected; independently copied argument/assembly backing is
// not that prepared call, even when every value and selected provider agrees.
func TestPreparedAcceptedDefaultInvocation(t *testing.T) {
	for _, language := range []string{"sql", "pl"} {
		for _, kind := range []string{"prepared", "query-prepared", "explicit-over-query", "foreign-host", "source", "descriptor", "arguments", "argument-value", "names", "input-map", "child-slots", "signature", "occurrence", "written-count", "variadic", "properties"} {
			t.Run(language+"/"+kind, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				before, after, want := "RETURN a+1", "RETURN a+10", int64(5)
				if language == "pl" {
					before, after, want = "LANGUAGE plpgsql AS $$BEGIN RETURN a+1; END$$", "LANGUAGE plpgsql AS $$BEGIN RETURN a+10; END$$", 14
				}
				for _, query := range []string{
					"CREATE FUNCTION invocation_arg(a integer) RETURNS integer " + before,
					"CREATE TABLE invocation_rows(v integer DEFAULT invocation_arg(a=>1+3))",
				} {
					enginetest.SessionSQL(t, s, query)
				}
				read := e.CatalogRead()
				ref := enginetest.CatalogRef(t, e, read, "invocation_rows")
				relation := read.MustRelation(ref.Address)
				source, origin := analyze.ColumnDefault(read, relation, 0)
				key := exec.DefaultOccurrence{Statement: enginetest.Statement(t, "INSERT INTO invocation_rows DEFAULT VALUES")}
				owner := exec.NewAcceptedDefaultSet()
				if _, _, err := prepareAcceptedDefault(t, e, owner, key, relation, ref, relation.Attributes[0]); err != nil {
					t.Fatal(err)
				}
				if err := owner.Seal(41); err != nil {
					t.Fatal(err)
				}
				artifact, err := owner.Lookup(key, ref, relation.Attributes[0])
				if err != nil || artifact == nil {
					t.Fatal("missing completed source preparation", err)
				}
				origin.Accepted, origin.Occurrence = artifact, key
				root := artifact.Expression()
				backing := root.(expr.Composite).Children()[0].(expr.Composite).Children()
				if _, literal := backing[0].(plan.LiteralExpr); !literal {
					t.Fatal("fixture did not fold the actual prepared argument")
				}
				if _, literal := source.(plan.SQLRoutineExpr).Args[0].(plan.LiteralExpr); literal {
					t.Fatal("fixture lost the distinct original argument tree")
				}
				enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION invocation_arg(a integer) RETURNS integer "+after)
				var foreign *core.StatementScope
				if kind == "explicit-over-query" || kind == "foreign-host" {
					other := engine.New()
					op, done := enginetest.RoleOperation(t, other, "postgres")
					foreign, err = other.StartStatement(op)
					if err != nil {
						done()
						t.Fatal(err)
					}
					defer func() {
						_ = foreign.Finish(context.Canceled)
						done()
					}()
				}
				enginetest.PreparedFoldProtocolCommand(t, e, s, s.NewProtocolCycle(), func(scope *core.StatementScope) error {
					e.Mu.Lock()
					auth, err := scope.RoutineAuthorizationLocked()
					e.Mu.Unlock()
					if err != nil {
						return err
					}
					var program exec.RoutineProgram
					c := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Catalog: auth.Catalog, Host: core.HostOf(scope), Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, DefaultOccurrence: key}}
					c.RoutineProgram = &program
					if kind == "query-prepared" {
						c.Host = nil
						c.Query = &exec.State{Source: &exec.Source{Host: core.HostOf(scope)}}
					}
					if kind == "explicit-over-query" {
						c.Query = &exec.State{Source: &exec.Source{Host: core.HostOf(foreign)}}
					}
					analyze.ApplyDefaultOrigin(origin, &c)
					var visit func(*exec.Context, expr.Expr) error
					visit = func(local *exec.Context, node expr.Expr) error {
						if handled, err := exec.AdmitAcceptedDefault(local, node, visit); handled {
							return err
						}
						call, ok := node.(plan.SQLRoutineExpr)
						if !ok {
							return fmt.Errorf("missing actual prepared call")
						}
						switch kind {
						case "foreign-host":
							// Mutate only the borrowed local evaluation, never the
							// accepted artifact or its original active host.
							local.Host = core.HostOf(foreign)
						case "source":
							call = source.(plan.SQLRoutineExpr)
						case "descriptor":
							copy := *call.Procedure
							call.Procedure, call.RoutineReference = &copy, &copy
						case "arguments", "argument-value":
							call.Args = slices.Clone(call.Args)
							if kind == "argument-value" {
								call.Args[0] = source.(plan.SQLRoutineExpr).Args[0]
							}
						case "names":
							call.ArgNames = slices.Clone(call.ArgNames)
						case "input-map":
							call.InputMap = slices.Clone(call.InputMap)
						case "child-slots":
							call.ChildSlots = slices.Clone(call.ChildSlots)
						case "signature":
							call.Signature.Inputs = slices.Clone(call.Signature.Inputs)
						case "occurrence":
							call.Occurrence++
						case "written-count":
							call.WrittenCount--
						case "variadic":
							call.Variadic = !call.Variadic
						case "properties":
							call.Property.Volatility = expr.Immutable
						}
						admission := exec.AdmitRoutineCall(local, call, true, false)
						snapshotAcceptedPureDemand(t, &program, artifact, auth.Catalog)
						value, invocation := local.EvalSQLRoutine(call)
						if kind == "prepared" || kind == "query-prepared" || kind == "explicit-over-query" {
							if admission != nil || invocation != nil {
								return fmt.Errorf("owned prepared invocation rejected: admission=%v invocation=%v", admission, invocation)
							}
							got, err := value.Integer()
							if err != nil || got != want || value.Type() != call.Usage.Type {
								return fmt.Errorf("invocation did not preserve authenticated mode and prepared arguments: value=%v error=%v want=%d", value, err, want)
							}
						} else if admission == nil || invocation == nil {
							return fmt.Errorf("foreign prepared value acquired exact occurrence authority: admission=%v invocation=%v", admission, invocation)
						}
						return nil
					}
					return visit(&c, root)
				})
				if root != artifact.Expression() || !reflect.DeepEqual(backing, root.(expr.Composite).Children()[0].(expr.Composite).Children()) || len(e.Storage.Relations[relation.Address()].Versions) != 0 {
					t.Fatal("invocation changed accepted backing or produced command effects")
				}
			})
		}
	}
}

func TestPreparedAcceptedDefaultHiddenIsolation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE FUNCTION isolated_arg(a integer) RETURNS integer RETURN a+1",
		"CREATE FUNCTION isolated_outer(a integer,b integer DEFAULT isolated_arg(9)) RETURNS integer RETURN a+b",
		"CREATE TABLE isolated_rows(v integer DEFAULT isolated_outer(4))",
		"CREATE ROLE isolated_actor LOGIN",
		"GRANT USAGE ON SCHEMA public TO isolated_actor",
		"REVOKE EXECUTE ON FUNCTION isolated_arg(integer),isolated_outer(integer,integer) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION isolated_arg(integer),isolated_outer(integer,integer) TO isolated_actor",
	} {
		enginetest.SessionSQL(t, s, query)
	}
	read := e.CatalogRead()
	ref := enginetest.CatalogRef(t, e, read, "isolated_rows")
	relation := read.MustRelation(ref.Address)
	_, origin := analyze.ColumnDefault(read, relation, 0)
	key := exec.DefaultOccurrence{Statement: enginetest.Statement(t, "INSERT INTO isolated_rows DEFAULT VALUES")}
	owner := exec.NewAcceptedDefaultSet()
	if _, _, err := prepareAcceptedDefault(t, e, owner, key, relation, ref, relation.Attributes[0]); err != nil {
		t.Fatal(err)
	}
	if err := owner.Seal(41); err != nil {
		t.Fatal(err)
	}
	artifact, err := owner.Lookup(key, ref, relation.Attributes[0])
	if err != nil || artifact == nil {
		t.Fatal("missing source-bound hidden-default fixture", err)
	}
	origin.Accepted, origin.Occurrence = artifact, key
	var program exec.RoutineProgram
	if err := admitAcceptedDefault(t, e, artifact, key, origin, artifact.Expression(), "isolated_actor", &program); err != nil {
		t.Fatal(err)
	}
	bounded, ordinary := 0, 0
	for _, call := range program.Calls {
		selected := !reflect.ValueOf(call).FieldByName("defaultCall").IsNil()
		switch call.Routine.Name() {
		case "isolated_outer":
			if !selected || call.Consumer != relation {
				t.Fatal("outer call lost its exact column DEFAULT occurrence")
			}
			bounded++
		case "isolated_arg":
			if selected || call.Consumer != nil {
				t.Fatal("hidden default inherited outer source or capability")
			}
			ordinary++
		}
	}
	if bounded != 1 || ordinary != 1 {
		t.Fatal("missing independently owned call occurrences", bounded, ordinary)
	}
	readmit := func() error {
		op, done := enginetest.RoleOperation(t, e, "isolated_actor")
		defer done()
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		return program.Readmit(core.RoutineAccessOf(auth))
	}
	enginetest.SessionSQL(t, s, "REVOKE EXECUTE ON FUNCTION isolated_arg(integer) FROM isolated_actor")
	if err := readmit(); err == nil || sqlStateOf(err) != "42501" {
		t.Fatal("hidden argument readmission borrowed parent permission", err)
	}
	enginetest.SessionSQL(t, s, "GRANT EXECUTE ON FUNCTION isolated_arg(integer) TO isolated_actor")
	if err := readmit(); err != nil {
		t.Fatal("live independent hidden-argument regrant failed", err)
	}
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION isolated_arg(a integer) RETURNS integer RETURN a+10")
	if err := readmit(); err == nil {
		t.Fatal("hidden argument acquired parent's predecessor privilege")
	}
	if len(e.Storage.Relations[relation.Address()].Versions) != 0 {
		t.Fatal("admission evaluated a hidden argument or command")
	}
}
