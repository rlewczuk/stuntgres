package views_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

func TestPreparedAcceptedDefaultFences(t *testing.T) {
	for _, language := range []string{"sql", "pl"} {
		for _, kind := range []string{"copied-owner", "dependency-purpose", "dependency-revision", "dependency-ref", "copied-current-relation", "wrong-current-revision", "wrong-current-body", "wrong-result", "language-provider", "type-codec", "old-epoch", "missing-current", "unrelated-ddl", "recreated", "rollback-branch"} {
			t.Run(language+"/"+kind, func(t *testing.T) {
				e, s, owner, artifact, key, origin := acceptedDefaultFixture(t, language)
				if kind == "rollback-branch" {
					enginetest.SessionSQL(t, s, "BEGIN")
					enginetest.SessionSQL(t, s, "SAVEPOINT default_branch")
					enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION accepted_lineage() RETURNS integer "+acceptedDefaultDeclaration(language, "7"))
					// A recipe born on the discarded branch has an exact distinct source.
					op, _, err := s.AcquireCommand(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					e.Mu.Lock()
					auth, err := e.AuthorizationLocked(op)
					e.Mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					ref := enginetest.CatalogRef(t, e, auth.Catalog, "accepted_lineage_rows")
					relation := auth.Catalog.MustRelation(ref.Address)
					_, branchOrigin := analyze.ColumnDefault(auth.Catalog, relation, 0)
					branch := exec.NewAcceptedDefaultSet()
					c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: auth.Catalog}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, DefaultOccurrence: key}}
					analyze.ApplyDefaultOrigin(branchOrigin, &c)
					_, _, prepareErr := branch.Prepare(&c, key, relation, ref, relation.Attributes[0], exec.DefaultPreparationPolicy{})
					op.Done()
					if err := prepareErr; err != nil {
						t.Fatal(err)
					}
					if err := branch.Seal(42); err != nil {
						t.Fatal(err)
					}
					artifact, err = branch.Lookup(key, ref, relation.Attributes[0])
					if err != nil {
						t.Fatal(err)
					}
					origin = branchOrigin
					origin.Accepted = artifact
					enginetest.SessionSQL(t, s, "ROLLBACK TO default_branch")
					enginetest.SessionSQL(t, s, "ROLLBACK")
				} else {
					enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION accepted_lineage() RETURNS integer "+acceptedDefaultDeclaration(language, "9"))
				}
				if kind == "unrelated-ddl" {
					enginetest.SessionSQL(t, s, "ALTER TABLE accepted_lineage_rows ADD COLUMN extra integer")
				}
				if kind == "recreated" {
					enginetest.SessionSQL(t, s, "DROP TABLE accepted_lineage_rows")
					enginetest.SessionSQL(t, s, "CREATE TABLE accepted_lineage_rows(v integer DEFAULT accepted_lineage())")
				}
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				state := catalog.CloneState(auth.Catalog.State())
				original, ref, _, definition := artifact.Source()
				routine := definition.Expression.(interface{ CallRoutineReference() expr.RoutineReference }).CallRoutineReference().(*catalog.Procedure)
				current, _ := auth.Catalog.Relation(ref.Address)
				switch kind {
				case "copied-owner":
					copied := *owner
					if copied.Live() {
						t.Fatal("copied set live")
					}
					if _, err := copied.Lookup(key, ref, original.Attributes[0]); err == nil {
						t.Fatal("copied owner lookup")
					}
					return
				case "dependency-purpose":
					artifact.Dependencies()[0].Purpose = catalog.OperandDependency
				case "dependency-revision":
					artifact.Dependencies()[0].Revision++
				case "dependency-ref":
					artifact.Dependencies()[0].Ref = ids.ObjectRef{}
				case "copied-current-relation":
					copied := *current
					state.SetDescriptor(copied.Address(), &copied)
				case "wrong-current-revision":
					copied := *current
					copied.ObjectRevision++
					state.SetDescriptor(copied.Address(), &copied)
				case "wrong-current-body", "wrong-result", "language-provider":
					live, _ := auth.Catalog.Procedure(routine.Address())
					copied := *live
					switch kind {
					case "wrong-current-body":
						copied.SQL, copied.SQLBody = routine.SQL, routine.SQLBody
					case "wrong-result":
						copied.Result = 25
					case "language-provider":
						copied.Language = ids.ObjectAddress{ClassID: ids.LanguageClassID, ObjectID: 999999}
					}
					state.SetDescriptor(copied.Address(), &copied)
				case "type-codec":
					address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23}
					typ, _ := auth.Catalog.Type(address)
					text, _ := auth.Catalog.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 25})
					copied := *typ
					copied.SQLType = text.SQLType
					state.SetDescriptor(address, &copied)
				case "old-epoch":
					state = catalog.NewState(auth.Catalog.Epoch() + 1)
				case "missing-current":
					state.RemoveDescriptor(ref.Address)
				}
				auth.Recapture(auth.Catalog.WithState(state))
				c := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: auth.Catalog}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, DefaultOccurrence: key}}
				analyze.ApplyDefaultOrigin(origin, &c)
				if _, _, err := exec.PreflightCoercionsWithPolicy(&c, artifact.Expression(), true); err == nil {
					t.Fatal("invalid accepted DEFAULT source admitted", kind)
				}
			})
		}
	}
}

