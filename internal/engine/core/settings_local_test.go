package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestSettingsLocalUntouchedTransaction(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	ref := enginetest.TransactionBegin(t, s)
	if s.Settings.Actions != nil {
		t.Fatal("Begin allocated actions")
	}
	r := enginetest.SessionSQL(t, s, "SHOW application_name")
	r.Rows.Close()
	if s.Settings.Actions != nil {
		t.Fatal("read allocated actions")
	}
	enginetest.TransactionEnd(t, s, ref, true)
	if s.Settings.Actions != nil {
		t.Fatal("end retained actions")
	}
}
