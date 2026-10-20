package engine

import (
	"math"
	"stuntgres/internal/engine/settings"
	"testing"
)

func TestSavepointsSettings(t *testing.T) {
	d, err := settings.Validate(settings.Definition{Name: "demo.ratio", Type: settings.Floating, BootDefault: "0", FloatMin: -1, FloatMax: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := NewWithSettings([]settings.Definition{d}, "", false)
	provider := &settingsDefaultProviderForTest{values: map[string]string{"demo.ratio": "0"}, calls: map[string]int{}}
	a := sessionStart(t, e, provider)
	sessionSQL(t, a, "SET demo.ratio='0.123456789'")
	original := math.Float64bits(a.Settings.Typed["demo.ratio"].Number)
	controlsRequire(t, a, "BEGIN")
	sessionSQL(t, a, "SET SESSION demo.ratio='0.234567891'")
	candidate := math.Float64bits(a.Settings.Typed["demo.ratio"].Number)
	sessionSQL(t, a, "SET LOCAL demo.ratio='0.345678912'")
	before := math.Float64bits(a.Settings.Typed["demo.ratio"].Number)
	controlsRequire(t, a, "SAVEPOINT s")
	sessionSQL(t, a, "SET demo.ratio='0.456789123'")
	calls := provider.calls["demo.ratio"]
	for range 2 {
		controlsRequire(t, a, "ROLLBACK TO s")
		if math.Float64bits(a.Settings.Typed["demo.ratio"].Number) != before || provider.calls["demo.ratio"] != calls {
			t.Fatal("restore reparsed typed text or called provider")
		}
		sessionSQL(t, a, "SET LOCAL demo.ratio='0.567891234'")
	}
	controlsRequire(t, a, "ROLLBACK TO s")
	controlsRequire(t, a, "COMMIT")
	if math.Float64bits(a.Settings.Typed["demo.ratio"].Number) != candidate {
		t.Fatal("saved SESSION candidate lost")
	}
	controlsRequire(t, a, "BEGIN")
	sessionSQL(t, a, "SET demo.ratio='0.123456789'")
	controlsRequire(t, a, "SAVEPOINT s")
	sessionSQL(t, a, "SET demo.ratio='0.678912345'")
	controlsRequire(t, a, "ROLLBACK TO s")
	if math.Float64bits(a.Settings.Typed["demo.ratio"].Number) != original {
		t.Fatal("mark precision lost")
	}
	controlsRequire(t, a, "ROLLBACK")
	if math.Float64bits(a.Settings.Typed["demo.ratio"].Number) != candidate {
		t.Fatal("outer original journal corrupted")
	}
}
