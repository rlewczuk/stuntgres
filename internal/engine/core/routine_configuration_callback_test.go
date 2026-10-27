package core_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// These are native callback-ownership tests, not another SQL acceptance suite.
// Public setting registration does not expose Normalize. The genuine injected
// definition below is the same mechanism as SettingsLocalNormalizerPublication;
// there is no PostgreSQL callback counterpart to simulate.
func TestRoutineConfigurationNormalizerPublication(t *testing.T) {
	for _, family := range []routineCallbackFamily{
		{kind: "FUNCTION", language: "sql", body: "SELECT 7"},
		{kind: "FUNCTION", language: "plpgsql", body: "BEGIN RETURN 7; END"},
		{kind: "PROCEDURE", language: "sql", body: "SELECT 7"},
		{kind: "PROCEDURE", language: "plpgsql", body: "BEGIN RETURN; END"},
	} {
		for _, operation := range []string{"create", "replace", "alter"} {
			for _, mode := range []string{"inspect", "cancel", "reset"} {
				t.Run(family.kind+"/"+family.language+"/"+operation+"/"+mode, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					var e *core.Engine
					var s *session.Session
					var unchanged func()
					calls := 0
					d := routineCallbackDefinition(t)
					d.Normalize = func(input string) (string, *sqlstate.Error, error) {
						if input != "transient" {
							return input, nil, nil
						}
						calls++
						// All declaration paths must release these publication locks.
						// Do not replace this with TryLock or a test-local timeout: a
						// retained lock is the regression the root must observe.
						e.Mu.Lock()
						e.Mu.Unlock()
						s.Settings.Mu.Lock()
						s.Settings.Mu.Unlock()
						s.Mu.Lock()
						s.Mu.Unlock()
						unchanged()
						switch mode {
						case "cancel":
							cancel()
						case "reset":
							// Reset also takes the replacement reservation: releasing
							// Engine.Mu alone is not a sufficient callback handoff.
							if err := engine.Reset(context.Background(), e); err != nil {
								return "", nil, err
							}
						}
						return "canonical", nil, nil
					}
					e = engine.NewWithSettings([]settings.Definition{d}, "", false)
					s = enginetest.StartSession(t, e, nil)
					enginetest.SessionSQL(t, s, family.declaration("CREATE", "rc_incumbent", "baseline"))
					before := e.CatalogRead()
					key := routineCallbackKey("rc_incumbent")
					address := before.NameAddress(key)
					incumbent := before.Get(address).(*catalog.Procedure)
					backing := incumbent.Configuration
					revision := incumbent.Revision()
					name := "rc_incumbent"
					if operation == "create" {
						name = "rc_candidate"
					}
					key.Name = name
					stmt := enginetest.Statement(t, family.change(operation, name, "transient"))
					op, err := s.Acquire(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer op.Done()
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					// Finish cleanup precedes operation/session cleanup even if an
					// assertion aborts while the declaration is being normalized.
					defer func() { scope.Finish(nil) }()
					unchanged = routineCallbackUnchanged(t, scope)
					result, executeErr := scope.Execute(stmt, 0, nil)
					if result.Rows != nil {
						defer result.Rows.Close()
					}
					unchanged()
					if calls != 1 {
						t.Fatalf("declaration invoked the injected normalizer %d times, want exactly once", calls)
					}
					if mode == "cancel" || mode == "reset" {
						if scope.Catalog != nil && (len(scope.Catalog.Descriptors) != 0 || len(scope.Catalog.Names) != 0) {
							t.Fatal("interrupted callback left a staged routine publication")
						}
					}
					finishErr := scope.Finish(executeErr)
					switch mode {
					case "inspect":
						if executeErr != nil || finishErr != nil {
							t.Fatal(executeErr, finishErr)
						}
						current := e.CatalogRead()
						published := current.Get(current.NameAddress(key)).(*catalog.Procedure)
						if published.Configuration.Len() != 2 || published.Configuration.At(0) != "application_name=partial" || published.Configuration.At(1) != "demo.routine_callback=canonical" {
							t.Fatal("declaration did not retain ordered canonical configuration")
						}
						if operation != "create" && (published == incumbent || published.Configuration == backing || published.Revision() == revision || published.Address() != address) {
							t.Fatal("successful mutation did not publish a distinct immutable revision")
						}
					case "cancel":
						if !errors.Is(executeErr, context.Canceled) || !errors.Is(finishErr, context.Canceled) {
							t.Fatal(executeErr, finishErr)
						}
						current := e.CatalogRead()
						if current.Epoch() != before.Epoch() || current.Get(address) != incumbent || (operation == "create" && current.NameAddress(key) != (ids.ObjectAddress{})) {
							t.Fatal("canceled normalization published a declaration")
						}
					case "reset":
						// Readmission may reject during Execute; regardless, Finish
						// must retain the canonical old-epoch publication fence.
						if (executeErr != nil && !errors.Is(executeErr, ids.ErrCatalogReplaced)) || !errors.Is(finishErr, ids.ErrCatalogReplaced) {
							t.Fatal(executeErr, finishErr)
						}
						current := e.CatalogRead()
						if current.Epoch() == before.Epoch() || current.NameAddress(key) != (ids.ObjectAddress{}) || current.NameAddress(routineCallbackKey("rc_incumbent")) != (ids.ObjectAddress{}) {
							t.Fatal("old declaration escaped Reset's catalog replacement")
						}
					}
					if before.Get(address) != incumbent || incumbent.Configuration != backing || incumbent.Revision() != revision || backing.Len() != 2 || backing.At(0) != "application_name=partial" || backing.At(1) != "demo.routine_callback=baseline" {
						t.Fatal("normalization or publication mutated retained incumbent backing")
					}
					localOwnerValue(t, s, d.Name, "boot")
				})
			}
		}
	}
}

