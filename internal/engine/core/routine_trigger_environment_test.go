package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineTriggerAttributeEnvironment(t *testing.T) {
	routineTriggerAttributeEnvironment(t, false)
}

func TestRoutineEventTriggerAttributeEnvironment(t *testing.T) {
	routineTriggerAttributeEnvironment(t, true)
}

// The callback is a witness inside the actual PL body, not an alternative
// trigger dispatcher. It inspects the same scope, operation and SAVE journal
// used by the real DML/utility command, and can cancel that operation there.
func routineTriggerAttributeEnvironment(t *testing.T, event bool) {
	t.Helper()
	for _, outcome := range []string{"success", "error", "cancel", "partial_entry", "restricted"} {
		t.Run(outcome, func(t *testing.T) {
			definition, err := settings.Validate(settings.Definition{Name: "demo.trigger_entry", Type: settings.String, BootDefault: "outside"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var scope *core.StatementScope
			var e *core.Engine
			calls := 0
			definition.Normalize = func(input string) (string, *sqlstate.Error, error) {
				if input != "body" {
					return input, nil, nil
				}
				calls++
				op := scope.Op
				owner := e.Roles().LookupRef("rt_owner")
				e.Mu.Lock()
				auth, failure := scope.RoutineAuthorizationLocked()
				e.Mu.Unlock()
				if failure != nil || auth.State.CurrentUser != owner || scope.Identity.CurrentPrincipal != owner || op.EffectiveUser == nil || op.EffectiveUser.Owner != owner || scope.ForeignActionAuth != nil {
					t.Fatal("real trigger body lost live definer/RI precedence", auth.State, scope.Identity, failure)
				}
				restricted := outcome == "restricted"
				if (op.Restricted != nil) != restricted || (op.EffectiveSettings() != op.Session.Settings) != restricted || core.ScopeActiveSQLRoutineDepth(scope) != 1 || op.EffectiveSettings().NestLevel <= 1 {
					t.Fatal("entry replaced operation, restricted semantics, depth or SAVE owner")
				}
				app, _ := op.Setting("application_name")
				if app != "trigger-inside" || op.LockTimeout != 37*time.Millisecond || scope.Input.DateStyle != scope.Output.DateStyle {
					t.Fatal("body presentation/settings were not installed together", app, op.LockTimeout)
				}
				if outcome == "cancel" {
					cancel()
				}
				return input, nil, nil
			}
			e = engine.NewWithSettings([]settings.Definition{definition}, "", false)
			sess := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE ROLE rt_owner", "CREATE ROLE rt_actor",
				"GRANT USAGE ON SCHEMA public TO rt_owner,rt_actor",
				"GRANT CREATE ON SCHEMA public TO rt_actor",
				"CREATE TABLE rt_data(id integer)",
				"GRANT INSERT ON rt_data TO rt_actor",
				"SET application_name='trigger-outside'",
			} {
				enginetest.SessionSQL(t, sess, q)
			}
			result, tail := "trigger", "RETURN NEW;"
			if event {
				result, tail = "event_trigger", "RETURN;"
			}
			if outcome == "error" {
				tail = "RAISE EXCEPTION 'entry body failure';"
			}
			enginetest.SessionSQL(t, sess, "CREATE FUNCTION rt_entry() RETURNS "+result+" LANGUAGE plpgsql SECURITY DEFINER SET application_name='trigger-inside' SET datestyle='SQL, DMY' SET lock_timeout='37ms' AS $$BEGIN PERFORM set_config('demo.trigger_entry','body',true); "+tail+" END$$")
			enginetest.SessionSQL(t, sess, "ALTER FUNCTION rt_entry() OWNER TO rt_owner")
			command := "INSERT INTO public.rt_data VALUES(1)"
			if event {
				enginetest.SessionSQL(t, sess, "CREATE EVENT TRIGGER rt_hook ON ddl_command_end WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION rt_entry()")
				command = "CREATE TABLE public.rt_created(id integer)"
			} else {
				enginetest.SessionSQL(t, sess, "CREATE TRIGGER rt_hook BEFORE INSERT ON rt_data FOR EACH ROW EXECUTE FUNCTION rt_entry()")
			}
			if outcome == "partial_entry" {
				enginetest.SessionSQL(t, sess, "ALTER FUNCTION rt_entry() SET transaction_read_only=on")
			}
			retained := eventUnitProcedure(t, e, "rt_entry")
			configuration := retained.Configuration
			catalogBefore := e.Catalog.State
			data := enginetest.RoleTableRef(t, e, "rt_data")
			backing := e.Storage.Relations[data.Address]
			generation := backing.Generation
			op, err := sess.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err = e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("uncompleted entry witness"))
			statement := enginetest.Statement(t, command)
			if err := scope.Gate(statement); err != nil {
				t.Fatal(err)
			}
			beforeRole := op.RoleState()
			beforeSettings := sess.Settings.NestLevel
			beforeActions := len(sess.Settings.Actions)
			beforeTimeout := op.LockTimeout
			input, output := scope.Input, scope.Output
			e.Mu.Lock()
			foreign, err := scope.RoutineAuthorizationLocked()
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			foreign.State.CurrentUser = e.Roles().LookupRef("rt_actor")
			scope.ForeignActionAuth = &foreign
			beforeRowSecurity := scope.RowSecurity
			demand := core.ScopeSQLRoutine(scope)
			invoke := func() error {
				_, failure := scope.Execute(statement, 0, nil)
				return failure
			}
			var failure error
			if outcome == "restricted" {
				failure = scope.WithAmcheckOwner(e.Catalog.State.MustRelation(data.Address), func(*exec.Context) error { return invoke() })
			} else {
				failure = invoke()
			}
			if outcome == "cancel" {
				if !errors.Is(failure, context.Canceled) {
					t.Fatal("body cancellation was lost", failure)
				}
			} else if outcome == "error" || outcome == "partial_entry" {
				var diagnostic *sqlstate.Error
				code := "P0001"
				if outcome == "partial_entry" {
					code = "0A000"
				}
				if !errors.As(failure, &diagnostic) || diagnostic.Code != code {
					t.Fatal("primary entry/body error was lost", failure)
				}
			} else if failure != nil {
				t.Fatal(failure)
			}
			// Entry/exit recaptures catalog input against the enclosing RI
			// actor, not against the definer. Gate has not necessarily demanded
			// those resolvers yet, so the pre-Execute struct is not the expected
			// post-entry capture. Restricted exit instead restores its borrowed
			// outer presentation exactly.
			wantInput := input
			if outcome != "restricted" {
				// Execute installs the active command's domain adapters before
				// entry; they belong to this scope, not the pre-Execute input.
				wantInput.LockedDomainInput, wantInput.DomainChecks = scope, scope
				e.Mu.Lock()
				caller, captureErr := scope.RoutineAuthorizationLocked()
				if captureErr == nil && caller.State.CurrentUser != foreign.State.CurrentUser {
					t.Errorf("entry retained definer input actor: got %+v, want %+v", caller.State.CurrentUser, foreign.State.CurrentUser)
				}
				if captureErr == nil {
					// Event DDL may own a private candidate. Do not replace
					// its coherent caller capture with the committed root.
					captureErr = e.CaptureElementInput(caller, op, &wantInput)
					wantInput.ACLItem = e.CaptureACLItemInput(caller, op)
					if captureErr == nil {
						wantInput.TypeDiagnosticName, captureErr = core.CaptureTypeDiagnosticName(caller)
					}
				}
				e.Mu.Unlock()
				if captureErr != nil {
					t.Fatal(captureErr)
				}
			}
			wantCalls := 1
			if outcome == "partial_entry" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatal("body callback reachability", calls, wantCalls)
			}
			if depth := core.ScopeActiveSQLRoutineDepth(scope); depth != 0 {
				t.Errorf("entry leaked SQL routine depth: got %d, want 0", depth)
			}
			if op.EffectiveUser != nil {
				t.Errorf("entry leaked effective user: got %+v, want nil", op.EffectiveUser)
			}
			if op.Restricted != nil {
				t.Errorf("entry leaked restricted invocation: got %+v, want nil", op.Restricted)
			}
			if scope.ForeignActionAuth != &foreign {
				t.Errorf("entry lost enclosing RI authority: got %p, want %p", scope.ForeignActionAuth, &foreign)
			}
			if role := op.RoleState(); role != beforeRole {
				t.Errorf("entry changed caller role: got %+v, want %+v", role, beforeRole)
			}
			// SAVE exit removes its own frame, not the transaction base.
			// The first SAVE promotes an idle store from zero to base level
			// one until Finish; restricted invocation uses a detached store
			// and must not promote the physical session's journal.
			wantSettings := beforeSettings
			if outcome != "restricted" && wantSettings < 1 {
				wantSettings = 1
			}
			if level := sess.Settings.NestLevel; level != wantSettings {
				t.Errorf("entry leaked SAVE level: got %d, want transaction base %d (before entry %d)", level, wantSettings, beforeSettings)
			}
			if op.LockTimeout != beforeTimeout {
				t.Errorf("entry leaked lock timeout: got %s, want %s", op.LockTimeout, beforeTimeout)
			}
			if scope.EventCommand != nil {
				t.Errorf("entry leaked event command scratch: got %+v, want nil", scope.EventCommand)
			}
			// LOCAL on a variable absent from proconfig is not SAVE-owned.
			// Successful entry merges it into the transaction base; aborting
			// entry discards it. Finish below must roll it back in both cases.
			wantActions, wantDemo := beforeActions, "outside"
			if outcome == "success" {
				wantActions++
				wantDemo = "body"
			}
			if actions := len(sess.Settings.Actions); actions != wantActions {
				t.Errorf("entry caller journal action count: got %d, want %d", actions, wantActions)
			}
			for name := range sess.Settings.Actions {
				if name != "demo.trigger_entry" || outcome != "success" {
					t.Errorf("entry retained unexpected caller action %q", name)
				}
			}
			wantJournal := &session.SettingsStore{NestLevel: wantSettings}
			if outcome == "success" {
				wantJournal.Touch("demo.trigger_entry", session.SettingLocal)
			}
			if !reflect.DeepEqual(sess.Settings.Actions, wantJournal.Actions) && (len(sess.Settings.Actions) != 0 || len(wantJournal.Actions) != 0) {
				t.Error("entry did not retain exactly the unsaved LOCAL action at the transaction base")
			}
			if !core.SameSQLRoutineDemand(demand, core.ScopeSQLRoutine(scope)) {
				t.Fatal("entry leaked PL frame/diagnostic or lexical command policy")
			}
			routineTriggerPresentationFields(t, "input", scope.Input, wantInput)
			routineTriggerPresentationFields(t, "output", scope.Output, output)
			if scope.RowSecurity != beforeRowSecurity {
				t.Errorf("entry changed caller row security: got %t, want %t", scope.RowSecurity, beforeRowSecurity)
			}
			// Function equality is not closure equality. Check the captured
			// naming result against independent catalog types as well as the
			// retained caller capture, rather than ignoring this input field.
			if outcome != "restricted" {
				for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Text} {
					typ, _ := e.CatalogRead().LookupType(id)
					want := typ.DiagnosticName()
					if scope.Input.TypeDiagnosticName == nil {
						t.Error("entry lost caller type diagnostic capture")
					} else if got := scope.Input.TypeDiagnosticName(id); got != want {
						t.Errorf("input.TypeDiagnosticName(%v): got %q, want %q", id, got, want)
					}
					if got := wantInput.TypeDiagnosticName(id); got != want {
						t.Errorf("entry mutated retained caller type diagnostic capture: got %q, want %q", got, want)
					}
				}
			}
			// This is deliberately before Finish: the exact retained LOCAL
			// journal was asserted above, so do not use the idle-store helper.
			sess.Settings.Mu.Lock()
			demo, application := sess.Settings.Value("demo.trigger_entry"), sess.Settings.Value("application_name")
			sess.Settings.Mu.Unlock()
			if demo != wantDemo || application != "trigger-outside" {
				t.Errorf("entry effective caller settings: got (%q,%q), want (%q,%q)", demo, application, wantDemo, "trigger-outside")
			}
			if e.Catalog.State != catalogBefore || retained.Configuration != configuration || backing.Generation != generation || len(backing.Versions) != 0 {
				t.Fatal("private trigger command mutated retained catalog/configuration/storage backing")
			}
			// Even successful callbacks remain private until the existing command
			// owner finishes; vetoing it must discard the whole firing effect.
			veto := failure
			if veto == nil {
				veto = errors.New("entry witness Finish veto")
			}
			if finish := scope.Finish(veto); !errors.Is(finish, veto) {
				t.Fatal("Finish replaced primary diagnostic", finish)
			}
			if level := sess.Settings.NestLevel; level != beforeSettings {
				t.Errorf("Finish retained transaction settings base: got %d, want %d", level, beforeSettings)
			}
			if actions := len(sess.Settings.Actions); actions != beforeActions {
				t.Errorf("Finish retained setting actions: got %d, want %d", actions, beforeActions)
			}
			localOwnerValue(t, sess, "demo.trigger_entry", "outside")
			localOwnerValue(t, sess, "application_name", "trigger-outside")
			if op.LockTimeout != beforeTimeout {
				t.Errorf("Finish changed caller lock timeout: got %s, want %s", op.LockTimeout, beforeTimeout)
			}
			if e.Catalog.State != catalogBefore || backing.Generation != generation || len(backing.Versions) != 0 {
				t.Fatal("veto published callback or firing command prefix")
			}
		})
	}
}

