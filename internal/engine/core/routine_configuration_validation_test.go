package core_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineConfigurationValidatorAdmission(t *testing.T) {
	for _, setting := range []struct {
		name, value      string
		declarationError string
	}{
		{name: "transaction_read_only", value: "off"},
		{name: "transaction_read_only", value: "on"},
		{name: "transaction_isolation", value: "read committed"},
		{"transaction_isolation", "repeatable read", "SET TRANSACTION ISOLATION LEVEL must be called before any query"},
		{"transaction_deferrable", "off", "SET TRANSACTION [NOT] DEFERRABLE must be called before any query"},
		{"transaction_deferrable", "on", "SET TRANSACTION [NOT] DEFERRABLE must be called before any query"},
	} {
		for _, language := range []string{"sql", "plpgsql"} {
			t.Run(setting.name+"/"+setting.value+"/"+language, func(t *testing.T) {
				e, scope := plUnitScope(t)
				// The matrix's idempotent isolation case is read committed.
				// plUnitScope supplies an explicit transaction with a different
				// isolation level; establish this fixture's intended baseline.
				scope.Owner.Isolation = session.ReadCommitted
				body := "SELECT 7"
				if language == "plpgsql" {
					body = "BEGIN RETURN 7; END"
				}
				stageDeclaration := func(source string, replay bool) (*core.CatalogStage, error) {
					t.Helper()
					raw := enginetest.Statement(t, source).(*parser.CreateFunction)
					e.Mu.Lock()
					defer e.Mu.Unlock()
					auth, err := e.AuthorizationLocked(scope.Op)
					if err != nil {
						return nil, err
					}
					stage := e.BeginCatalogStage(auth.Catalog)
					core.OperationReplay(scope.Op).SQLBodies = replay
					defer func() { core.OperationReplay(scope.Op).SQLBodies = false }()
					err = routine.StageCreateFunction(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, auth, raw, sqltype.InputEnvironment{})
					return stage, err
				}
				baseline := fmt.Sprintf("CREATE FUNCTION rv_existing() RETURNS integer LANGUAGE %s SET application_name='baseline' AS $$%s$$", language, body)
				stage, err := stageDeclaration(baseline, false)
				if err != nil {
					t.Fatal(err)
				}
				domainUnitPublish(t, e, stage)
				before := e.CatalogRead()
				namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
				key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: namespace, Name: "rv_existing", Signature: ""}
				address := before.NameAddress(key)
				original := before.Get(address).(*catalog.Procedure)
				roles := scope.Op.RoleState()
				values := map[string]string{}
				for name, value := range scope.Op.EffectiveSettings().Values {
					values[name] = value
				}
				modes := []any{scope.Owner.Isolation, scope.Owner.ReadOnly, scope.Owner.Deferrable}
				assertState := func() {
					t.Helper()
					if !reflect.DeepEqual(values, scope.Op.EffectiveSettings().Values) || len(scope.Op.EffectiveSettings().Actions) != 0 || scope.Op.RoleState() != roles || scope.Op.EffectiveUser != nil || !reflect.DeepEqual(modes, []any{scope.Owner.Isolation, scope.Owner.ReadOnly, scope.Owner.Deferrable}) {
						t.Fatal("validation mutated settings, actions, identity or transaction modes")
					}
					if e.CatalogRead().Get(address) != original || original.Configuration.Len() != 1 || original.Configuration.At(0) != "application_name=baseline" {
						t.Fatal("validation mutated published declaration or old configuration backing")
					}
				}
				assertDiagnostic := func(err error, code, message string) {
					t.Helper()
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message || diagnostic.Detail != "" || diagnostic.Hint != "" || diagnostic.Position != "" || diagnostic.InternalPosition != "" || diagnostic.InternalQuery != "" || diagnostic.Context != "" {
						t.Fatal("configuration admission diagnostic", err)
					}
				}
				for _, replace := range []bool{false, true} {
					verb, name := "CREATE", "rv_new"
					if replace {
						verb, name = "CREATE OR REPLACE", "rv_existing"
					}
					source := fmt.Sprintf("%s FUNCTION %s() RETURNS integer LANGUAGE %s SET application_name='partial' SET %s='%s' AS $$%s$$", verb, name, language, setting.name, setting.value, body)
					stage, err := stageDeclaration(source, false)
					code, message := "0A000", fmt.Sprintf(`parameter "%s" cannot be set locally in functions`, setting.name)
					if setting.declarationError != "" {
						code, message = "25001", setting.declarationError
					}
					assertDiagnostic(err, code, message)
					if e.StageRead(stage).Get(address) != original || e.CatalogRead().Get(address) != original {
						t.Fatal("failed validation replaced immutable declaration")
					}
					newKey := key
					newKey.Name = "rv_new"
					if e.StageRead(stage).NameAddress(newKey) != (ids.ObjectAddress{}) {
						t.Fatal("failed CREATE staged a declaration")
					}
					e.DiscardCatalogStage(stage)
					assertState()
					stage, err = stageDeclaration(source, true)
					if setting.declarationError != "" {
						assertDiagnostic(err, "25001", setting.declarationError)
						if e.StageRead(stage).Get(address) != original || e.StageRead(stage).NameAddress(newKey) != (ids.ObjectAddress{}) {
							t.Fatal("replay suppression bypassed declaration admission")
						}
						e.DiscardCatalogStage(stage)
						assertState()
						continue
					}
					if err != nil {
						t.Fatal("existing private replay validator suppression", err)
					}
					replayKey := key
					replayKey.Name = name
					replayed := e.StageRead(stage).Get(e.StageRead(stage).NameAddress(replayKey)).(*catalog.Procedure)
					if replayed.Configuration.Len() != 2 || replayed.Configuration.At(0) != "application_name=partial" || replayed.Configuration.At(1) != setting.name+"="+setting.value {
						t.Fatal("private replay lost ordered configuration")
					}
					e.DiscardCatalogStage(stage)
					assertState()
				}
				for _, reset := range []bool{false, true} {
					clause := fmt.Sprintf("SET application_name='altered' SET %s='%s'", setting.name, setting.value)
					if reset {
						clause = "RESET " + setting.name
					}
					raw := enginetest.Statement(t, "ALTER FUNCTION rv_existing() "+clause).(*parser.AlterRoutine)
					e.Mu.Lock()
					auth, err := e.AuthorizationLocked(scope.Op)
					if err != nil {
						e.Mu.Unlock()
						t.Fatal(err)
					}
					stage := e.BeginCatalogStage(auth.Catalog)
					err = routine.StageAlterRoutine(schemacmds.RoutineHost{DDLHost: core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, auth, raw)
					e.Mu.Unlock()
					switch {
					case reset:
						assertDiagnostic(err, "0A000", fmt.Sprintf(`parameter "%s" cannot be reset`, setting.name))
					case setting.declarationError != "":
						assertDiagnostic(err, "25001", setting.declarationError)
					default:
						if err != nil {
							t.Fatal("ALTER incorrectly ran validator SAVE admission", err)
						}
						altered := e.StageRead(stage).Get(address).(*catalog.Procedure)
						if altered == original || altered.Configuration == original.Configuration || altered.Revision() == original.Revision() || altered.Configuration.Len() != 2 || altered.Configuration.At(0) != "application_name=altered" || altered.Configuration.At(1) != setting.name+"="+setting.value {
							t.Fatal("ALTER did not retain a distinct immutable configuration revision")
						}
					}
					if err != nil && e.StageRead(stage).Get(address) != original {
						t.Fatal("failed ALTER replaced staged declaration")
					}
					e.DiscardCatalogStage(stage)
					assertState()
				}
			})
		}
	}
}

