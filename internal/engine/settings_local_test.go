package engine

import (
	"math"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
)

func TestSettingsLocalActions(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, tc := range []struct {
			actions string
			want    string
		}{
			{"S", "s"}, {"L", "b"}, {"SL", "s"}, {"LS", "s"}, {"SLSL", "s"}, {"SLD", "s"}, {"SLR", ""}, {"SDR", "s"},
		} {
			s := &sessionSettings{Values: map[string]string{"x": "b"}, Typed: map[string]settings.Value{"x": {Number: math.SmallestNonzeroFloat64}}}
			for _, action := range tc.actions {
				switch action {
				case 'S':
					s.Touch("x", false)
					s.Restore("x", settingOverride{Present: true, Text: "s", Typed: settings.Value{Number: 2}})
				case 'L':
					s.Touch("x", true)
					s.Restore("x", settingOverride{Present: true, Text: "l", Typed: settings.Value{Number: 3}})
				case 'D':
					s.Touch("x", true)
					s.Restore("x", settingOverride{})
				case 'R':
					if _, ok := s.Values["x"]; ok {
						s.Touch("x", false)
						s.Restore("x", settingOverride{})
					}
				}
				if len(s.Actions) != 1 {
					t.Fatal("unbounded action map", s.Actions)
				}
			}
			s.FinishTransaction(commit)
			want := tc.want
			if !commit {
				want = "b"
			}
			if s.Values["x"] != want || s.Actions != nil {
				t.Fatalf("%s commit=%v got=%q want=%q", tc.actions, commit, s.Values["x"], want)
			}
			if !commit && s.Typed["x"].Number != math.SmallestNonzeroFloat64 {
				t.Fatal("typed original lost")
			}
			s.FinishTransaction(commit)
		}
	}
}

func TestSettingsLocalEndedCleanup(t *testing.T) {
	s := &sessionSettings{Values: map[string]string{"x": "b"}, Typed: map[string]settings.Value{}}
	s.Touch("x", true)
	s.EndSession(ids.SessionToken{}, 0)
	s.FinishTransaction(false)
	if s.Values != nil || s.Typed != nil || s.Actions != nil {
		t.Fatal("ended owner recreated")
	}
}

func TestSettingsLocalUntouchedTransaction(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	ref := transactionBegin(t, s)
	if s.Settings.Actions != nil {
		t.Fatal("Begin allocated actions")
	}
	r := sessionSQL(t, s, "SHOW application_name")
	r.Rows.Close()
	if s.Settings.Actions != nil {
		t.Fatal("read allocated actions")
	}
	transactionEnd(t, s, ref, true)
	if s.Settings.Actions != nil {
		t.Fatal("end retained actions")
	}
}
