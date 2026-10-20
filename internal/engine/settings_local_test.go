package engine

import "testing"

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
