package views_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
)

// Exercise provider selection through the actual source-bound preparation,
// admission program and statement host, never through a mode constructor.
func TestPreparedAcceptedDefaultCurrentProvider(t *testing.T) {
	for _, language := range []string{"sql", "pl"} {
		for _, kind := range []string{"accepted", "copied-root", "copied-call", "raw-source", "foreign-source", "prepared-arguments", "wrong-occurrence", "retired-owner", "cancelled-operation", "missing-relation-link", "copied-relation", "reversed-relation", "copied-provider", "result", "input-abi", "strictness", "volatility", "security", "configuration", "transforms", "language", "missing-language", "foreign-language", "missing-handler", "foreign-handler", "body-kind"} {
			t.Run(language+"/"+kind, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				before, after, want := "RETURN a+1", "RETURN a+10", int64(5)
				if language == "pl" {
					before, after, want = "LANGUAGE plpgsql AS $$BEGIN RETURN a+2; END$$", "LANGUAGE plpgsql AS $$BEGIN RETURN a+20; END$$", 24
				}
				for _, query := range []string{
					"CREATE FUNCTION provider_arg(a integer) RETURNS integer " + before,
					"CREATE FUNCTION provider_other(a integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN a+200; END$$",
					"CREATE TABLE provider_rows(v integer DEFAULT provider_arg(a=>1+3),sibling integer DEFAULT provider_other(9))",
				} {
					enginetest.SessionSQL(t, s, query)
				}
				read := e.CatalogRead()
				ref := enginetest.CatalogRef(t, e, read, "provider_rows")
				relation := read.MustRelation(ref.Address)
				source, origin := analyze.ColumnDefault(read, relation, 0)
				key := exec.DefaultOccurrence{Statement: enginetest.Statement(t, "INSERT INTO provider_rows DEFAULT VALUES")}
				owner := exec.NewAcceptedDefaultSet()
				ordinary, _, err := prepareAcceptedDefault(t, e, owner, key, relation, ref, relation.Attributes[0])
				if err != nil {
					t.Fatal(err)
				}
				siblingKey := key
				siblingKey.Slot = 1
				if kind == "foreign-source" {
					if _, _, err := prepareAcceptedDefault(t, e, owner, siblingKey, relation, ref, relation.Attributes[1]); err != nil {
						t.Fatal(err)
					}
				}
				if err := owner.Seal(41); err != nil {
					t.Fatal(err)
				}
				artifact, err := owner.Lookup(key, ref, relation.Attributes[0])
				if err != nil || artifact == nil {
					t.Fatal("missing genuine source-bound artifact", err)
				}
				var foreignNode expr.Expr
				if kind == "foreign-source" {
					sibling, err := owner.Lookup(siblingKey, ref, relation.Attributes[1])
					if err != nil || sibling == nil {
						t.Fatal("missing genuine sibling artifact", err)
					}
					foreignNode = sibling.Expression().(expr.Composite).Children()[0]
				}
				origin.Accepted, origin.Occurrence = artifact, key
				root := artifact.Expression()
				wrapper := root.(expr.Composite).Children()[0]
				backing := wrapper.(expr.Composite).Children()
				prepared := ordinary.(plan.SQLRoutineExpr)
				if _, ok := prepared.Args[0].(plan.LiteralExpr); !ok {
					t.Fatal("fixture did not fold its real argument")
				}
				if _, ok := source.(plan.SQLRoutineExpr).Args[0].(plan.LiteralExpr); ok {
					t.Fatal("fixture lost original argument syntax")
				}
				definition := source.(plan.SQLRoutineExpr).Procedure.RoutineDefinition()
				dependencies := slices.Clone(artifact.Dependencies())
				var originalProgram exec.RoutineProgram
				if err := admitAcceptedDefault(t, e, artifact, key, origin, root, "postgres", &originalProgram); err != nil {
					t.Fatal(err)
				}
				enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION provider_arg(a integer) RETURNS integer "+after)
				enginetest.PreparedFoldProtocolCommand(t, e, s, s.NewProtocolCycle(), func(scope *core.StatementScope) error {
					e.Mu.Lock()
					auth, err := scope.RoutineAuthorizationLocked()
					e.Mu.Unlock()
					if err != nil {
						return err
					}
					current, _ := auth.Catalog.Procedure(prepared.Procedure.Address())
					state := catalog.CloneState(auth.Catalog.State())
					patch := *current
					negative := kind != "accepted"
					switch kind {
					case "missing-relation-link":
						next := *auth.Catalog.MustRelation(ref.Address)
						next.ScalarPublication = nil
						state.SetDescriptor(next.Address(), &next)
					case "copied-relation":
						next := *auth.Catalog.MustRelation(ref.Address)
						state.SetDescriptor(next.Address(), &next)
					case "reversed-relation":
						next := *relation
						next.ObjectRevision = auth.Catalog.MustRelation(ref.Address).Revision() + 1
						state.SetDescriptor(next.Address(), &next)
					case "copied-provider":
						patch.SQL, patch.SQLBody = prepared.Procedure.SQL, prepared.Procedure.SQLBody
					case "result":
						patch.Result = 25
					case "input-abi":
						code := *patch.SQL
						code.Inputs = slices.Clone(code.Inputs)
						code.Inputs[0] = auth.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 25}).SQLType.Usage()
						patch.SQL = &code
					case "strictness":
						patch.Strict = !patch.Strict
					case "volatility":
						patch.Volatility = expr.Immutable
					case "security":
						patch.SecurityDefiner = !patch.SecurityDefiner
					case "configuration":
						patch.Configuration = catalog.NewRoutineConfiguration([]string{"search_path=public"})
					case "transforms":
						patch.Transforms = catalog.NewRoutineTransforms([]catalog.RoutineTransform{{Type: prepared.Usage.Type}})
					case "language":
						patch.Language = ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 14}
						if patch.Language == current.Language {
							patch.Language.ObjectID = 13
						}
					case "body-kind":
						code := *patch.SQL
						code.Kind = proc.PLpgSQLBody
						provider, err := catalog.LookupLanguage(auth.Catalog, "plpgsql")
						if err != nil {
							return err
						}
						patch.Language = provider.Address()
						if language == "pl" {
							code.Kind = proc.SQLBody
							patch.Language = ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 14}
						}
						patch.SQL = &code
					case "missing-language":
						state.RemoveDescriptor(current.Language)
					case "foreign-language":
						other := engine.New()
						foreign, _ := other.CatalogRead().Get(current.Language).(*catalog.Language)
						if foreign == nil {
							return fmt.Errorf("missing independently owned language fixture")
						}
						state.SetDescriptor(current.Language, foreign)
					case "missing-handler", "foreign-handler":
						if language != "pl" {
							negative = false
							break
						}
						provider := auth.Catalog.Get(current.Language).(*catalog.Language)
						if kind == "missing-handler" {
							state.RemoveDescriptor(provider.Call)
						} else {
							foreign := *auth.Catalog.Get(provider.Call).(*catalog.Procedure)
							foreign.Owner = engine.New().CatalogRead().Owner().Principals.Principal(foreign.Owner.ID())
							state.SetDescriptor(provider.Call, &foreign)
						}
					}
					if kind == "copied-provider" || kind == "result" || kind == "input-abi" || kind == "strictness" || kind == "volatility" || kind == "security" || kind == "configuration" || kind == "transforms" || kind == "language" || kind == "body-kind" {
						state.SetDescriptor(patch.Address(), &patch)
					}
					auth.Recapture(auth.Catalog.WithState(state))
					snapshotAcceptedPureDemand(t, &originalProgram, artifact, auth.Catalog)
					ctx, cancel := context.WithCancel(scope.Op.Context())
					defer cancel()
					c := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: auth.Catalog, Host: core.HostOf(scope), Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, DefaultOccurrence: key}}
					if kind == "foreign-source" {
						// The unchanged raw sibling is independently callable.
						// Only transplanting its accepted wrapper into another
						// artifact/occurrence must reject.
						raw := relation.ColumnMeta.Defaults[relation.Attributes[1]].Expression
						value, err := raw.Eval(&c)
						if err != nil {
							return fmt.Errorf("ordinary current sibling rejected: %w", err)
						}
						got, err := value.Integer()
						if err != nil || got != 209 {
							return fmt.Errorf("ordinary current sibling value=%v error=%v want=209", value, err)
						}
					}
					analyze.ApplyDefaultOrigin(origin, &c)
					node := root
					switch kind {
					case "copied-root", "copied-call":
						original := root
						if kind == "copied-call" {
							original = wrapper
						}
						value := reflect.ValueOf(original)
						copy := reflect.New(value.Elem().Type())
						copy.Elem().Set(value.Elem())
						node = copy.Interface().(expr.Expr)
					case "raw-source":
						node = source
					case "foreign-source":
						node = foreignNode
					case "wrong-occurrence":
						c.DefaultOccurrence.Statement = enginetest.Statement(t, "INSERT INTO provider_rows DEFAULT VALUES")
					case "retired-owner":
						owner.Retire()
					case "cancelled-operation":
						cancel()
					}
					if kind == "prepared-arguments" {
						var visit func(*exec.Context, expr.Expr) error
						visit = func(local *exec.Context, expression expr.Expr) error {
							if handled, err := exec.AdmitAcceptedDefault(local, expression, visit); handled {
								return err
							}
							call := expression.(plan.SQLRoutineExpr)
							call.Args = slices.Clone(call.Args)
							if err := exec.AdmitRoutineCall(local, call, true, false); err == nil {
								return fmt.Errorf("recombined prepared backing admitted")
							}
							if _, err := local.EvalSQLRoutine(call); err == nil {
								return fmt.Errorf("recombined prepared backing executed")
							}
							return nil
						}
						return visit(&c, root)
					}
					admission := exec.AdmitExpression(&c, node, auth.Catalog)
					if negative {
						if admission == nil {
							return fmt.Errorf("invalid provider source/mode admitted: %s", kind)
						}
						if _, err := node.Eval(&c); err == nil {
							return fmt.Errorf("invalid provider source/mode executed: %s", kind)
						}
						return nil
					}
					if admission != nil {
						return fmt.Errorf("authentic %s/%s mode admission rejected: %w", language, kind, admission)
					}
					if err := originalProgram.Readmit(core.RoutineAccessOf(auth)); err != nil {
						return fmt.Errorf("genuine mode readmission rejected: %w", err)
					}
					for range 2 {
						value, err := node.Eval(&c)
						if err != nil {
							return err
						}
						got, err := value.Integer()
						if err != nil || got != want {
							return fmt.Errorf("wrong sealed provider selection or original prepared argument: value=%v error=%v want=%d", value, err, want)
						}
					}
					var phase exec.RoutineProgram
					if err := phase.InitializePhase(&originalProgram, core.RoutineAccessOf(auth)); err == nil {
						return fmt.Errorf("different phase inherited source mode")
					}
					owner.Retire()
					if err := originalProgram.Readmit(core.RoutineAccessOf(auth)); err == nil {
						return fmt.Errorf("retired current-provider owner readmitted")
					}
					if _, err := node.Eval(&c); err == nil {
						return fmt.Errorf("retired current-provider owner executed")
					}
					return nil
				})
				if artifact.Expression() != root || source.(plan.SQLRoutineExpr).Procedure.RoutineDefinition() != definition || !slices.Equal(artifact.Dependencies(), dependencies) || !reflect.DeepEqual(backing, wrapper.(expr.Composite).Children()) || !owner.MatchesGeneration(41) && owner.Live() || len(e.Storage.Relations[relation.Address()].Versions) != 0 {
					t.Fatal("provider selection mutated owned backing, generation, source or command effects")
				}
			})
		}
	}
}

