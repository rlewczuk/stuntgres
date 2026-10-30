package session

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/settings"
)

type selfGrantDefault struct{ calls int }

func (p *selfGrantDefault) SessionDefault(_ context.Context, _ settings.Definition) (string, bool, error) {
	p.calls++
	return `"INHERIT"`, true, nil
}

func TestCreateroleSelfGrantStoreOwnership(t *testing.T) {
	const name = "createrole_self_grant"
	d := settings.NewRegistry(nil, false).Definitions()[name]
	provider := &selfGrantDefault{}
	var s SettingsStore
	if err := s.Initialize(t.Context(), map[string]settings.Definition{name: d}, []string{name}, provider); err != nil {
		t.Fatal(err)
	}
	s.Values = make(map[string]string)
	captured := settings.Value{Text: `"INHERIT"`, SelfGrant: settings.SelfGrantInherit}
	check := func(want settings.Value) {
		t.Helper()
		if !reflect.DeepEqual(s.TypedValue(name), want) || s.Value(name) != want.Text || provider.calls != 1 {
			t.Fatal("typed/text/captured provider mismatch", s.TypedValue(name), s.Value(name), provider.calls, want)
		}
	}
	check(captured)
	for _, text := range []string{"", "set", "inherit,set", `"SeT"`, "   "} {
		got, notice, err := s.ParseValue(d, text)
		want, parseErr := d.Parse(text)
		if err != nil || parseErr != nil || notice != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("live/default paths differ", got, want, notice, err, parseErr)
		}
	}
	set, _, err := s.ParseValue(d, "SET")
	if err != nil {
		t.Fatal(err)
	}
	s.Touch(name, SettingSet)
	s.Restore(name, settingOverride{Present: true, Text: set.Text, Typed: set})
	mark := s.Savepoint()
	op := &Operation{Session: &Session{Settings: &s}, Restricted: &RestrictedInvocation{}}
	image := op.EffectiveSettings()
	both, _, err := s.ParseValue(d, "inherit,set")
	if err != nil {
		t.Fatal(err)
	}
	s.Touch(name, SettingLocal)
	s.Restore(name, settingOverride{Present: true, Text: both.Text, Typed: both})
	if image.TypedValue(name).SelfGrant != settings.SelfGrantSet || image.Value(name) != "SET" {
		t.Fatal("detached invocation image changed")
	}
	before := s.override(name)
	if _, _, err := s.ParseValue(d, "bogus,SET,"); err == nil || !reflect.DeepEqual(before, s.override(name)) {
		t.Fatal("invalid parse installed a value", err)
	}
	s.RestoreSavepointLocked(mark, authorization.RoleState{})
	check(set)
	s.FinishTransaction(false)
	check(captured)
	for _, local := range []bool{false, true} {
		s.Touch(name, SettingAssignment(local))
		s.Restore(name, settingOverride{Present: true, Text: both.Text, Typed: both})
		s.FinishTransaction(true)
		if local {
			check(captured)
		} else {
			check(both)
		}
		// Both RESET and RESET ALL use Restore with an absent override.
		s.Restore(name, settingOverride{})
		check(captured)
	}
}
