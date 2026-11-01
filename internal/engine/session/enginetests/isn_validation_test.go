package enginetests_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

func TestIsnValidationSettings(t *testing.T) {
	for _, placeholder := range []string{"", "true", "bogus"} {
		for _, local := range []bool{false, true} {
			defaults := map[string]string{"application_name": "original"}
			typedDefaults := map[string]settings.Value{"application_name": {Text: "original"}}
			s := &session.SettingsStore{Values: make(map[string]string)}
			s.InitializeShared(settings.SessionDefaults{Text: defaults, Typed: typedDefaults})
			if s.IsnLoaded() { t.Fatal("new physical owner already loaded") }
			if placeholder != "" {
				action := session.SettingSet
				if local { action = session.SettingLocal }
				s.Touch("isn.weak", action)
				s.Values["isn.weak"], s.Typed["isn.weak"] = placeholder, settings.Value{Text: placeholder}
				s.Placeholders["isn.weak"] = true
			}
			warning := s.LoadIsnLocked()
			if !s.IsnLoaded() || s.Placeholders["isn.weak"] || session.IsnDefinition().BootDefault != "off" { t.Fatal("module recognition/default") }
			if _, shared := defaults["isn.weak"]; shared { t.Fatal("module changed shared registry defaults") }
			if _, shared := typedDefaults["isn.weak"]; shared { t.Fatal("module changed shared typed defaults") }
			if (warning != nil) != (placeholder == "bogus") { t.Fatal("placeholder warning", placeholder, warning) }
			if warning != nil && (warning.Severity != "WARNING" || warning.SeverityUnlocalized != "WARNING" || warning.Code != "22023" || warning.Message != `parameter "isn.weak" requires a Boolean value` || warning.Detail != "" || warning.Hint != "" || warning.Position != "" || warning.Context != "") { t.Fatal("promotion diagnostic", warning) }
			want := "off"
			if placeholder == "true" { want = "on" }
			if s.Value("isn.weak") != want || s.TypedValue("isn.weak").Boolean != (want == "on") { t.Fatal("canonical promotion", placeholder) }
			if s.LoadIsnLocked() != nil { t.Fatal("module promotion repeated") }
			s.FinishTransaction(true)
			if local { want = "off" }
			if s.Value("isn.weak") != want || !s.IsnLoaded() { t.Fatal("LOCAL commit or recognition lifetime") }
			assign := func(on bool, action session.SettingAction) {
				s.Touch("isn.weak", action)
				text := "off"
				if on { text = "on" }
				value, err := session.IsnDefinition().Parse(text)
				if err != nil { t.Fatal(err) }
				s.Values["isn.weak"], s.Typed["isn.weak"] = text, value
			}
			assign(false, session.SettingSet)
			s.FinishTransaction(true)
			assign(true, session.SettingSet)
			s.FinishTransaction(false)
			if s.Value("isn.weak") != "off" || !s.IsnLoaded() { t.Fatal("rollback lost value or unloaded module") }
			level := s.BeginSettingsSaveLocked()
			assign(true, session.SettingSave)
			s.EndSettingsSaveLocked(level, true)
			if s.Value("isn.weak") != "off" { t.Fatal("routine SAVE failed") }
			level = s.BeginSettingsSaveLocked()
			assign(true, session.SettingSave)
			assign(true, session.SettingSet)
			s.EndSettingsSaveLocked(level, true)
			if s.Value("isn.weak") != "on" { t.Fatal("ordinary SET failed to merge out of SAVE") }
			s.FinishTransaction(false)
			if s.Value("isn.weak") != "off" { t.Fatal("merged SET lost transaction original") }
			other := &session.SettingsStore{}
			if other.IsnLoaded() { t.Fatal("recognition leaked to another physical owner") }
		}
	}
	for _, placeholder := range []string{"", "true", "bogus"} {
		s := &session.SettingsStore{Values: make(map[string]string), Typed: make(map[string]settings.Value), Defaults: make(map[string]string), DefaultTyped: make(map[string]settings.Value), Placeholders: make(map[string]bool)}
		if placeholder != "" {
			s.Touch("isn.weak", session.SettingSet)
			s.Values["isn.weak"], s.Typed["isn.weak"] = placeholder, settings.Value{Text: placeholder}
			s.Placeholders["isn.weak"] = true
		}
		level := s.BeginSettingsSaveLocked()
		s.Touch("isn.weak", session.SettingSave)
		mark := s.Savepoint()
		before := mark.Clone()
		warning := s.LoadIsnLocked()
		if (warning != nil) != (placeholder == "bogus") { t.Fatal("pre-load mark warning", warning) }
		candidate := s.PrepareSavepointRestore(mark)
		s.Values["isn.weak"], s.Typed["isn.weak"] = "on", settings.Value{Boolean: true}
		s.RestoreSavepointLocked(candidate, authorization.RoleState{})
		want := "off"
		if placeholder == "true" { want = "on" }
		if s.Value("isn.weak") != want || s.TypedValue("isn.weak").Boolean != (want == "on") || !s.IsnLoaded() { t.Fatal("pre-load mark restored untyped image", placeholder) }
		if s.LoadIsnLocked() != nil { t.Fatal("rollback promotion repeated warning") }
		if !reflect.DeepEqual(mark, before) { t.Fatal("promotion mutated retained savepoint mark") }
		s.EndSettingsSaveLocked(level, true)
		s.FinishTransaction(false)
		if s.Value("isn.weak") != "off" || !s.IsnLoaded() { t.Fatal("restored masked tail lost rollback/default") }
	}
}
