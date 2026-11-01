package views_test

import (
	"context"
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
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/view"
)

func TestPreparedAcceptedDefaultOwnership(t *testing.T) {
	for _, language := range []string{"sql", "pl"} {
		t.Run(language, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE FUNCTION accepted_default() RETURNS integer " + acceptedDefaultDeclaration(language, "5"), "CREATE TABLE accepted_default_rows(v integer DEFAULT accepted_default())", "BEGIN"} {
				enginetest.SessionSQL(t, s, q)
			}
			cycle := s.NewProtocolCycle()
			command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
			command(func(scope *core.StatementScope) error {
				_, err := scope.ParseProtocol("default", "INSERT INTO accepted_default_rows DEFAULT VALUES RETURNING v", nil)
				return err
			})
			bind := func(name string) {
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal(name, "default", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
			}
			bind("old")
			registry := core.PayloadOf(s).PreparedSQL
			old := registry.Binding("old")
			if old == nil || old.Folds == nil || old.Resource != nil && old.Resource.Root != nil {
				t.Fatal("command retained a graph or omitted accepted ownership")
			}
			// Inspect the real private accepted owner, not a parallel observation token.
			owner := reflect.ValueOf(old).Elem().FieldByName("defaults")
			if owner.IsNil() {
				t.Fatal("Bind omitted consumed DEFAULT artifact")
			}
			set := owner.Elem()
			if !set.FieldByName("sealed").Bool() || set.FieldByName("retired").Bool() || set.FieldByName("entries").Len() != 1 {
				t.Fatal("incomplete accepted DEFAULT set")
			}
			artifact := set.FieldByName("entries").Index(0).Elem()
			if artifact.FieldByName("calls").Len() != 1 || artifact.FieldByName("root").IsNil() {
				t.Fatal("DEFAULT did not retain its typed call closure")
			}
			ownerPointer, rootPointer := owner.Pointer(), artifact.FieldByName("root").Pointer()
			dependencyPointer := artifact.FieldByName("dependencies").Pointer()
			group := old.Folds
			accepted := *group
			entries := slices.Clone(group.Entries)
			analysis := old.Entry.Description.Analysis
			dependencies := slices.Clone(analysis.Dependencies)
			enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION accepted_default() RETURNS integer "+acceptedDefaultDeclaration(language, "9"))
			bind("new")
			fresh := registry.Binding("new")
			if fresh.Entry.Description.Analysis == analysis || reflect.ValueOf(fresh).Elem().FieldByName("defaults").Pointer() == ownerPointer {
				t.Fatal("new Bind adopted old accepted ownership")
			}
			command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("default") })
			command(func(scope *core.StatementScope) error {
				_, err := scope.ExecuteProtocolPortal("old", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
				return err
			})
			if owner.Pointer() != ownerPointer || artifact.FieldByName("root").Pointer() != rootPointer || artifact.FieldByName("dependencies").Pointer() != dependencyPointer || !reflect.DeepEqual(accepted.Entries, entries) || old.Entry.Description.Analysis != analysis || !slices.Equal(analysis.Dependencies, dependencies) || group.Generation != accepted.Generation {
				t.Fatal("Execute mutated original recipe/source/dependency/fold backing")
			}
			enginetest.SessionSQL(t, s, "ROLLBACK")
			if !set.FieldByName("retired").Bool() {
				t.Fatal("transaction end left accepted DEFAULT live")
			}
		})
	}
}

func acceptedDefaultDeclaration(language, value string) string {
	if language == "pl" {
		return "LANGUAGE plpgsql AS $$BEGIN RETURN " + value + "; END$$"
	}
	return "RETURN " + value
}

func acceptedDefaultFixture(t *testing.T, language string) (*core.Engine, *session.Session, *exec.AcceptedDefaultSet, *exec.AcceptedDefault, exec.DefaultOccurrence, analyze.DefaultOrigin) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE FUNCTION accepted_lineage() RETURNS integer " + acceptedDefaultDeclaration(language, "5"), "CREATE TABLE accepted_lineage_rows(v integer DEFAULT accepted_lineage())"} {
		enginetest.SessionSQL(t, s, q)
	}
	read := e.CatalogRead()
	ref := enginetest.CatalogRef(t, e, read, "accepted_lineage_rows")
	relation := read.MustRelation(ref.Address)
	_, origin := analyze.ColumnDefault(read, relation, 0)
	key := exec.DefaultOccurrence{Statement: enginetest.Statement(t, "INSERT INTO accepted_lineage_rows DEFAULT VALUES")}
	owner := exec.NewAcceptedDefaultSet()
	if _, _, err := prepareAcceptedDefault(t, e, owner, key, relation, ref, relation.Attributes[0]); err != nil {
		t.Fatal(err)
	}
	if err := owner.Seal(41); err != nil {
		t.Fatal(err)
	}
	artifact, err := owner.Lookup(key, ref, relation.Attributes[0])
	if err != nil || artifact == nil {
		t.Fatal("missing owned source", err)
	}
	origin.Accepted, origin.Occurrence = artifact, key
	return e, s, owner, artifact, key, origin
}

