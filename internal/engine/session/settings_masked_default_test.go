package session

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/settings"
)

func TestSettingsMaskedDefaultOwnership(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, originalPresent := range []bool{false, true} {
			// The formatted default deliberately loses precision. Masking must
			// preserve captured typed precision and never reparse that display.
			captured := settings.Value{Number: 1.234567891234}
			s := &SettingsStore{
				Defaults:     map[string]string{"x": "1.23457"},
				DefaultTyped: map[string]settings.Value{"x": captured},
				Values:       map[string]string{}, Typed: map[string]settings.Value{},
			}
			if originalPresent {
				s.Restore("x", settingOverride{Present: true, Text: "2", Typed: settings.Value{Number: 2}})
			}
			original := s.override("x")
			s.Touch("x", SettingSet)
			s.Restore("x", settingOverride{}) // RESET/SET DEFAULT/null set_config.
			if s.override("x").Present {
				t.Fatal("reset itself created session source")
			}
			s.Touch("x", SettingLocal)
			s.Restore("x", settingOverride{Present: true, Text: "3", Typed: settings.Value{Number: 3}})
			action := s.Actions["x"]
			if !reflect.DeepEqual(action.original, original) || !action.candidate.Present || action.candidate.Text != "1.23457" || !reflect.DeepEqual(action.candidate.Typed, captured) {
				t.Fatal("masked default lost candidate/original ownership", action)
			}
			mark := s.Savepoint()
			s.Touch("x", SettingSet)
			s.Restore("x", settingOverride{Present: true, Text: "4", Typed: settings.Value{Number: 4}})
			s.RestoreSavepointLocked(mark, authorization.RoleState{})
			if !reflect.DeepEqual(s.Actions["x"], action) || s.Value("x") != "3" {
				t.Fatal("savepoint did not restore masked candidate")
			}
			s.FinishTransaction(commit)
			want := original
			if commit {
				want = settingOverride{Present: true, Text: "1.23457", Typed: captured}
			}
			if !reflect.DeepEqual(s.override("x"), want) || s.Actions != nil || !reflect.DeepEqual(s.DefaultTyped["x"], captured) {
				t.Fatalf("commit=%v originalPresent=%v got=%+v want=%+v", commit, originalPresent, s.override("x"), want)
			}
		}
	}
}