func TestRoutineConfigurationNormalizerFromCurrent(t *testing.T) {
	for _, operation := range []string{"create", "replace", "alter"} {
		t.Run(operation, func(t *testing.T) {
			var e *core.Engine
			var s *session.Session
			var unchanged func()
			reached := false
			d := routineCallbackDefinition(t)
			describing := false
			d.Normalize = func(input string) (string, *sqlstate.Error, error) {
				if describing {
					t.Fatal("Describe invoked declaration normalization")
				}
				if unchanged == nil {
					return input, nil, nil
				}
				e.Mu.Lock()
				e.Mu.Unlock()
				s.Settings.Mu.Lock()
				s.Settings.Mu.Unlock()
				s.Mu.Lock()
				s.Mu.Unlock()
				unchanged()
				if input != "capture-b" {
					t.Fatalf("FROM CURRENT captured %q, want execution-time capture-b", input)
				}
				reached = true
				return "canonical-b", nil, nil
			}
			e = engine.NewWithSettings([]settings.Definition{d}, "", false)
			s = enginetest.StartSession(t, e, nil)
			family := routineCallbackFamily{kind: "FUNCTION", language: "sql", body: "SELECT 7"}
			if operation != "create" {
				enginetest.SessionSQL(t, s, family.declaration("CREATE", "rc_capture", "baseline"))
			}
			enginetest.SessionSQL(t, s, "SET demo.routine_callback='capture-a'")
			stmt := enginetest.Statement(t, strings.Replace(family.change(operation, "rc_capture", "unused"), "SET demo.routine_callback='unused'", "SET demo.routine_callback FROM CURRENT", 1))
			op := enginetest.TransactionOperation(t, s)
			describing = true
			if _, err := e.Describe(op, stmt); err != nil {
				t.Fatal(err)
			}
			describing = false
			op.Done()
			enginetest.SessionSQL(t, s, "SET demo.routine_callback='capture-b'")
			op = enginetest.TransactionOperation(t, s)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { scope.Finish(nil) }()
			unchanged = routineCallbackUnchanged(t, scope)
			result, executeErr := scope.Execute(stmt, 0, nil)
			if result.Rows != nil {
				defer result.Rows.Close()
			}
			unchanged()
			finishErr := scope.Finish(executeErr)
			if executeErr != nil || finishErr != nil || !reached {
				t.Fatal("FROM CURRENT execution", executeErr, finishErr, reached)
			}
			current := e.CatalogRead()
			published := current.Get(current.NameAddress(routineCallbackKey("rc_capture"))).(*catalog.Procedure)
			if published.Configuration.Len() != 2 || published.Configuration.At(1) != d.Name+"=canonical-b" {
				t.Fatal("FROM CURRENT did not publish normalized execution-time value")
			}
			localOwnerValue(t, s, d.Name, "capture-b")
		})
	}
}

