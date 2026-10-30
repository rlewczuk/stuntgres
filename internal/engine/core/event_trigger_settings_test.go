package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

func eventSettingStore(store *session.SettingsStore, enabled bool) {
	store.Mu.Lock()
	defer store.Mu.Unlock()
	store.Typed["event_triggers"] = settings.Value{Boolean: enabled}
	value := "off"
	if enabled { value = "on" }
	store.Values["event_triggers"] = value
}

func TestEventTriggerSettingCollection(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false:"finish", true:"error_unwind"}[fail], func(t *testing.T) {
			e := engine.New()
			actor := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE TABLE et_setting_owned(v integer)",
				"CREATE FUNCTION et_setting_owned() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$",
				"CREATE EVENT TRIGGER et_setting_end ON ddl_command_end EXECUTE FUNCTION et_setting_owned()",
				"CREATE EVENT TRIGGER et_setting_drop ON sql_drop EXECUTE FUNCTION et_setting_owned()",
				"SET event_triggers=off",
			} { enginetest.SessionSQL(t, actor, q) }
			before := e.Catalog.State
			frontier := e.Visibility.CommitSeq()
			target := enginetest.RoleTableRef(t, e, "et_setting_owned")
			op, err := actor.Acquire(context.Background())
			if err != nil { t.Fatal(err) }
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil { t.Fatal(err) }
			veto := errors.New("collection owner veto")
			defer scope.Finish(veto)
			if err := scope.Gate(enginetest.Statement(t, "ALTER TABLE et_setting_owned ADD COLUMN n integer")); err != nil { t.Fatal(err) }
			previous, err := scope.BeginEventCommand("ALTER TABLE")
			if err != nil { t.Fatal(err) }
			outer, journal := scope.EventCommand, scope.Catalog
			if previous != nil || outer == nil || !outer.CollectDDL || journal == nil || scope.Completed { t.Fatal("off discarded lexical inventory or journal") }
			entryDescriptors, entryNames := maps.Clone(journal.Descriptors), maps.Clone(journal.Names)
			if _, err := scope.Execute(enginetest.Statement(t, "CREATE TABLE et_setting_private(v integer)"), 0, nil); err != nil { t.Fatal(err) }
			// Real DDL transfers an immutable journal successor to the same
			// statement owner; only phase-only entry/exit preserves its pointer.
			if scope.EventCommand != outer || scope.Catalog == nil || scope.Catalog == journal || scope.PendingCatalog() != scope.Catalog || core.OperationCatalog(op) != scope.Catalog || scope.Completed || e.Catalog.State != before { t.Fatal("off let nested DDL escape the enclosing journal") }
			if !reflect.DeepEqual(entryDescriptors, journal.Descriptors) || !reflect.DeepEqual(entryNames, journal.Names) { t.Fatal("nested DDL mutated its predecessor journal") }
			journal = scope.Catalog
			privateKey := ids.NameKey{ClassID: ids.RelationClassID, Namespace: before.MustRelation(target.Address).Namespace(), Name: "et_setting_private"}
			if e.PrivateCatalogLocked(journal).NameAddress(privateKey) == (ids.ObjectAddress{}) || before.NameAddress(privateKey) != (ids.ObjectAddress{}) { t.Fatal("missing genuine private DDL effect") }
			// Use a real catalog descriptor and the ordinary staged deletion
			// identity producer, not fabricated dropped-object receipts.
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			provider := core.StageDropProvider(op.Context(), stage, scope, journal, nil)
			provider.MarkDropRoots([]ids.ObjectAddress{target.Address})
			if err := provider.Remove(target); err != nil { t.Fatal(err) }
			for _, removal := range stage.Removals() { scope.RecordDroppedObjects(removal.Dropped) }
			scope.RecordDDLCommand("ALTER TABLE", target.Address)
			if !outer.Dropped || len(outer.Records) == 0 || len(outer.Commands) != 1 || outer.Commands[0].ObjectID != uint32(target.Address.ObjectID) { t.Fatal("off lost real command/drop facts") }
			commands, records := outer.Commands, outer.Records
			enclosing, err := scope.BeginEventCommand("CREATE TABLE")
			if err != nil { t.Fatal(err) }
			if enclosing != outer || scope.EventCommand == outer || scope.Catalog != journal { t.Fatal("nested boundary replaced journal or enclosing owner") }
			primary := error(nil)
			if fail { primary = errors.New("nested primary failure") }
			if err := scope.FinishEventCommand(enclosing, primary); err != primary || scope.EventCommand != outer { t.Fatal("nested unwind lost exact enclosing pointer", err) }
			// Later admission must not reset the collector's owned slices.
			eventSettingStore(actor.Settings, true)
			if err := scope.FinishEventCommand(previous, primary); err != primary { t.Fatal(err) }
			if scope.EventCommand != nil || scope.Catalog != journal || &outer.Commands[0] != &commands[0] || &outer.Records[0] != &records[0] { t.Fatal("phase completion replaced collection or journal ownership") }
			if err := scope.Finish(veto); err != veto { t.Fatal(err) }
			if e.Catalog.State != before || e.Visibility.CommitSeq() != frontier || core.ActiveScopeOfLease(op.Lease) != nil { t.Fatal("veto published effects or retained live scope") }
		})
	}
}

