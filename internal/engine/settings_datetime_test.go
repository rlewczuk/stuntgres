package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestSettingsTimezoneAbbreviationsCapturedProvider(t *testing.T) {
	e := New()
	provider := &settingsDefaultProviderForTest{values: map[string]string{"timezone_abbreviations": "India"}, calls: map[string]int{}}
	a := sessionStart(t, e, provider)
	check := func(s *session.Session, want sqltype.TimezoneAbbreviations) {
		t.Helper()
		input, _ := s.Settings.Presentation()
		s.Settings.Mu.Lock()
		text := s.Settings.Value("timezone_abbreviations")
		s.Settings.Mu.Unlock()
		if input.TimezoneAbbreviations != want || text != want.String() {
			t.Fatal(input.TimezoneAbbreviations, text, want)
		}
	}
	check(a, sqltype.TimezoneAbbreviationsIndia)
	provider.values["timezone_abbreviations"] = "Australia"
	for _, q := range []string{"RESET timezone_abbreviations", "SET timezone_abbreviations=DEFAULT", "RESET ALL", "SELECT set_config('timezone_abbreviations',NULL,false)"} {
		sessionSQL(t, a, "SET timezone_abbreviations='Default'")
		sessionSQL(t, a, q)
		check(a, sqltype.TimezoneAbbreviationsIndia)
	}
	if provider.calls["timezone_abbreviations"] != 1 {
		t.Fatal(provider.calls)
	}
	b := sessionStart(t, e, provider)
	check(b, sqltype.TimezoneAbbreviationsAustralia)
	check(a, sqltype.TimezoneAbbreviationsIndia)
	for _, q := range []string{"BEGIN", "SET LOCAL timezone_abbreviations='Australia'", "SAVEPOINT mark", "SET timezone_abbreviations='Default'", "ROLLBACK TO mark"} {
		sessionSQL(t, a, q)
	}
	check(a, sqltype.TimezoneAbbreviationsAustralia)
	sessionSQL(t, a, "COMMIT")
	check(a, sqltype.TimezoneAbbreviationsIndia)
	def := e.Settings.Definitions()["timezone_abbreviations"]
	if _, err := def.Parse("default"); err == nil {
		t.Fatal("invalid exact-case name accepted")
	}
	bad := &settingsDefaultProviderForTest{values: map[string]string{"timezone_abbreviations": "default"}, calls: map[string]int{}}
	if s, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Defaults: bad}); err == nil || s != nil {
		t.Fatal("invalid provider default published", s, err)
	}
}
