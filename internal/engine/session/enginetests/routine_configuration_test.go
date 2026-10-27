package enginetests_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

func TestRoutineConfigurationSaveActions(t *testing.T) {
	newStore := func() *session.SettingsStore {
		return &session.SettingsStore{
			Defaults: map[string]string{"x": "default"}, DefaultTyped: map[string]settings.Value{"x": {Text: "default"}},
			Values: map[string]string{"x": "outside"}, Typed: map[string]settings.Value{"x": {Text: "outside"}},
		}
	}
	assign := func(s *session.SettingsStore, name, text string, action session.SettingAction) {
		s.Touch(name, action)
		s.Values[name], s.Typed[name] = text, settings.Value{Text: text}
	}
	for _, commit := range []bool{false, true} {
		for _, action := range []session.SettingAction{session.SettingSave, session.SettingLocal, session.SettingSet} {
			s := newStore()
			level := s.BeginSettingsSaveLocked()
			assign(s, "x", "configured", session.SettingSave)
			if action != session.SettingSave {
				assign(s, "x", "body", action)
			}
			assign(s, "unsaved", "escaped", session.SettingSet)
			s.EndSettingsSaveLocked(level, commit)
			want := "outside"
			if commit && action == session.SettingSet {
				want = "body"
			}
			if s.Value("x") != want || s.TypedValue("x").Text != want || s.NestLevel != 1 {
				t.Fatal("SAVE exit lost typed value or level", action, commit, s.Values, s.Typed, s.NestLevel)
			}
			if commit && s.Value("unsaved") != "escaped" || !commit && s.Value("unsaved") != "" {
				t.Fatal("SAVE incorrectly snapshots unsaved variables", commit, s.Values)
			}
			s.FinishTransaction(false)
			if s.Value("x") != "outside" || len(s.Actions) != 0 || s.NestLevel != 0 || s.Value("unsaved") != "" {
				t.Fatal("root rollback lost escaped SET original", s.Values, s.NestLevel)
			}
		}
	}
	t.Run("nested_set_local_merge", func(t *testing.T) {
		s := newStore()
		outer := s.BeginSettingsSaveLocked()
		assign(s, "x", "outer", session.SettingSave)
		inner := s.BeginSettingsSaveLocked()
		assign(s, "x", "inner", session.SettingSave)
		assign(s, "x", "escaped", session.SettingSet)
		assign(s, "x", "masked", session.SettingLocal)
		s.EndSettingsSaveLocked(inner, true)
		if s.Value("x") != "masked" {
			t.Fatal("inner exit restored SET candidate too early", s.Values)
		}
		s.EndSettingsSaveLocked(outer, true)
		if s.Value("x") != "masked" {
			t.Fatal("outer exit restored transaction candidate too early", s.Values)
		}
		s.FinishTransaction(true)
		if s.Value("x") != "escaped" || s.TypedValue("x").Text != "escaped" {
			t.Fatal("commit lost nested masked SET", s.Values, s.Typed)
		}
	})
	t.Run("caught_subtransaction_and_partial_entry", func(t *testing.T) {
		s := newStore()
		level := s.BeginSettingsSaveLocked()
		assign(s, "x", "configured", session.SettingSave)
		mark := s.Savepoint()
		before := mark.Clone()
		assign(s, "x", "escaped", session.SettingSet)
		assign(s, "x", "masked", session.SettingLocal)
		inner := s.BeginSettingsSaveLocked()
		assign(s, "x", "nested", session.SettingSave)
		s.EndSettingsSaveLocked(inner, false)
		s.RestoreSavepointLocked(mark.Clone(), authorization.RoleState{})
		if s.Value("x") != "configured" || s.NestLevel != level || !reflect.DeepEqual(before, mark) {
			t.Fatal("exception restoration mutated retained mark", s.Values, s.NestLevel)
		}
		// Entry failure after the first successful setting uses the same abort.
		s.EndSettingsSaveLocked(level, false)
		if s.Value("x") != "outside" || len(s.Actions) != 0 {
			t.Fatal("partial entry leaked first setting", s.Values, s.Actions)
		}
	})
}
