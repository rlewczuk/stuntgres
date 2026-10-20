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
			s := &sessionSettings{values: map[string]string{"x": "b"}, typed: map[string]settings.Value{"x": {Number: math.SmallestNonzeroFloat64}}}
			for _, action := range tc.actions {
				switch action {
				case 'S':
					s.touch("x", false)
					s.restore("x", settingOverride{present: true, text: "s", typed: settings.Value{Number: 2}})
				case 'L':
					s.touch("x", true)
					s.restore("x", settingOverride{present: true, text: "l", typed: settings.Value{Number: 3}})
				case 'D':
					s.touch("x", true)
					s.restore("x", settingOverride{})
				case 'R':
					if _, ok := s.values["x"]; ok {
						s.touch("x", false)
						s.restore("x", settingOverride{})
					}
				}
				if len(s.actions) != 1 {
					t.Fatal("unbounded action map", s.actions)
				}
			}
			s.finishTransaction(commit)
			want := tc.want
			if !commit {
				want = "b"
			}
			if s.values["x"] != want || s.actions != nil {
				t.Fatalf("%s commit=%v got=%q want=%q", tc.actions, commit, s.values["x"], want)
			}
			if !commit && s.typed["x"].Number != math.SmallestNonzeroFloat64 {
				t.Fatal("typed original lost")
			}
			s.finishTransaction(commit)
		}
	}
}

func TestSettingsLocalEndedCleanup(t *testing.T) {
	s := &sessionSettings{values: map[string]string{"x": "b"}, typed: map[string]settings.Value{}}
	s.touch("x", true)
	s.EndSession(ids.SessionToken{}, 0)
	s.finishTransaction(false)
	if s.values != nil || s.typed != nil || s.actions != nil {
		t.Fatal("ended owner recreated")
	}
}

func TestSettingsLocalUntouchedTransaction(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	ref := transactionBegin(t, s)
	if s.Settings.actions != nil {
		t.Fatal("Begin allocated actions")
	}
	r := sessionSQL(t, s, "SHOW application_name")
	r.Rows.Close()
	if s.Settings.actions != nil {
		t.Fatal("read allocated actions")
	}
	transactionEnd(t, s, ref, true)
	if s.Settings.actions != nil {
		t.Fatal("end retained actions")
	}
}