// Hidden defaults retain independent manifests even when their containing
// column DEFAULT has a genuine current PL provider mode.
func TestPreparedAcceptedDefaultCurrentProviderIsolation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE FUNCTION provider_hidden_arg(a integer) RETURNS integer RETURN a+1",
		"CREATE FUNCTION provider_hidden_outer(a integer,b integer DEFAULT provider_hidden_arg(9)) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN a+b; END$$",
		"CREATE TABLE provider_hidden_rows(v integer DEFAULT provider_hidden_outer(4))",
		"CREATE ROLE provider_hidden_actor LOGIN",
		"GRANT USAGE ON SCHEMA public TO provider_hidden_actor",
		"REVOKE EXECUTE ON FUNCTION provider_hidden_arg(integer),provider_hidden_outer(integer,integer) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION provider_hidden_arg(integer),provider_hidden_outer(integer,integer) TO provider_hidden_actor",
	} {
		enginetest.SessionSQL(t, s, query)
	}
	read := e.CatalogRead()
	ref := enginetest.CatalogRef(t, e, read, "provider_hidden_rows")
	relation := read.MustRelation(ref.Address)
	_, origin := analyze.ColumnDefault(read, relation, 0)
	key := exec.DefaultOccurrence{Statement: enginetest.Statement(t, "INSERT INTO provider_hidden_rows DEFAULT VALUES")}
	owner := exec.NewAcceptedDefaultSet()
	if _, _, err := prepareAcceptedDefault(t, e, owner, key, relation, ref, relation.Attributes[0]); err != nil {
		t.Fatal(err)
	}
	if err := owner.Seal(41); err != nil {
		t.Fatal(err)
	}
	artifact, err := owner.Lookup(key, ref, relation.Attributes[0])
	if err != nil || artifact == nil {
		t.Fatal(err)
	}
	origin.Accepted, origin.Occurrence = artifact, key
	var program exec.RoutineProgram
	if err := admitAcceptedDefault(t, e, artifact, key, origin, artifact.Expression(), "provider_hidden_actor", &program); err != nil {
		t.Fatal(err)
	}
	var mixed exec.RoutineProgram
	mixed.Include(&program)
	ordinary := origin
	ordinary.Accepted = nil
	if err := admitAcceptedDefault(t, e, artifact, key, ordinary, origin.Definition.Expression, "provider_hidden_actor", &mixed); err != nil {
		t.Fatal(err)
	}
	if len(mixed.Calls) != len(program.Calls)+1 {
		t.Fatal("ordinary and bounded provider calls collapsed")
	}
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION provider_hidden_outer(a integer,b integer DEFAULT provider_hidden_arg(9)) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN a+b+20; END$$")
	readmit := func(p *exec.RoutineProgram) error {
		op, done := enginetest.RoleOperation(t, e, "provider_hidden_actor")
		defer done()
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		snapshotAcceptedPureDemand(t, p, artifact, auth.Catalog)
		return p.Readmit(core.RoutineAccessOf(auth))
	}
	if err := readmit(&program); err != nil {
		t.Fatal("exact current-provider occurrence rejected", err)
	}
	if err := readmit(&mixed); err == nil {
		t.Fatal("ordinary same-address consumer borrowed mode")
	}
	enginetest.SessionSQL(t, s, "REVOKE EXECUTE ON FUNCTION provider_hidden_arg(integer) FROM provider_hidden_actor")
	if err := readmit(&program); err == nil || sqlStateOf(err) != "42501" {
		t.Fatal("hidden default borrowed parent EXECUTE", err)
	}
	enginetest.SessionSQL(t, s, "GRANT EXECUTE ON FUNCTION provider_hidden_arg(integer) TO provider_hidden_actor")
	if err := readmit(&program); err != nil {
		t.Fatal("live independent hidden default did not recover", err)
	}
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION provider_hidden_arg(a integer) RETURNS integer RETURN a+10")
	if err := readmit(&program); err == nil {
		t.Fatal("hidden default inherited current-provider selection")
	}
	if len(e.Storage.Relations[relation.Address()].Versions) != 0 {
		t.Fatal("readmission produced hidden/body command effects")
	}
}