func prepareAcceptedDefault(t *testing.T, e *core.Engine, owner *exec.AcceptedDefaultSet, key exec.DefaultOccurrence, relation *catalog.Relation, ref ids.ObjectRef, attribute int32) (expr.Expr, bool, error) {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		return nil, false, err
	}
	c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: auth.Catalog}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, DefaultOccurrence: key}}
	origin := analyze.DefaultOrigin{Consumer: relation, Definition: relation.ColumnMeta.Defaults[attribute], Fold: exec.FoldOrigin{Owner: ref, Attribute: attribute, Definition: relation.ColumnMeta.Defaults[attribute].OID}}
	analyze.ApplyDefaultOrigin(origin, &c)
	return owner.Prepare(&c, key, relation, ref, attribute, exec.DefaultPreparationPolicy{Update: key.Kind == 1, PlanConstants: true})
}

func admitAcceptedDefault(t *testing.T, e *core.Engine, artifact *exec.AcceptedDefault, key exec.DefaultOccurrence, origin analyze.DefaultOrigin, node expr.Expr, role string, program *exec.RoutineProgram) error {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, role)
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: auth.Catalog}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, DefaultOccurrence: key, RoutineProgram: program}}
	analyze.ApplyDefaultOrigin(origin, &c)
	return view.ValidateExpressionJoinExecution(&c, node, auth.Catalog)
}

func TestPreparedAcceptedDefaultLineage(t *testing.T) {
	for _, language := range []string{"sql", "pl"} {
		t.Run(language, func(t *testing.T) {
			e, s, owner, artifact, key, origin := acceptedDefaultFixture(t, language)
			source, ref, attribute, definition := artifact.Source()
			dependencies := slices.Clone(artifact.Dependencies())
			root := artifact.Expression()
			var program exec.RoutineProgram
			if err := admitAcceptedDefault(t, e, artifact, key, origin, root, "postgres", &program); err != nil {
				t.Fatal(err)
			}
			var mixed exec.RoutineProgram
			mixed.Include(&program)
			ordinary := origin
			ordinary.Accepted = nil
			if err := admitAcceptedDefault(t, e, artifact, key, ordinary, definition.Expression, "postgres", &mixed); err != nil {
				t.Fatal(err)
			}
			if len(mixed.Calls) != 2 || mixed.Calls[0].Routine != mixed.Calls[1].Routine {
				t.Fatal("bounded and ordinary same-address calls collapsed")
			}
			for _, q := range []string{"CREATE OR REPLACE FUNCTION accepted_lineage() RETURNS integer " + acceptedDefaultDeclaration(language, "9"), "CREATE OR REPLACE FUNCTION accepted_lineage() RETURNS integer " + acceptedDefaultDeclaration(language, "11")} {
				enginetest.SessionSQL(t, s, q)
			}
			if err := admitAcceptedDefault(t, e, artifact, key, origin, root, "postgres", &program); err != nil {
				t.Fatal("paired repeated publication", err)
			}
			for _, kind := range []string{"copied-artifact", "copied-root", "copied-call", "transplanted-call", "raw-node", "wrong-command", "wrong-expansion", "wrong-attribute", "wrong-oid", "wrong-source", "foreign-owner"} {
				t.Run(kind, func(t *testing.T) {
					node, selected, occurrence := root, origin, key
					switch kind {
					case "copied-artifact":
						copied := *artifact
						selected.Accepted = &copied
						if copied.Valid() {
							t.Fatal("copied artifact valid")
						}
					case "copied-root", "copied-call":
						original := root
						if kind == "copied-call" {
							original = root.(expr.Composite).Children()[0]
						}
						value := reflect.ValueOf(original)
						copy := reflect.New(value.Elem().Type())
						copy.Elem().Set(value.Elem())
						node = copy.Interface().(expr.Expr)
					case "transplanted-call":
						node = root.(expr.Composite).Children()[0]
					case "raw-node":
						node = definition.Expression
					case "wrong-command":
						occurrence.Statement = enginetest.Statement(t, "INSERT INTO accepted_lineage_rows DEFAULT VALUES")
					case "wrong-expansion":
						occurrence.Expansion++
					case "wrong-attribute":
						selected.Fold.Attribute++
					case "wrong-oid":
						selected.Fold.Definition++
					case "wrong-source":
						copied := *source
						selected.Consumer = &copied
					case "foreign-owner":
						selected.Fold.Owner = engine.New().Catalog.Domain.Ref(ref.Epoch, ref.Address)
					}
					snapshotAcceptedPureDemand(t, &program, artifact, e.CatalogRead())
					if err := admitAcceptedDefault(t, e, artifact, occurrence, selected, node, "postgres", nil); err == nil {
						t.Fatal("invalid accepted occurrence admitted", kind)
					}
				})
			}
			if len(program.Calls) != 1 {
				t.Fatal("same exact accepted call lost dedup", len(program.Calls))
			}
			op, done := enginetest.RoleOperation(t, e, "postgres")
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			done()
			if err != nil {
				t.Fatal(err)
			}
			if err := program.Readmit(core.RoutineAccessOf(auth)); err != nil {
				t.Fatal(err)
			}
			if err := mixed.Readmit(core.RoutineAccessOf(auth)); err == nil {
				t.Fatal("bounded evidence rehabilitated ordinary same-address call")
			}
			var included exec.RoutineProgram
			included.Include(&program)
			if len(included.Calls) != len(program.Calls) || included.Readmit(core.RoutineAccessOf(auth)) != nil {
				t.Fatal("Include lost exact occurrence evidence")
			}
			var phase exec.RoutineProgram
			if err := phase.InitializePhase(&program, core.RoutineAccessOf(auth)); err == nil {
				t.Fatal("different phase inherited DEFAULT selection")
			}
			if old, oldRef, oldAttribute, oldDefinition := artifact.Source(); old != source || oldRef != ref || oldAttribute != attribute || !reflect.DeepEqual(oldDefinition, definition) || !slices.Equal(artifact.Dependencies(), dependencies) {
				t.Fatal("lineage admission mutated original source")
			}
			owner.Retire()
			if artifact.Valid() || program.Readmit(core.RoutineAccessOf(auth)) == nil {
				t.Fatal("retired owner regained admission")
			}
			if _, _, err := (&exec.AcceptedDefaultInvocation{}).Validate(core.RoutineAccessOf(auth), nil, nil); err == nil {
				t.Fatal("zero invocation acquired authority")
			}
			if _, err := owner.Lookup(key, ref, attribute); err == nil {
				t.Fatal("retired owner lookup")
			}
		})
	}
}