// Report each field separately. Catalog resolvers are compared structurally
// against a caller-authorized capture; output formatters and execution boundary
// adapters retain their original backing. Only TypeDiagnosticName is a newly
// captured closure, whose catalog semantics are asserted at the call site.
func routineTriggerPresentationFields(t *testing.T, label string, got, want any) {
	t.Helper()
	g, w := reflect.ValueOf(got), reflect.ValueOf(want)
	for i := range g.NumField() {
		name := g.Type().Field(i).Name
		a, b := g.Field(i), w.Field(i)
		if name == "TypeDiagnosticName" {
			if a.IsNil() != b.IsNil() {
				t.Errorf("%s.%s capture presence: got %t, want %t", label, name, !a.IsNil(), !b.IsNil())
			}
			continue
		}
		if a.Kind() == reflect.Func {
			if a.Pointer() != b.Pointer() {
				t.Errorf("%s.%s changed callback backing: got %x, want %x", label, name, a.Pointer(), b.Pointer())
			}
		} else if a.Kind() == reflect.Pointer && a.Pointer() != b.Pointer() {
			t.Errorf("%s.%s changed pointer backing: got %x, want %x", label, name, a.Pointer(), b.Pointer())
		} else if a.Kind() == reflect.Interface && !a.IsNil() && !b.IsNil() && a.Elem().Kind() == reflect.Pointer && b.Elem().Kind() == reflect.Pointer && a.Elem().Pointer() != b.Elem().Pointer() {
			t.Errorf("%s.%s changed adapter backing: got %x, want %x", label, name, a.Elem().Pointer(), b.Elem().Pointer())
		} else if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			t.Errorf("%s.%s changed caller state/backing: got %#v, want %#v", label, name, a.Interface(), b.Interface())
		}
	}
}