func TestRoutineConfigurationDeclarationCheckHooks(t *testing.T) {
	for _, tc := range []struct {
		name, value              string
		readOnly, subtransaction bool
		message                  string
	}{
		{name: "transaction_read_only", value: "off"},
		{name: "transaction_read_only", value: "on"},
		{"transaction_read_only", "off", true, false, "transaction read-write mode must be set before any query"},
		{"transaction_read_only", "off", true, true, "cannot set transaction read-write mode inside a read-only transaction"},
		{name: "transaction_read_only", value: "on", readOnly: true, subtransaction: true},
		{name: "transaction_isolation", value: "read committed", subtransaction: true},
		{"transaction_isolation", "repeatable read", false, true, "SET TRANSACTION ISOLATION LEVEL must be called before any query"},
		{"transaction_deferrable", "off", false, true, "SET TRANSACTION [NOT] DEFERRABLE cannot be called within a subtransaction"},
	} {
		t.Run(fmt.Sprintf("%s/%s/readonly=%t/subtransaction=%t", tc.name, tc.value, tc.readOnly, tc.subtransaction), func(t *testing.T) {
			e, scope := plUnitScope(t)
			scope.Owner.Isolation = session.ReadCommitted
			owner := scope.Owner
			initialReadOnly, initialSavepoints := owner.ReadOnly, owner.Savepoints
			defer func() { owner.ReadOnly, owner.Savepoints = initialReadOnly, initialSavepoints }()
			owner.ReadOnly = tc.readOnly
			if tc.subtransaction {
				owner.Savepoints = append(owner.Savepoints, session.SavepointMark{})
			}
			snapshot := owner.SnapshotSet
			isolation, readOnly, deferrable := owner.Isolation, owner.ReadOnly, owner.Deferrable
			savepoints := append([]session.SavepointMark(nil), owner.Savepoints...)
			store := scope.Op.EffectiveSettings()
			values := make(map[string]string, len(store.Values))
			for name, value := range store.Values {
				values[name] = value
			}
			raw := enginetest.Statement(t, fmt.Sprintf("SET %s='%s'", tc.name, tc.value)).(*parser.Set)
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(scope.Op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			name, value, remove, err := (core.DDLHost{E: e, Op: scope.Op}).ValidateRoutineSetting(auth, raw)
			e.Mu.Unlock()
			if tc.message == "" {
				if err != nil || name != tc.name || value != tc.value || remove {
					t.Fatal("nonmutating declaration check", name, value, remove, err)
				}
			} else {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "25001" || diagnostic.Message != tc.message {
					t.Fatal("declaration hook precedence", err)
				}
			}
			if owner.SnapshotSet != snapshot || owner.Isolation != isolation || owner.ReadOnly != readOnly || owner.Deferrable != deferrable || !reflect.DeepEqual(owner.Savepoints, savepoints) || !reflect.DeepEqual(store.Values, values) || len(store.Actions) != 0 {
				t.Fatal("check hook assigned transaction or settings state")
			}
		})
	}
}