// The callback changes a real published parameter grant from another session.
// The suspended declaration must use live authority, not its captured grant,
// and must never reconstruct/retry the callback-bearing attribute fold.
func TestRoutineConfigurationNormalizerLiveAuthority(t *testing.T) {
	for _, operation := range []string{"create", "replace", "alter"} {
		t.Run(operation, func(t *testing.T) {
			var admin *session.Session
			calls := 0
			// Native injection deliberately replaces an eligible privileged
			// builtin name with the validated string callback fixture; public
			// registration neither replaces builtins nor accepts callbacks.
			d := routineCallbackDefinition(t)
			d.Name, d.Access = "log_duration", settings.Privileged
			d.Normalize = func(input string) (string, *sqlstate.Error, error) {
				if input == "transient" {
					calls++
					enginetest.SessionSQL(t, admin, "REVOKE SET ON PARAMETER log_duration FROM rc_actor")
					return "canonical", nil, nil
				}
				return input, nil, nil
			}
			e := engine.NewWithSettings([]settings.Definition{d}, "", false)
			admin = enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, admin, "CREATE ROLE rc_actor")
			enginetest.SessionSQL(t, admin, "CREATE SCHEMA rc_auth AUTHORIZATION rc_actor")
			enginetest.SessionSQL(t, admin, "GRANT SET ON PARAMETER log_duration TO rc_actor")
			actor := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, actor, "SET ROLE rc_actor")
			family := routineCallbackFamily{kind: "FUNCTION", language: "sql", body: "SELECT 7"}
			declaration := func(operation, value string) string {
				return strings.Replace(family.change(operation, "rc_auth.rc_target", value), "demo.routine_callback", d.Name, 1)
			}
			if operation != "create" {
				enginetest.SessionSQL(t, actor, declaration("create", "baseline"))
			}
			before := e.CatalogRead()
			namespace := before.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "rc_auth"})
			key := routineCallbackKey("rc_target")
			key.Namespace = namespace
			address := before.NameAddress(key)
			incumbent := before.Get(address)
			op := enginetest.TransactionOperation(t, actor)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { scope.Finish(nil) }()
			result, executeErr := scope.Execute(enginetest.Statement(t, declaration(operation, "transient")), 0, nil)
			if result.Rows != nil {
				defer result.Rows.Close()
			}
			finishErr := scope.Finish(executeErr)
			for _, err := range []error{executeErr, finishErr} {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != `permission denied to set parameter "log_duration"` {
					t.Fatalf("stale parameter grant admitted: %v", err)
				}
			}
			if calls != 1 {
				t.Fatalf("normalizer calls = %d, want exactly once", calls)
			}
			current := e.CatalogRead()
			if current.NameAddress(key) != address || current.Get(address) != incumbent {
				t.Fatal("revoked authority published a candidate or changed the incumbent")
			}
			if scope.Catalog != nil && (len(scope.Catalog.Descriptors) != 0 || len(scope.Catalog.Names) != 0) {
				t.Fatal("revoked authority left a staged routine publication")
			}
			localOwnerValue(t, actor, d.Name, "boot")
		})
	}
}

type routineCallbackFamily struct {
	kind, language, body string
}

func (f routineCallbackFamily) declaration(verb, name, value string) string {
	result, attributes := "", "SECURITY DEFINER"
	if f.kind == "FUNCTION" {
		result = " RETURNS integer"
		attributes += " STABLE STRICT PARALLEL RESTRICTED COST 7"
	}
	return fmt.Sprintf("%s %s %s()%s LANGUAGE %s %s SET application_name='partial' SET demo.routine_callback='%s' AS $$%s$$", verb, f.kind, name, result, f.language, attributes, value, f.body)
}

func (f routineCallbackFamily) change(operation, name, value string) string {
	switch operation {
	case "create":
		return f.declaration("CREATE", name, value)
	case "replace":
		return f.declaration("CREATE OR REPLACE", name, value)
	default:
		return fmt.Sprintf("ALTER %s %s() SECURITY INVOKER SET application_name='partial' SET demo.routine_callback='%s'", f.kind, name, value)
	}
}

func routineCallbackDefinition(t *testing.T) settings.Definition {
	t.Helper()
	d, err := settings.Validate(settings.Definition{Name: "demo.routine_callback", Type: settings.String, BootDefault: "boot"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func routineCallbackKey(name string) ids.NameKey {
	return ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: name, Signature: ""}
}

// Capture the actual store and transaction owner, not a detached clone. Checking
// inside Normalize also catches install-then-undo implementations that would
// otherwise leave an apparently unchanged session after Finish.
func routineCallbackUnchanged(t *testing.T, scope *core.StatementScope) func() {
	t.Helper()
	store := scope.Op.EffectiveSettings()
	values, typed := maps.Clone(store.Values), maps.Clone(store.Typed)
	defaults, defaultTyped := maps.Clone(store.Defaults), maps.Clone(store.DefaultTyped)
	actions, placeholders := maps.Clone(store.Actions), maps.Clone(store.Placeholders)
	roleAction, nestLevel := store.RoleAction, store.NestLevel
	roles, effective := scope.Op.RoleState(), scope.Op.EffectiveUser
	owner := scope.Owner
	isolation, readOnly, deferrable := owner.Isolation, owner.ReadOnly, owner.Deferrable
	return func() {
		t.Helper()
		if scope.Op.EffectiveSettings() != store || !reflect.DeepEqual(store.Values, values) || !reflect.DeepEqual(store.Typed, typed) || !reflect.DeepEqual(store.Defaults, defaults) || !reflect.DeepEqual(store.DefaultTyped, defaultTyped) || !reflect.DeepEqual(store.Actions, actions) || !reflect.DeepEqual(store.Placeholders, placeholders) || store.RoleAction != roleAction || store.NestLevel != nestLevel {
			t.Fatal("declaration normalization installed settings or SAVE journal state")
		}
		if scope.Op.RoleState() != roles || scope.Op.EffectiveUser != effective || scope.Owner != owner || owner.Isolation != isolation || owner.ReadOnly != readOnly || owner.Deferrable != deferrable {
			t.Fatal("declaration normalization changed identity or transaction modes")
		}
	}
}
