package enginetests

import (
	"testing"

	"stuntgres/internal/engine/settings"
)

func TestPlannerOtherSettingsDefinitions(t *testing.T) {
	r := settings.NewRegistry(nil, false)
	for _, tc := range []struct {
		name, boot string
		kind       settings.Type
	}{{"default_statistics_target", "100", settings.Integer}, {"constraint_exclusion", "partition", settings.Enum}, {"cursor_tuple_fraction", "0.1", settings.Floating}, {"from_collapse_limit", "8", settings.Integer}, {"jit", "on", settings.Boolean}, {"join_collapse_limit", "8", settings.Integer}, {"plan_cache_mode", "auto", settings.Enum}, {"recursive_worktable_factor", "10", settings.Floating}} {
		d, err := r.Lookup(tc.name)
		if err != nil || d.Type != tc.kind || d.Access != settings.Session || d.Category != "Query Tuning / Other Planner Options" || d.BootDefault != tc.boot || d.Unit != settings.UnitNone {
			t.Fatal(tc, d, err)
		}
		v, err := d.Parse(tc.boot)
		if err != nil || d.Format(v) != tc.boot {
			t.Fatal(tc, v, err)
		}
	}
	d, _ := r.Lookup("constraint_exclusion")
	for input, want := range map[string]string{"YES": "on", "true": "on", "1": "on", "NO": "off", "false": "off", "0": "off", "PARTITION": "partition"} {
		v, err := d.Parse(input)
		if err != nil || d.Format(v) != want {
			t.Fatal(input, v, err)
		}
	}
	if len(d.EnumValues) != 3 {
		t.Fatal("hidden aliases leaked", d.EnumValues)
	}
}
