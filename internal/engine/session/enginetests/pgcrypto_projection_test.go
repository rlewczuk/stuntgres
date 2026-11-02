package enginetests_test

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"testing"

	rootengine "stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

func TestPgcryptoModuleCombinedProjection(t *testing.T) {
	trigrams := []struct {
		name, operator, bootText string
		boot float64
	}{
		{"pg_trgm.similarity_threshold", "%", "0.3", float64(float32(0.3))},
		{"pg_trgm.word_similarity_threshold", "<%", "0.6", float64(float32(0.6))},
		{"pg_trgm.strict_word_similarity_threshold", "<<%", "0.5", float64(float32(0.5))},
	}
	baseline := [3]string{"0.2", "0.4", "0.8"}
	local := [3]string{"0.25", "0.5", "0.75"}
	parentValues := [3]string{"0.125", "0.125", "0.125"}
	childValues := [3]string{"0.875", "0.875", "0.875"}
	for _, order := range [][3]string{
		{"Isn", "Pgcrypto", "PgTrgm"}, {"Isn", "PgTrgm", "Pgcrypto"},
		{"Pgcrypto", "Isn", "PgTrgm"}, {"Pgcrypto", "PgTrgm", "Isn"},
		{"PgTrgm", "Isn", "Pgcrypto"}, {"PgTrgm", "Pgcrypto", "Isn"},
	} {
		t.Run(order[0]+"Then"+order[1]+"Then"+order[2], func(t *testing.T) {
			registered, err := settings.Validate(settings.Definition{Name: "isn.weak", Type: settings.String, Access: settings.Session, BootDefault: "registered", Description: "registered sentinel"})
			if err != nil { t.Fatal(err) }
			unrelated, err := settings.Validate(settings.Definition{Name: "projection.keep", Type: settings.String, Access: settings.Session, BootDefault: "keep"})
			if err != nil { t.Fatal(err) }
			sibling, err := settings.Validate(settings.Definition{Name: "pgcrypto.registered", Type: settings.String, Access: settings.Session, BootDefault: "sibling"})
			if err != nil { t.Fatal(err) }
			e := rootengine.NewWithSettings([]settings.Definition{registered, unrelated, sibling}, "", false)
			sharedText := maps.Clone(e.Settings.SessionDefaults().Text)
			sharedTyped := maps.Clone(e.Settings.SessionDefaults().Typed)
			s := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			op, err := s.Acquire(t.Context())
			if err != nil { t.Fatal(err) }
			defer op.Done()
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			if err != nil { t.Fatal(err) }
			setter := auth.State.CurrentUser
			s.Settings.Mu.Lock()
			err = s.Settings.CapturePersistedDefaults(t.Context(), e.Settings, map[string]session.CapturedDefault{
				trigrams[1].name: {Input: "0.625", Source: session.DefaultSourceUser},
			})
			s.Settings.Mu.Unlock()
			if err != nil { t.Fatal(err) }
			assignTrigrams := func(values [3]string, action session.SettingAction) {
				t.Helper()
				for i, setting := range trigrams {
					pgcryptoModuleAssign(t, e, op, setting.name, values[i], action, setter)
				}
			}
			pgcryptoModuleAssign(t, e, op, "isn.weak", "on", session.SettingSet, setter)
			pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "off", session.SettingSet, setter)
			pgcryptoModuleAssign(t, e, op, sibling.Name, "kept", session.SettingSet, setter)
			assignTrigrams(baseline, session.SettingSet)
			s.Settings.FinishTransaction(true)

			check := func(store *session.SettingsStore, loaded [3]bool, weakValue, cryptoValue string, thresholds [3]string) {
				t.Helper()
				store.Mu.Lock()
				defer store.Mu.Unlock()
				s.Mu.Lock()
				defer s.Mu.Unlock()
				var names []string
				counts := map[string]int{}
				if err := store.VisitDefinitionsLocked(e.Settings, func(d settings.Definition) error {
					names = append(names, d.Name)
					counts[d.Name]++
					return nil
				}); err != nil { t.Fatal(err) }
				if !slices.IsSorted(names) || !slices.Equal(names, store.EffectiveSettingNamesLocked(e.Settings)) { t.Fatal("module projections disagree on registry order", names) }
				for name, count := range counts { if count != 1 { t.Fatal("duplicate active definition", name, count) } }
				wantCount := len(e.Settings.Order())
				if loaded[1] { wantCount++ }
				if loaded[2] { wantCount += 3 }
				if len(names) != wantCount { t.Fatal("active union has missing or extra names", len(names), wantCount) }
				d, err := store.LookupDefinition(e.Settings, "ISN.WEAK", false)
				if err != nil { t.Fatal(err) }
				effective, known := store.EffectiveDefinitionLocked(e.Settings, "ISN.WEAK")
				if !known || !reflect.DeepEqual(d, effective) { t.Fatal("lookup and effective ISN metadata differ") }
				flags, recognized := store.SettingFlagsLocked(e.Settings, "ISN.WEAK")
				if !recognized || flags != 0 || counts[d.Name] != 1 || d.Access != settings.Session || d.Category != "Customized Options" || store.Value(d.Name) != weakValue { t.Fatal("ISN definition/value/context/flags", d, flags, weakValue) }
				if loaded[0] {
					if d.Type != settings.Boolean || d.BootDefault != "off" || d.Description != "Accept input with invalid ISN check digits." || d.Format(store.DefaultTyped[d.Name]) != "off" || store.TypedValue(d.Name).Boolean != (weakValue == "on") { t.Fatal("loaded ISN did not override real registry metadata/reset", d, store.DefaultTyped[d.Name]) }
				} else if d.Type != settings.String || d.BootDefault != "registered" || d.Description != "registered sentinel" || d.Format(store.DefaultTyped[d.Name]) != "registered" { t.Fatal("unloaded ISN changed genuine registered definition", d) }
				d, err = store.LookupDefinition(e.Settings, settings.PgcryptoBuiltinCrypto, false)
				if err != nil { t.Fatal(err) }
				flags, recognized = store.SettingFlagsLocked(e.Settings, settings.PgcryptoBuiltinCrypto)
				effective, known = store.EffectiveDefinitionLocked(e.Settings, settings.PgcryptoBuiltinCrypto)
				if known != loaded[1] || loaded[1] && !reflect.DeepEqual(d, effective) || !recognized || store.Value(d.Name) != cryptoValue { t.Fatal("pgcrypto effective metadata/value/recognition", d, known, recognized) }
				if loaded[1] {
					if counts[d.Name] != 1 || d.Type != settings.Enum || d.Access != settings.Privileged || d.Flags != 0 || flags != 0 || d.BootDefault != "on" || d.Format(store.DefaultTyped[d.Name]) != "on" || store.TypedValue(d.Name).Text != cryptoValue || !slices.Equal(d.EnumValues, []string{"on", "off", "fips"}) { t.Fatal("pgcrypto enum/reset/context/flags", d, flags) }
				} else if counts[d.Name] != 0 || d.Type != settings.String || flags != settings.PlaceholderFlags { t.Fatal("unloaded pgcrypto lost hidden text placeholder", d, flags) }
				for i, setting := range trigrams {
					d, err = store.LookupDefinition(e.Settings, setting.name, false)
					if err != nil { t.Fatal(err) }
					effective, known = store.EffectiveDefinitionLocked(e.Settings, setting.name)
					flags, recognized = store.SettingFlagsLocked(e.Settings, setting.name)
					if known != loaded[2] || !recognized || store.Value(setting.name) != thresholds[i] { t.Fatal("trgm visibility/current value", setting.name, known, recognized, store.Value(setting.name)) }
					value, err := store.PgTrgmThresholdLocked(e.Settings, setting.name)
					if !loaded[2] {
						if counts[setting.name] != 0 || d.Type != settings.String || flags != settings.PlaceholderFlags || err == nil { t.Fatal("unloaded trgm leaked real metadata/typed authority", d, flags, err) }
						continue
					}
					wantValue, parseErr := strconv.ParseFloat(thresholds[i], 64)
					if parseErr != nil { t.Fatal(parseErr) }
					reset, resetText := setting.boot, setting.bootText
					if i == 1 { reset, resetText = 0.625, "0.625" }
					if !reflect.DeepEqual(d, effective) || counts[d.Name] != 1 || d.Type != settings.Floating || d.Access != settings.Session || d.Unit != settings.UnitNone || d.FloatMin != 0 || d.FloatMax != 1 || flags != 0 || d.Flags != 0 || d.Category != "Customized Options" || d.ExtraDescription != "Valid range is 0.0 .. 1.0." || d.Description != "Sets the threshold used by the "+setting.operator+" operator." { t.Fatal("trgm effective definition", d, effective, flags) }
					if d.BootValue().Number != setting.boot || d.BootDefault != setting.bootText || store.DefaultTyped[d.Name].Number != reset || store.Defaults[d.Name] != resetText || err != nil || value != wantValue || store.TypedValue(d.Name).Number != wantValue || store.Placeholders[d.Name] { t.Fatal("trgm precise boot/reset/current typed consumer", d.Name, d.BootValue().Number, store.DefaultTyped[d.Name], value, err) }
				}
				if store.DefaultSources[trigrams[1].name] != session.DefaultSourceUser { t.Fatal("trgm promotion lost captured reset provenance") }
				if store.Value(unrelated.Name) != "keep" || store.Value(sibling.Name) != "kept" || store.DefaultTyped[sibling.Name].Text != "sibling" { t.Fatal("module projection changed unrelated/registered sibling state") }
				if actual := e.Settings.Definitions()["isn.weak"]; actual.Type != settings.String || actual.BootDefault != "registered" { t.Fatal("module projection mutated frozen registry", actual) }
				if !reflect.DeepEqual(sharedText, e.Settings.SessionDefaults().Text) || !reflect.DeepEqual(sharedTyped, e.Settings.SessionDefaults().Typed) { t.Fatal("module promotion mutated shared reset maps") }
			}
			loaded := [3]bool{}
			check(s.Settings, loaded, "on", "off", baseline)

			// Retain genuine raw settings and exception images before any load.
			s.Settings.NestLevel = 1
			pgcryptoModuleAssign(t, e, op, "isn.weak", "off", session.SettingLocal, setter)
			pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "fips", session.SettingLocal, setter)
			assignTrigrams(local, session.SettingLocal)
			mark := s.Settings.Savepoint()
			before := mark.Clone()
			op.Restricted = &session.RestrictedInvocation{Owner: setter}
			preloadFrame := op.Restricted
			preloadException := op.CaptureInvocation()
			op.Restricted = nil
			pgcryptoModuleAssign(t, e, op, "isn.weak", "on", session.SettingLocal, setter)
			pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "off", session.SettingLocal, setter)
			assignTrigrams(baseline, session.SettingLocal)

			parentFrame := &session.RestrictedInvocation{Owner: setter}
			op.Restricted = parentFrame
			parent := op.EffectiveSettings()
			assignTrigrams(parentValues, session.SettingLocal)
			owner := s.Transactions
			owner.Mu.Lock()
			activeMark := owner.CaptureSavepoint(session.SavepointMark{Name: "combined-modules", Settings: s.Settings.Savepoint(), Invocation: op.CaptureInvocation(), Identity: s.Authorization.State})
			owner.Savepoints = append(owner.Savepoints, activeMark)
			originalMarks := owner.Savepoints
			owner.Mu.Unlock()
			control := op.CaptureInvocation()
			if !reflect.DeepEqual(activeMark.Invocation, control) { t.Fatal("pre-load checkpoint is not independent equivalent capture") }
			op.Restricted = &session.RestrictedInvocation{Owner: setter, Parent: parentFrame}
			child := op.EffectiveSettings()
			assignTrigrams(childValues, session.SettingLocal)
			for _, module := range order {
				switch module {
				case "Isn":
					store := op.EffectiveSettings()
					store.Mu.Lock()
					warning := store.LoadIsnLocked()
					store.Mu.Unlock()
					if warning != nil { t.Fatal(warning) }
					loaded[0] = true
				case "Pgcrypto":
					if notices := pgcryptoModuleInitialize(t, e, s, op, func(_ authorization.PrincipalRef) (bool, error) { return true, nil }, ""); len(notices) != 0 { t.Fatal(notices) }
					loaded[1] = true
					published := owner.Savepoints[0]
					if &owner.Savepoints[0] == &originalMarks[0] || published.Invocation == activeMark.Invocation || published.Serial != activeMark.Serial || published.Name != activeMark.Name { t.Fatal("activation mutated retained owner checkpoint/backing") }
					if !reflect.DeepEqual(activeMark.Invocation, control) { t.Fatal("activation mutated retained raw invocation") }
					op.Restricted = parentFrame
					published.Invocation.PrepareRestore()
					published.Invocation.Restore()
					parent = op.EffectiveSettings()
					op.Restricted = &session.RestrictedInvocation{Owner: setter, Parent: parentFrame}
					child = op.EffectiveSettings()
					assignTrigrams(childValues, session.SettingLocal)
				case "PgTrgm":
					e.Mu.Lock()
					warnings, err := e.ActivatePgTrgmLocked(op)
					e.Mu.Unlock()
					if err != nil || len(warnings) != 0 { t.Fatal("combined trgm activation", warnings, err) }
					loaded[2] = true
					// Inspect active ancestry before any lazy lookup can repair it.
					func() {
						parent.Mu.Lock()
						defer parent.Mu.Unlock()
						for i, setting := range trigrams {
							reset := setting.boot
							if i == 1 { reset = 0.625 }
							if parent.Typed[setting.name].Number != 0.125 || parent.DefaultTyped[setting.name].Number != reset || parent.Placeholders[setting.name] { t.Fatal("active parent current/reset was not eagerly promoted", setting.name, parent.Typed[setting.name], parent.DefaultTyped[setting.name]) }
						}
					}()
				}
				if !reflect.DeepEqual(activeMark.Invocation, control) { t.Fatal("later activation/restoration mutated retained checkpoint ancestry") }
				check(s.Settings, loaded, "on", "off", baseline)
				check(parent, loaded, "on", "off", parentValues)
				check(child, loaded, "on", "off", childValues)
			}
			op.Restricted = nil
			owner.Mu.Lock()
			owner.Savepoints = nil
			owner.Mu.Unlock()
			if peer.Settings.IsnLoaded() || peer.Settings.PgcryptoInitialized() { t.Fatal("module activation leaked to physical peer") }
			if _, found := peer.Setting(settings.PgcryptoBuiltinCrypto); found { t.Fatal("private enum leaked to unloaded peer") }
			if value, _ := peer.Setting("isn.weak"); value != "registered" { t.Fatal("peer lost genuine registry default", value) }
			func() {
				peer.Settings.Mu.Lock()
				defer peer.Settings.Mu.Unlock()
				for _, setting := range trigrams {
					if _, known := peer.Settings.EffectiveDefinitionLocked(e.Settings, setting.name); known { t.Fatal("trgm metadata leaked to peer", setting.name) }
					if _, err := peer.Settings.PgTrgmThresholdLocked(e.Settings, setting.name); err == nil { t.Fatal("peer acquired typed trgm authority") }
				}
			}()

			// Raw pre-load marks restore all typed journals without unloading.
			s.Settings.NestLevel = 2
			pgcryptoModuleAssign(t, e, op, "isn.weak", "on", session.SettingLocal, setter)
			pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "on", session.SettingLocal, setter)
			assignTrigrams(childValues, session.SettingLocal)
			candidate := s.Settings.PrepareSavepointRestore(mark)
			s.Settings.Mu.Lock()
			s.Mu.Lock()
			s.Settings.RestoreSavepointLocked(candidate, s.Authorization.State)
			s.Mu.Unlock()
			s.Settings.Mu.Unlock()
			if !reflect.DeepEqual(mark, before) { t.Fatal("combined restore changed retained raw mark/reset capture") }
			check(s.Settings, loaded, "off", "fips", local)
			s.Settings.FinishTransaction(false)
			check(s.Settings, loaded, "on", "off", baseline)
			op.Restricted = preloadFrame
			preloadException.PrepareRestore()
			preloadException.Restore()
			preloadRestored := op.EffectiveSettings()
			check(preloadRestored, loaded, "off", "fips", local)
			op.Restricted = nil

			op.Restricted = &session.RestrictedInvocation{Owner: setter}
			image := op.EffectiveSettings()
			if image == s.Settings { t.Fatal("detached clone reused physical settings") }
			pgcryptoModuleAssign(t, e, op, "isn.weak", "off", session.SettingSave, setter)
			pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "on", session.SettingSave, setter)
			saved := [3]string{"0.375", "0.375", "0.375"}
			assignTrigrams(saved, session.SettingSave)
			exception := op.CaptureInvocation()
			pgcryptoModuleAssign(t, e, op, "isn.weak", "on", session.SettingLocal, setter)
			pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "fips", session.SettingLocal, setter)
			assignTrigrams(childValues, session.SettingLocal)
			exception.PrepareRestore()
			assignTrigrams(local, session.SettingLocal)
			exception.Restore()
			restored := op.EffectiveSettings()
			if restored == image { t.Fatal("prepared exception reused active image") }
			check(restored, loaded, "off", "on", saved)
			op.Restricted = nil
			check(s.Settings, loaded, "on", "off", baseline)
			if err := s.Settings.EndSession(s.SessionToken, session.CleanClose); err != nil { t.Fatal(err) }
			for _, store := range []*session.SettingsStore{s.Settings, parent, child, preloadRestored, image, restored} {
				func() {
					store.Mu.Lock()
					defer store.Mu.Unlock()
					if store.IsnLoaded() || store.PgcryptoInitialized() { t.Fatal("physical end retained ISN/pgcrypto authority") }
					s.Mu.Lock()
					defer s.Mu.Unlock()
					for _, setting := range trigrams {
						if _, err := store.PgTrgmThresholdLocked(e.Settings, setting.name); err == nil { t.Fatal("physical end retained trgm authority", setting.name) }
					}
				}()
			}
			if s.Settings.DefaultTyped != nil { t.Fatal("physical end retained typed reset image") }
		})
	}

	for _, setting := range trigrams {
		t.Run("RegisteredTrgmCollision/"+setting.name, func(t *testing.T) {
			collision, err := settings.Validate(settings.Definition{Name: setting.name, Type: settings.String, Access: settings.Session, BootDefault: "registered threshold"})
			if err != nil { t.Fatal(err) }
			e := rootengine.NewWithSettings([]settings.Definition{collision}, "", false)
			s := enginetest.StartSession(t, e, nil)
			op, err := s.Acquire(t.Context())
			if err != nil { t.Fatal(err) }
			defer op.Done()
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			e.Mu.Unlock()
			if err != nil { t.Fatal(err) }
			pgcryptoModuleAssign(t, e, op, settings.PgcryptoBuiltinCrypto, "off", session.SettingSet, auth.State.CurrentUser)
			s.Settings.FinishTransaction(true)
			s.Settings.Mu.Lock()
			warning := s.Settings.LoadIsnLocked()
			s.Settings.Mu.Unlock()
			if warning != nil { t.Fatal(warning) }
			if notices := pgcryptoModuleInitialize(t, e, s, op, func(authorization.PrincipalRef) (bool, error) { return true, nil }, ""); len(notices) != 0 { t.Fatal(notices) }
			before := s.Settings.Savepoint()
			s.Settings.Mu.Lock()
			defaults := maps.Clone(s.Settings.DefaultTyped)
			names := s.Settings.EffectiveSettingNamesLocked(e.Settings)
			s.Settings.Mu.Unlock()
			e.Mu.Lock()
			warnings, err := e.ActivatePgTrgmLocked(op)
			e.Mu.Unlock()
			var diagnostic *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: `attempt to redefine parameter "`+setting.name+`"`}
			if len(warnings) != 0 || !errors.As(err, &diagnostic) || *diagnostic != want { t.Fatal("registered trgm collision diagnostic", warnings, err) }
			if !reflect.DeepEqual(before, s.Settings.Savepoint()) { t.Fatal("failed collision changed settings/journals") }
			s.Settings.Mu.Lock()
			defer s.Settings.Mu.Unlock()
			s.Mu.Lock()
			defer s.Mu.Unlock()
			if !reflect.DeepEqual(defaults, s.Settings.DefaultTyped) || !slices.Equal(names, s.Settings.EffectiveSettingNamesLocked(e.Settings)) || !s.Settings.IsnLoaded() || !s.Settings.PgcryptoInitialized() || s.Settings.Value(settings.PgcryptoBuiltinCrypto) != "off" { t.Fatal("collision changed another module or reset metadata") }
			d, known := s.Settings.EffectiveDefinitionLocked(e.Settings, setting.name)
			if !known || d.Type != settings.String || d.BootDefault != "registered threshold" { t.Fatal("collision replaced genuine registry metadata", d, known) }
			for _, other := range trigrams {
				if _, err := s.Settings.PgTrgmThresholdLocked(e.Settings, other.name); err == nil { t.Fatal("failed collision published trgm authority") }
				if other.name != setting.name {
					if _, known := s.Settings.EffectiveDefinitionLocked(e.Settings, other.name); known { t.Fatal("collision partially published trgm definitions", other.name) }
				}
			}
		})
	}
}