func TestPreparedAcceptedDefaultTerminalOwnership(t *testing.T) {
	for _, language := range []string{"sql", "pl"} {
		for _, kind := range []string{"close", "savepoint", "transaction", "generation", "native"} {
			t.Run(language+"/"+kind, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				declaration := "RETURN 5"
				if language == "pl" {
					declaration = "LANGUAGE plpgsql AS $$BEGIN RETURN 5; END$$"
				}
				for _, q := range []string{"CREATE FUNCTION terminal_default() RETURNS integer " + declaration, "CREATE TABLE terminal_default_rows(v integer DEFAULT terminal_default())", "BEGIN", "SAVEPOINT terminal_mark"} {
					enginetest.SessionSQL(t, s, q)
				}
				cycle := s.NewProtocolCycle()
				command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
				command(func(scope *core.StatementScope) error {
					_, err := scope.ParseProtocol("terminal", "INSERT INTO terminal_default_rows DEFAULT VALUES", nil)
					return err
				})
				command(func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal("terminal", "terminal", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
				binding := core.PayloadOf(s).PreparedSQL.Binding("terminal")
				retained := reflect.ValueOf(binding).Elem().FieldByName("defaults").Elem()
				switch kind {
				case "close":
					command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("terminal") })
				case "savepoint":
					enginetest.SessionSQL(t, s, "ROLLBACK TO terminal_mark")
				case "transaction":
					enginetest.SessionSQL(t, s, "ROLLBACK")
				case "generation", "native":
					if kind == "native" {
						if err := engine.Reset(context.Background(), e); err != nil {
							t.Fatal(err)
						}
					} else {
						binding.Folds.Generation++
					}
					err := enginetest.PreparedFoldProtocolTry(t, e, s, cycle, false, func(scope *core.StatementScope) error {
						_, err := scope.ExecuteProtocolPortal("terminal", 0, nil, nil, nil, core.CopyProtocolHooks{})
						return err
					})
					if err == nil {
						t.Fatal("invalid native/generation owner executed DEFAULT", kind)
					}
					if kind == "native" && !errors.Is(err, ids.ErrCatalogReplaced) {
						t.Fatal("native replacement lost its exact owner fence", err)
					}
					enginetest.SessionSQL(t, s, "ROLLBACK")
				}
				if !binding.Retired || !retained.FieldByName("retired").Bool() {
					t.Fatal("terminal binding left DEFAULT admission live", kind)
				}
				if retained.FieldByName("entries").Len() != 1 {
					t.Fatal("terminal cleanup erased immutable accepted evidence")
				}
				if kind != "transaction" && kind != "generation" && kind != "native" {
					enginetest.SessionSQL(t, s, "ROLLBACK")
				}
			})
		}
	}
}

func TestPreparedAcceptedDefaultFailedBind(t *testing.T) {
	for _, language := range []string{"sql", "pl"} {
		for _, cancelled := range []bool{false, true} {
			t.Run(language+"/"+map[bool]string{false: "late_cte", true: "cancel"}[cancelled], func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE FUNCTION failed_default() RETURNS integer " + acceptedDefaultDeclaration(language, "5"), "CREATE TABLE failed_default_rows(v integer DEFAULT failed_default())", "BEGIN"} {
					enginetest.SessionSQL(t, s, q)
				}
				cycle := s.NewProtocolCycle()
				query := "INSERT INTO failed_default_rows DEFAULT VALUES RETURNING v"
				if !cancelled {
					query = "WITH first AS (INSERT INTO failed_default_rows DEFAULT VALUES RETURNING v), second AS (UPDATE failed_default_rows SET v=(1/0)::bigint RETURNING v) SELECT v FROM first"
				}
				enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, func(scope *core.StatementScope) error {
					_, err := scope.ParseProtocol("failed_default", query, nil)
					return err
				})
				err := enginetest.PreparedFoldProtocolTry(t, e, s, cycle, cancelled, func(scope *core.StatementScope) error {
					_, err := scope.BindProtocolPortal("failed_default", "failed_default", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
					return err
				})
				if err == nil || core.PayloadOf(s).PreparedSQL.Binding("failed_default") != nil {
					t.Fatal("failed Bind installed accepted DEFAULT ownership", err)
				}
				ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "failed_default_rows")
				if len(e.Storage.Relations[ref.Address].Versions) != 0 {
					t.Fatal("Bind evaluated command effects")
				}
				enginetest.SessionSQL(t, s, "ROLLBACK")
			})
		}
	}
}