func TestPreparedAcceptedDefaultDemandAndAuthority(t *testing.T) {
	for _, language := range []string{"sql", "pl"} {
		t.Run(language, func(t *testing.T) {
			e, s, owner, artifact, key, origin := acceptedDefaultFixture(t, language)
			for _, q := range []string{"CREATE ROLE accepted_actor LOGIN", "GRANT USAGE ON SCHEMA public TO accepted_actor", "GRANT EXECUTE ON FUNCTION accepted_lineage() TO accepted_actor", "REVOKE EXECUTE ON FUNCTION accepted_lineage() FROM PUBLIC", "CREATE OR REPLACE FUNCTION accepted_lineage() RETURNS integer " + acceptedDefaultDeclaration(language, "9")} {
				enginetest.SessionSQL(t, s, q)
			}
			var program exec.RoutineProgram
			if err := admitAcceptedDefault(t, e, artifact, key, origin, artifact.Expression(), "accepted_actor", &program); err != nil {
				t.Fatal(err)
			}
			enginetest.SessionSQL(t, s, "REVOKE EXECUTE ON FUNCTION accepted_lineage() FROM accepted_actor")
			op, done := enginetest.RoleOperation(t, e, "accepted_actor")
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			done()
			if err != nil {
				t.Fatal(err)
			}
			if err := program.Readmit(core.RoutineAccessOf(auth)); err == nil || sqlStateOf(err) != "42501" {
				t.Fatal("readmission reused revoked authority", err)
			}
			enginetest.SessionSQL(t, s, "GRANT EXECUTE ON FUNCTION accepted_lineage() TO accepted_actor")
			if err := admitAcceptedDefault(t, e, artifact, key, origin, artifact.Expression(), "accepted_actor", nil); err != nil {
				t.Fatal("live regrant", err)
			}
			owner.Retire()
			if err := admitAcceptedDefault(t, e, artifact, key, origin, artifact.Expression(), "accepted_actor", nil); err == nil {
				t.Fatal("regrant resurrected terminal owner")
			}
			// No effects are produced by structural admission or by failed authority.
			relation, _, _, _ := artifact.Source()
			if len(e.Storage.Relations[relation.Address()].Versions) != 0 {
				t.Fatal("admission executed a DEFAULT")
			}
		})
	}
}

func sqlStateOf(err error) string {
	if diagnostic, ok := err.(interface{ SQLState() string }); ok {
		return diagnostic.SQLState()
	}
	return ""
}
