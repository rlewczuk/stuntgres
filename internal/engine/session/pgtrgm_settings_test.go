package session

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func TestPgTrgmSettingOwnership(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	fresh := func() *SettingsStore {
		s := &SettingsStore{Values: make(map[string]string)}
		s.InitializeShared(registry.SessionDefaults())
		return s
	}
	assign := func(s *SettingsStore, name, text string, action SettingAction) {
		s.Touch(name, action)
		s.Values[name], s.Typed[name] = text, settings.Value{Text: text}
		s.Placeholders[name] = true
	}
	load := func(t *testing.T, s *SettingsStore) []*sqlstate.Error {
		t.Helper()
		owner := s.pgTrgmAuthority()
		owner.Mu.Lock()
		if owner != s {
			s.Mu.Lock()
		}
		warnings, err := s.ActivatePgTrgmLocked(registry)
		if owner != s {
			s.Mu.Unlock()
		}
		owner.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		return warnings
	}
	value := func(t *testing.T, s *SettingsStore, name string, want float64) {
		t.Helper()
		s.Mu.Lock()
		got, err := s.PgTrgmThresholdLocked(registry, name)
		s.Mu.Unlock()
		if err != nil || got != want || s.TypedValue(name).Number != want {
			t.Fatalf("%s typed threshold=%v want=%v err=%v", name, got, want, err)
		}
	}
	warning := func(t *testing.T, got []*sqlstate.Error, name, input string) {
		t.Helper()
		d, _ := pgTrgmDefinition(name)
		_, err := d.Parse(input)
		var expected *sqlstate.Error
		if !errors.As(err, &expected) {
			t.Fatal("expected parser diagnostic", input, err)
		}
		copy := *expected
		copy.Severity, copy.SeverityUnlocalized = "WARNING", "WARNING"
		if len(got) != 1 || !reflect.DeepEqual(got[0], &copy) {
			t.Fatalf("promotion diagnostic=%+v expected=%+v", got, &copy)
		}
	}

	t.Run("lazy_exact_boots_and_metadata", func(t *testing.T) {
		s, peer := fresh(), fresh()
		sharedText, sharedTyped := maps.Clone(registry.SessionDefaults().Text), maps.Clone(registry.SessionDefaults().Typed)
		definitions := settings.PgTrgmDefinitions()
		boots := []float64{float64(float32(0.3)), float64(float32(0.6)), float64(float32(0.5))}
		displays := []string{"0.3", "0.6", "0.5"}
		descriptions := []string{"Sets the threshold used by the % operator.", "Sets the threshold used by the <% operator.", "Sets the threshold used by the <<% operator."}
		for _, d := range definitions {
			if _, err := s.LookupDefinition(registry, d.Name, false); err == nil {
				t.Fatal("unloaded definition", d.Name)
			}
			if _, known := s.SettingFlagsLocked(registry, d.Name); known {
				t.Fatal("unloaded flags")
			}
			if _, err := s.PgTrgmThresholdLocked(registry, d.Name); err == nil {
				t.Fatal("typed read implicitly loaded")
			}
		}
		if s.pgTrgmLoaded.Load() || !s.defaultsShared || len(s.EffectiveSettingNamesLocked(registry)) != len(registry.Order()) {
			t.Fatal("eager library state")
		}
		if notices := load(t, s); len(notices) != 0 {
			t.Fatal(notices)
		}
		for i, d := range definitions {
			if d.Type != settings.Floating || d.Access != settings.Session || d.Unit != settings.UnitNone || d.FloatMin != 0 || d.FloatMax != 1 || d.Flags != 0 || d.Category != "Customized Options" || d.ExtraDescription != "Valid range is 0.0 .. 1.0." || d.Description != descriptions[i] || d.BootValue().Number != boots[i] || d.BootDefault != displays[i] {
				t.Fatal("released metadata", d)
			}
			value(t, s, d.Name, boots[i])
			if s.Value(d.Name) != displays[i] || s.DefaultTyped[d.Name].Number != boots[i] || s.Placeholders[d.Name] {
				t.Fatal("boot/current/reset disagreement")
			}
			if flags, known := s.SettingFlagsLocked(registry, d.Name); !known || flags != 0 {
				t.Fatal("loaded zero flags", flags, known)
			}
			if _, err := peer.LookupDefinition(registry, d.Name, false); err == nil {
				t.Fatal("peer borrowed activation")
			}
			if _, exists := registry.Definitions()[d.Name]; exists {
				t.Fatal("registry mutated")
			}
		}
		names := s.EffectiveSettingNamesLocked(registry)
		if len(names) != len(registry.Order())+3 || !slices.IsSorted(names) {
			t.Fatal("effective enumeration", names)
		}
		if s.defaultsShared || !peer.defaultsShared || !reflect.DeepEqual(sharedText, registry.SessionDefaults().Text) || !reflect.DeepEqual(sharedTyped, registry.SessionDefaults().Typed) {
			t.Fatal("shared defaults mutated")
		}
		if notices := load(t, s); len(notices) != 0 {
			t.Fatal("duplicate load warnings", notices)
		}
	})

	for _, definition := range settings.PgTrgmDefinitions() {
		t.Run(definition.Name, func(t *testing.T) {
			name, boot := definition.Name, definition.BootValue().Number
			t.Run("committed_set_and_internal_null_reset", func(t *testing.T) {
				s := fresh()
				assign(s, name, "0.2", SettingSet)
				s.FinishTransaction(true)
				if got := load(t, s); len(got) != 0 {
					t.Fatal(got)
				}
				value(t, s, name, 0.2)
				if s.DefaultTyped[name].Number != boot || s.DefaultSources[name] != DefaultSourceDefault {
					t.Fatal("session SET became reset source")
				}
				s.Touch(name, SettingSet)
				s.Restore(name, settingOverride{})
				s.FinishTransaction(false)
				value(t, s, name, 0.2)
				s.Touch(name, SettingSet)
				s.Restore(name, settingOverride{})
				s.FinishTransaction(true)
				value(t, s, name, boot)

				s = fresh()
				assign(s, name, "0.2", SettingSet)
				s.FinishTransaction(true)
				s.Touch(name, SettingSet)
				s.Restore(name, settingOverride{})
				s.FinishTransaction(true)
				if got := load(t, s); len(got) != 0 {
					t.Fatal("RESET NULL warned as empty", got)
				}
				value(t, s, name, boot)
			})
			t.Run("invalid_current_and_inclusive_range", func(t *testing.T) {
				for _, input := range []string{"", "bogus", "2", "-1", "NaN", "Infinity", "-Infinity", "0.2s"} {
					s := fresh()
					assign(s, name, input, SettingSet)
					s.FinishTransaction(true)
					warning(t, load(t, s), name, input)
					value(t, s, name, boot)
					beforeValues, beforeTyped := maps.Clone(s.Values), maps.Clone(s.Typed)
					d, _ := s.LookupDefinition(registry, name, true)
					if _, _, err := s.ParseValue(d, input); err == nil {
						t.Fatal("loaded invalid SET accepted", input)
					}
					if !reflect.DeepEqual(beforeValues, s.Values) || !reflect.DeepEqual(beforeTyped, s.Typed) {
						t.Fatal("failed parse changed state")
					}
					if got := load(t, s); len(got) != 0 {
						t.Fatal("warning reissued", got)
					}
				}
				for _, input := range []string{"0", "1"} {
					v, err := definition.Parse(input)
					if err != nil || v.Number != float64(input[0]-'0') {
						t.Fatal("inclusive endpoint", input, v, err)
					}
				}
				_, err := definition.Parse("Infinity")
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Message != `Infinity is outside the valid range for parameter "`+name+`" (0 .. 1)` {
					t.Fatal("exact Infinity diagnostic", err)
				}
			})
			t.Run("full_set_local_history", func(t *testing.T) {
				cases := []struct {
					base, set, local  string
					inside, committed float64
					invalid           string
				}{
					{"0.2", "", "0.8", 0.8, 0.2, ""},
					{"0.2", "0.4", "0.8", 0.8, 0.4, ""},
					{"bogus", "", "0.8", 0.8, boot, "bogus"},
					{"0.2", "bogus", "0.8", 0.8, 0.2, "bogus"},
					{"0.2", "", "bogus", 0.2, 0.2, "bogus"},
				}
				for _, tc := range cases {
					for _, commit := range []bool{false, true} {
						s := fresh()
						assign(s, name, tc.base, SettingSet)
						s.FinishTransaction(true)
						if tc.set != "" {
							assign(s, name, tc.set, SettingSet)
						}
						assign(s, name, tc.local, SettingLocal)
						got := load(t, s)
						if tc.invalid != "" {
							warning(t, got, name, tc.invalid)
						} else if len(got) != 0 {
							t.Fatal(got)
						}
						value(t, s, name, tc.inside)
						s.FinishTransaction(commit)
						want := 0.2
						if tc.base == "bogus" {
							want = boot
						}
						if commit {
							want = tc.committed
						}
						value(t, s, name, want)
						if s.Actions != nil || s.NestLevel != 0 {
							t.Fatal("journal not retired")
						}
					}
				}
			})
			t.Run("save_marks_and_immutable_tails", func(t *testing.T) {
				s := fresh()
				assign(s, name, "0.2", SettingSet)
				s.FinishTransaction(true)
				outer := s.BeginSettingsSaveLocked()
				assign(s, name, "0.4", SettingSave)
				mark := s.Savepoint()
				markBefore := mark.Clone()
				inner := s.BeginSettingsSaveLocked()
				assign(s, name, "bogus", SettingSet)
				assign(s, name, "0.8", SettingLocal)
				oldTail := s.Actions[name].previous
				oldTailValue := *oldTail
				warning(t, load(t, s), name, "bogus")
				value(t, s, name, 0.8)
				if !reflect.DeepEqual(*oldTail, oldTailValue) || s.Actions[name].previous == oldTail {
					t.Fatal("promotion mutated/shared original tail")
				}
				if !reflect.DeepEqual(mark.Values, markBefore.Values) || !reflect.DeepEqual(mark.Actions, markBefore.Actions) {
					t.Fatal("promotion mutated retained mark")
				}
				s.EndSettingsSaveLocked(inner, false)
				value(t, s, name, 0.4)
				candidate := mark.Clone()
				if candidate.Typed[name].Number != 0.4 || candidate.Actions[name].original.Typed.Number != 0.2 || candidate.NestLevel != outer {
					t.Fatal("untyped candidate", candidate)
				}
				candidate.Values[name] = "0.9"
				if mark.Values[name] != "0.4" {
					t.Fatal("candidate map borrowed mark")
				}
				candidate = mark.Clone()
				s.RestoreSavepointLocked(candidate, authorization.RoleState{})
				value(t, s, name, 0.4)
				s.EndSettingsSaveLocked(outer, true)
				value(t, s, name, 0.2)
				s.FinishTransaction(false)
				value(t, s, name, 0.2)
				if got := load(t, s); len(got) != 0 {
					t.Fatal("restore reissued warning", got)
				}
			})
			t.Run("preload_savepoint_and_reset_all", func(t *testing.T) {
				for _, commit := range []bool{false, true} {
					s := fresh()
					assign(s, name, "0.2", SettingSet)
					s.FinishTransaction(true)
					mark := s.Savepoint()
					assign(s, name, "0.8", SettingLocal)
					if got := load(t, s); len(got) != 0 {
						t.Fatal(got)
					}
					value(t, s, name, 0.8)
					candidate := mark.Clone()
					if candidate.Typed[name].Number != 0.2 || mark.Typed[name].Text != "0.2" || mark.Typed[name].Number != 0 {
						t.Fatal("preload mark was not independently typed")
					}
					s.RestoreSavepointLocked(candidate, authorization.RoleState{})
					value(t, s, name, 0.2)
					s.ResetAllLocked(registry)
					value(t, s, name, boot)
					if s.Placeholders[name] {
						t.Fatal("RESET ALL reclassified real definition")
					}
					s.FinishTransaction(commit)
					want := 0.2
					if commit {
						want = boot
					}
					value(t, s, name, want)
					if _, exists := s.EffectiveDefinitionLocked(registry, name); !exists {
						t.Fatal("RESET ALL hid loaded definition")
					}
				}
			})
			t.Run("captured_reset_source", func(t *testing.T) {
				for _, reset := range []string{"0.4", "bogus"} {
					s := fresh()
					captured := map[string]CapturedDefault{name: {Input: reset, Source: DefaultSourceUser}}
					if err := s.CapturePersistedDefaults(t.Context(), registry, captured); err != nil {
						t.Fatal(err)
					}
					oldDefaults := maps.Clone(s.Defaults)
					assign(s, name, "0.8", SettingSet)
					s.FinishTransaction(true)
					got := load(t, s)
					want := 0.4
					if reset == "bogus" {
						warning(t, got, name, reset)
						want = boot
					} else if len(got) != 0 {
						t.Fatal(got)
					}
					value(t, s, name, 0.8)
					if s.DefaultTyped[name].Number != want || oldDefaults[name] != reset || captured[name].Input != reset {
						t.Fatal("reset capture/source changed")
					}
					if reset != "bogus" && s.DefaultSources[name] != DefaultSourceUser {
						t.Fatal("valid source discarded")
					}
					if reset == "bogus" && s.DefaultSources[name] != DefaultSourceDefault {
						t.Fatal("invalid source retained")
					}
					s.Touch(name, SettingSet)
					s.Restore(name, settingOverride{})
					s.FinishTransaction(true)
					value(t, s, name, want)
				}
			})
		})
	}

	t.Run("reserved_prefix_never_resurrects", func(t *testing.T) {
		s := fresh()
		assign(s, "pg_trgm.typo", "anything", SettingSet)
		s.FinishTransaction(true)
		mark := s.Savepoint()
		assign(s, "pg_trgm.typo", "local", SettingLocal)
		got := load(t, s)
		want := &sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42602", Message: `invalid configuration parameter name "pg_trgm.typo", removing it`, Detail: `"pg_trgm" is now a reserved prefix.`}
		if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
			t.Fatal("reservation diagnostic", got)
		}
		candidate := mark.Clone()
		if _, exists := candidate.Values["pg_trgm.typo"]; exists {
			t.Fatal("mark retained removed value")
		}
		s.RestoreSavepointLocked(candidate, authorization.RoleState{})
		s.FinishTransaction(false)
		if s.Placeholders["pg_trgm.typo"] || s.Values["pg_trgm.typo"] != "" {
			t.Fatal("rollback resurrected typo")
		}
		if _, err := s.LookupDefinition(registry, "pg_trgm.typo", false); err == nil {
			t.Fatal("removed SHOW recognized")
		}
		_, err := s.LookupDefinition(registry, "pg_trgm.typo", true)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42602", Message: `invalid configuration parameter name "pg_trgm.typo"`, Detail: `"pg_trgm" is a reserved prefix.`}) {
			t.Fatal("reserved SET diagnostic", err)
		}
		if mark.Values["pg_trgm.typo"] != "anything" {
			t.Fatal("retained source mutated")
		}
		if got := load(t, s); len(got) != 0 {
			t.Fatal("reservation warning duplicated")
		}
	})

	t.Run("physical_invocation_exception_and_close", func(t *testing.T) {
		root, peer := fresh(), fresh()
		name := "pg_trgm.similarity_threshold"
		assign(root, name, "bogus", SettingSet)
		root.FinishTransaction(true)
		physical := &Session{Settings: root}
		outer := &RestrictedInvocation{}
		op := &Operation{Session: physical, Restricted: outer}
		parent := op.EffectiveSettings()
		assign(parent, name, "0.4", SettingLocal)
		checkpoint := op.CaptureInvocation()
		retained := checkpoint.image
		retainedValues := maps.Clone(retained.Values)
		op.Restricted = &RestrictedInvocation{Parent: outer}
		child := op.EffectiveSettings()
		assign(child, name, "0.8", SettingLocal)
		warning(t, load(t, child), name, "bogus")
		value(t, child, name, 0.8)
		value(t, parent, name, 0.4)
		value(t, root, name, float64(float32(0.3)))
		if child.pgTrgmAuthority() != root || parent.pgTrgmAuthority() != root || peer.pgTrgmLoaded.Load() {
			t.Fatal("physical owner escaped")
		}
		op.Restricted = outer
		checkpoint.PrepareRestore()
		if checkpoint.prepared == retained || !reflect.DeepEqual(retained.Values, retainedValues) || retained.pgTrgmPromoted {
			t.Fatal("preflight mutated retained image")
		}
		if checkpoint.prepared.Typed[name].Number != 0.4 || checkpoint.prepared.Actions[name].original.Present {
			t.Fatal("exception candidate is not typed fallback")
		}
		checkpoint.Restore()
		value(t, op.EffectiveSettings(), name, 0.4)
		op.EffectiveSettings().FinishTransaction(false)
		value(t, op.EffectiveSettings(), name, float64(float32(0.3)))
		if got := load(t, op.EffectiveSettings()); len(got) != 0 {
			t.Fatal("exception reload warned", got)
		}
		if err := root.EndSession(ids.SessionToken{}, CleanClose); err != nil {
			t.Fatal(err)
		}
		if root.pgTrgmLoaded.Load() || root.Defaults != nil || root.Typed != nil || root.Actions != nil || root.Placeholders != nil {
			t.Fatal("close retained physical settings")
		}
		if _, err := root.ActivatePgTrgmLocked(registry); err == nil {
			t.Fatal("retired owner activated")
		}
		if _, err := peer.PgTrgmThresholdLocked(registry, name); err == nil {
			t.Fatal("peer activation leaked")
		}
	})

	t.Run("conflicting_definition_is_atomic", func(t *testing.T) {
		d, err := settings.Validate(settings.Definition{Name: "pg_trgm.word_similarity_threshold", Type: settings.String, BootDefault: "registered"})
		if err != nil {
			t.Fatal(err)
		}
		conflicting := settings.NewRegistry([]settings.Definition{d}, false)
		s := &SettingsStore{Values: map[string]string{}}
		s.InitializeShared(conflicting.SessionDefaults())
		assign(s, "pg_trgm.similarity_threshold", "0.2", SettingSet)
		beforeValues, beforeTyped, beforeActions := maps.Clone(s.Values), maps.Clone(s.Typed), maps.Clone(s.Actions)
		beforeDefaults := maps.Clone(s.Defaults)
		warnings, err := s.ActivatePgTrgmLocked(conflicting)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != `attempt to redefine parameter "pg_trgm.word_similarity_threshold"` || len(warnings) != 0 {
			t.Fatal("redefinition diagnostic", err, warnings)
		}
		if s.pgTrgmLoaded.Load() || s.pgTrgmPromoted || !s.defaultsShared || !reflect.DeepEqual(s.Values, beforeValues) || !reflect.DeepEqual(s.Typed, beforeTyped) || !reflect.DeepEqual(s.Actions, beforeActions) || !reflect.DeepEqual(s.Defaults, beforeDefaults) {
			t.Fatal("failed preparation published partial state")
		}
		if _, exists := s.Defaults["pg_trgm.similarity_threshold"]; exists {
			t.Fatal("partial boot published")
		}
	})
}
