package session

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func TestAutoExplainModuleSettings(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	setter := (&authorization.Domain{}).Principal(100)
	fresh := func() *SettingsStore {
		s := &SettingsStore{Values: make(map[string]string)}
		s.InitializeShared(registry.SessionDefaults())
		return s
	}
	assign := func(s *SettingsStore, name, value string, action SettingAction, superuser bool) {
		s.Touch(name, action)
		s.Values[name], s.Typed[name], s.Placeholders[name] = value, settings.Value{Text: value}, true
		s.CaptureAutoExplainSetterLocked(name, setter, superuser, true)
	}
	load := func(s *SettingsStore, allowed bool) []*sqlstate.Error {
		t.Helper()
		owner := s.autoExplainAuthority()
		owner.Mu.Lock()
		defer owner.Mu.Unlock()
		if owner != s {
			s.Mu.Lock()
			defer s.Mu.Unlock()
		}
		warnings, err := s.ActivateAutoExplainLocked(nil, registry, func(string, authorization.PrincipalRef) (bool, error) { return allowed, nil })
		if err != nil {
			t.Fatal(err)
		}
		return warnings
	}
	t.Run("lazy_typed_union_and_terminal", func(t *testing.T) {
		s, peer := fresh(), fresh()
		defaults := maps.Clone(registry.SessionDefaults().Text)
		if s.AutoExplainLoaded() || len(s.EffectiveSettingNamesLocked(registry)) != len(registry.Order()) {
			t.Fatal("eager module activation")
		}
		if warnings := load(s, true); len(warnings) != 0 {
			t.Fatal(warnings)
		}
		names := s.EffectiveSettingNamesLocked(registry)
		if !slices.IsSorted(names) || len(names) != len(registry.Order())+12 {
			t.Fatal("typed effective union", names)
		}
		for _, d := range settings.AutoExplainDefinitions() {
			got, ok := s.EffectiveDefinitionLocked(registry, d.Name)
			if !ok || got.Access != settings.Privileged || !reflect.DeepEqual(got.BootValue(), s.DefaultTyped[d.Name]) || s.Placeholders[d.Name] {
				t.Fatal("typed default/metadata disagreement", got)
			}
			if _, ok := peer.EffectiveDefinitionLocked(registry, d.Name); ok {
				t.Fatal("peer borrowed physical activation")
			}
		}
		if !reflect.DeepEqual(defaults, registry.SessionDefaults().Text) {
			t.Fatal("shared registry reset map changed")
		}
		child := cloneInvocationSettings(s)
		if child.autoExplainAuthority() != s || !child.AutoExplainLoaded() {
			t.Fatal("detached authority changed")
		}
		if err := s.EndSession(ids.SessionToken{}, CleanClose); err != nil {
			t.Fatal(err)
		}
		if s.AutoExplainLoaded() || child.AutoExplainLoaded() || s.Defaults != nil || s.Actions != nil || s.autoExplainSetters != nil {
			t.Fatal("terminal physical module retained")
		}
		if _, err := s.ActivateAutoExplainLocked(nil, registry, nil); err == nil {
			t.Fatal("ended module reactivated")
		}
	})
	t.Run("original_context_and_warning_identity", func(t *testing.T) {
		s := fresh()
		assign(s, "auto_explain.log_analyze", "true", SettingSet, true)
		captured := s.autoExplainSetters["auto_explain.log_analyze"]
		if captured.principal != setter || captured.assignment == 0 || !captured.superuser {
			t.Fatal("original privileged context was not captured as an owned assignment", captured)
		}
		assign(s, "auto_explain.typo", "remove", SettingSet, false)
		before := s.Savepoint()
		retained := maps.Clone(before.Values)
		child := cloneInvocationSettings(s)
		if before.autoExplainSetters["auto_explain.log_analyze"] != captured || child.autoExplainSetters["auto_explain.log_analyze"] != captured {
			t.Fatal("savepoint/invocation lost original context or assignment identity")
		}
		warnings := load(child, false)
		if len(warnings) != 1 || warnings[0].Code != "42602" || child.Value("auto_explain.log_analyze") != "on" || s.Value("auto_explain.log_analyze") != "on" {
			t.Fatal("original superuser context lost or duplicate image warning", warnings)
		}
		if !reflect.DeepEqual(before.Values, retained) || before.autoExplainPromoted {
			t.Fatal("load mutated retained mark")
		}
		promoted := before.Clone()
		if promoted.Typed["auto_explain.log_analyze"].Boolean != true || promoted.Values["auto_explain.typo"] != "" || !reflect.DeepEqual(before.Values, retained) {
			t.Fatal("retained image promotion/reservation", promoted)
		}
		if before.autoExplainSetters["auto_explain.log_analyze"] != captured {
			t.Fatal("historical reconciliation rewrote the retained original context")
		}
		if warnings := load(s, false); len(warnings) != 0 {
			t.Fatal("repeated load warned again", warnings)
		}
	})
	t.Run("distinct_equal_assignments_and_frozen_permissions", func(t *testing.T) {
		s := fresh()
		const name = "auto_explain.log_analyze"
		s.NestLevel = 1
		assign(s, name, "bogus", SettingLocal, false)
		first := s.autoExplainSetters[name]
		s.NestLevel = 2
		assign(s, name, "bogus", SettingLocal, false)
		second := s.autoExplainSetters[name]
		action := s.Actions[name]
		original := action.original
		if first.principal != setter || second.principal != setter || first.superuser || second.superuser ||
			first.assignment == 0 || second.assignment <= first.assignment ||
			original.Setter != setter || original.Assignment != first.assignment || original.SuperuserContext {
			t.Fatal("distinct ordinary-context assignments lost original scalar facts", first, second, original)
		}
		mark := s.Savepoint()
		warnings := load(s, false)
		want := sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42501", Message: `permission denied to set parameter "auto_explain.log_analyze"`}
		if len(warnings) != 2 || *warnings[0] != want || *warnings[1] != want {
			t.Fatal("distinct assignments collapsed or candidate parsed before authority", warnings)
		}
		promoted := mark.Clone()
		if promoted.Values[name] != "" || !promoted.autoExplainPromoted || mark.Values[name] != "bogus" {
			t.Fatal("late mark reconciliation changed frozen replay permission or retained backing")
		}
		if mark.autoExplainSetters[name] != second || mark.Actions[name].previous != action.previous ||
			!reflect.DeepEqual(mark.Actions[name], action) {
			t.Fatal("promotion mutated retained original context, action or journal backing")
		}
		if notices := load(s, true); len(notices) != 0 || s.Value(name) != "off" {
			t.Fatal("later authorization revived a rejected historical assignment", notices)
		}
	})
	for _, maskedDenied := range []bool{false, true} {
		t.Run(map[bool]string{true: "masked_set_denied", false: "local_denied"}[maskedDenied], func(t *testing.T) {
			s := fresh()
			const name = "auto_explain.log_min_duration"
			assign(s, name, "100", SettingSet, true)
			s.FinishTransaction(true)
			s.NestLevel = 1
			assign(s, name, "200", SettingSet, !maskedDenied)
			assign(s, name, "300", SettingLocal, maskedDenied)
			// Use a genuine nonzero original identity, not loader authority.
			warnings := load(s, false)
			if len(warnings) != 1 || warnings[0].Code != "42501" {
				t.Fatal("original setter permission-before-parse", warnings)
			}
			want := "200ms"
			if maskedDenied {
				want = "300ms"
			}
			if s.Value(name) != want {
				t.Fatal("successful-action replay lost preceding state", s.Value(name), want)
			}
			s.FinishTransaction(true)
			want = "200ms"
			if maskedDenied {
				want = "100ms"
			}
			if s.Value(name) != want {
				t.Fatal("failed replay fabricated journal node", s.Value(name), want)
			}
		})
	}
}