func TestEventTriggerSettingEffectiveStore(t *testing.T) {
	for _, physical := range []bool{true, false} {
		t.Run(map[bool]string{true:"physical_on_detached_off", false:"physical_off_detached_on"}[physical], func(t *testing.T) {
			e := engine.New()
			actor := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE TABLE et_setting_audit(v integer)",
				"CREATE FUNCTION et_setting_record() RETURNS integer LANGUAGE SQL AS $$INSERT INTO public.et_setting_audit VALUES(7); SELECT 1$$",
				"CREATE FUNCTION et_setting_effective() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT public.et_setting_record() INTO n; END$$",
				"CREATE EVENT TRIGGER et_setting_start ON ddl_command_start WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION et_setting_effective()",
				"CREATE EVENT TRIGGER et_setting_end ON ddl_command_end WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION et_setting_effective()",
			} { enginetest.SessionSQL(t, actor, q) }
			eventSettingStore(actor.Settings, physical)
			audit := enginetest.RoleTableRef(t, e, "et_setting_audit")
			values, typed, actions := maps.Clone(actor.Settings.Values), maps.Clone(actor.Settings.Typed), maps.Clone(actor.Settings.Actions)
			op, err := actor.Acquire(context.Background())
			if err != nil { t.Fatal(err) }
			defer op.Done()
			op.Restricted = &session.RestrictedInvocation{Owner: actor.Authorization.State.CurrentUser}
			detached := op.EffectiveSettings()
			if detached == actor.Settings { t.Fatal("invocation did not detach settings") }
			eventSettingStore(detached, !physical)
			detachedValues, detachedTyped, detachedActions := maps.Clone(detached.Values), maps.Clone(detached.Typed), maps.Clone(detached.Actions)
			frame := op.Restricted
			mark := op.CaptureInvocation()
			scope, err := e.StartStatement(op)
			if err != nil { t.Fatal(err) }
			defer scope.Finish(context.Canceled)
			// StartStatement owns admission; Gate attaches the transaction
			// incarnation and lock ticket needed by actual callback DML.
			if err := scope.Gate(enginetest.Statement(t, "CREATE TABLE et_setting_effective_target(v integer)")); err != nil { t.Fatal(err) }
			previous, err := scope.BeginEventCommand("CREATE TABLE")
			if err != nil { t.Fatal(err) }
			if err := scope.FinishEventCommand(previous, nil); err != nil { t.Fatal(err) }
			want := 2
			if physical { want = 0 }
			if len(scope.Stage.Rows[audit.Address]) != want { t.Fatal("dispatch ignored operation-effective Boolean", len(scope.Stage.Rows[audit.Address]), want) }
			if op.Restricted != frame || op.EffectiveSettings() != detached || scope.EventCommand != nil { t.Fatal("callback leaked invocation or lexical frame") }
			if !reflect.DeepEqual(values, actor.Settings.Values) || !reflect.DeepEqual(typed, actor.Settings.Typed) || !reflect.DeepEqual(actions, actor.Settings.Actions) || !reflect.DeepEqual(detachedValues, detached.Values) || !reflect.DeepEqual(detachedTyped, detached.Typed) || !reflect.DeepEqual(detachedActions, detached.Actions) { t.Fatal("dispatch mutated caller settings/actions") }
			if err := scope.Finish(context.Canceled); err != context.Canceled { t.Fatal(err) }
			mark.Restore()
			if op.Restricted != frame || !reflect.DeepEqual(detachedValues, op.EffectiveSettings().Values) || !reflect.DeepEqual(detachedActions, op.EffectiveSettings().Actions) { t.Fatal("invocation checkpoint lost its caller image") }
			op.Restricted = nil
			if op.Restricted != nil || op.EffectiveSettings() != actor.Settings { t.Fatal("invocation unwind retained detached frame") }
		})
	}
}
