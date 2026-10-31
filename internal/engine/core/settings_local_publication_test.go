package core_test

import (
	"math"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/settings"
)

func TestSettingsLocalExactTypedUndo(t *testing.T) {
	// Exact hexadecimal subnormals are accepted; rounded decimal ones are not.
	d, err := settings.Validate(settings.Definition{Name: "demo.exact", Type: settings.Floating, BootDefault: "0x1p-1023", FloatMax: 10})
	if err != nil {
		t.Fatal(err)
	}
	e := engine.NewWithSettings([]settings.Definition{d}, "", false)
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "SET demo.exact='0x1p-1074'")
	original := math.Float64bits(s.Settings.Typed["demo.exact"].Number)
	if original != math.Float64bits(math.SmallestNonzeroFloat64) {
		t.Fatal(original)
	}
	for _, commit := range []bool{false, true} {
		ref := enginetest.TransactionBegin(t, s)
		enginetest.SessionSQL(t, s, "SET LOCAL demo.exact='2'")
		enginetest.TransactionEnd(t, s, ref, commit)
		if math.Float64bits(s.Settings.Typed["demo.exact"].Number) != original {
			t.Fatal("undo reparsed lossy SHOW text")
		}
	}
	ref := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "SET demo.exact='0x1p-1023'")
	candidate := math.Float64bits(s.Settings.Typed["demo.exact"].Number)
	if candidate != math.Float64bits(0x1p-1023) {
		t.Fatal("SESSION candidate was not exact")
	}
	if got := enginetest.Tuples(enginetest.SessionSQL(t, s, "SHOW demo.exact"))[0][0]; got != "1.11254e-308" {
		t.Fatal("expected independently known lossy display", got)
	}
	enginetest.SessionSQL(t, s, "SET LOCAL demo.exact='2'")
	enginetest.TransactionEnd(t, s, ref, true)
	if math.Float64bits(s.Settings.Typed["demo.exact"].Number) != candidate {
		t.Fatal("masked candidate lost typed bits")
	}
}