func TestPreparedAcceptedDefaultCurrentProviderBodyIsolation(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE FUNCTION provider_body_leaf(a integer) RETURNS integer SET search_path=public RETURN a+1",
		"CREATE FUNCTION provider_body_outer(a integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN provider_body_leaf(a)+2; END$$",
		"CREATE TABLE provider_body_rows(v integer DEFAULT provider_body_outer(1+3))",
		"CREATE ROLE provider_body_actor LOGIN",
		"GRANT USAGE ON SCHEMA public TO provider_body_actor",
		"REVOKE EXECUTE ON FUNCTION provider_body_leaf(integer),provider_body_outer(integer) FROM PUBLIC",
		"GRANT EXECUTE ON FUNCTION provider_body_leaf(integer),provider_body_outer(integer) TO provider_body_actor",
	} {
		enginetest.SessionSQL(t, s, query)
	}
	read := e.CatalogRead()
	ref := enginetest.CatalogRef(t, e, read, "provider_body_rows")
	relation := read.MustRelation(ref.Address)
	_, origin := analyze.ColumnDefault(read, relation, 0)
	key := exec.DefaultOccurrence{Statement: enginetest.Statement(t, "INSERT INTO provider_body_rows DEFAULT VALUES")}
	owner := exec.NewAcceptedDefaultSet()
	if _, _, err := prepareAcceptedDefault(t, e, owner, key, relation, ref, relation.Attributes[0]); err != nil {
		t.Fatal(err)
	}
	if err := owner.Seal(41); err != nil {
		t.Fatal(err)
	}
	artifact, err := owner.Lookup(key, ref, relation.Attributes[0])
	if err != nil || artifact == nil {
		t.Fatal("missing original body-call artifact", err)
	}
	origin.Accepted, origin.Occurrence = artifact, key
	root := artifact.Expression()
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION provider_body_outer(a integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN provider_body_leaf(a)+20; END$$")
	run := func(want int64, state string) {
		t.Helper()
		enginetest.SessionSQL(t, s, "SET ROLE provider_body_actor")
		defer enginetest.SessionSQL(t, s, "RESET ROLE")
		enginetest.PreparedFoldProtocolCommand(t, e, s, s.NewProtocolCycle(), func(scope *core.StatementScope) error {
			e.Mu.Lock()
			auth, err := scope.RoutineAuthorizationLocked()
			e.Mu.Unlock()
			if err != nil {
				return err
			}
			var program exec.RoutineProgram
			c := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Catalog: auth.Catalog, Host: core.HostOf(scope), Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, DefaultOccurrence: key, RoutineProgram: &program}}
			analyze.ApplyDefaultOrigin(origin, &c)
			if err := exec.AdmitExpression(&c, root, auth.Catalog); err != nil {
				return fmt.Errorf("outer current-provider admission failed: %w", err)
			}
			snapshotAcceptedPureDemand(t, &program, artifact, auth.Catalog)
			if err := program.Readmit(core.RoutineAccessOf(auth)); err != nil {
				return fmt.Errorf("outer current-provider readmission failed: %w", err)
			}
			value, failure := root.Eval(&c)
			if state != "" {
				if failure == nil || sqlStateOf(failure) != state {
					return fmt.Errorf("child body borrowed outer mode/EXECUTE: %v", failure)
				}
				return nil
			}
			if failure != nil {
				return fmt.Errorf("independently admitted current child body failed: %w", failure)
			}
			got, err := value.Integer()
			if err != nil || got != want {
				return fmt.Errorf("wrong current body or original prepared argument: value=%v error=%v want=%d", value, err, want)
			}
			return nil
		})
	}
	run(25, "")
	enginetest.SessionSQL(t, s, "REVOKE EXECUTE ON FUNCTION provider_body_leaf(integer) FROM provider_body_actor")
	run(0, "42501")
	enginetest.SessionSQL(t, s, "GRANT EXECUTE ON FUNCTION provider_body_leaf(integer) TO provider_body_actor")
	run(25, "")
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION provider_body_leaf(a integer) RETURNS integer SET search_path=public RETURN a+10")
	run(34, "")
	if artifact.Expression() != root || len(e.Storage.Relations[relation.Address()].Versions) != 0 {
		t.Fatal("child body admission changed accepted root or produced command effects")
	}
}
