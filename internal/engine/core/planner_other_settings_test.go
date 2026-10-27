package core_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
)

func TestPlannerOtherSettingsDefinitions(t *testing.T) {
	e := engine.New()
	values := map[string]string{"default_statistics_target": "2", "constraint_exclusion": "on", "cursor_tuple_fraction": "0.123456789012345", "from_collapse_limit": "2", "jit": "off", "join_collapse_limit": "2", "plan_cache_mode": "force_generic_plan", "recursive_worktable_factor": "2.123456789012345"}
	provider := &settingsDefaultProviderForTest{values: values, calls: map[string]int{}}
	s := enginetest.StartSession(t, e, provider)
	captured := s.Settings.DefaultTyped
	for name := range values {
		provider.values[name] = "provider changed after creation"
	}
	for _, q := range []string{"SET default_statistics_target=3", "SET constraint_exclusion=off", "SET cursor_tuple_fraction='0.5'", "SET from_collapse_limit=3", "SET jit=on", "SET join_collapse_limit=3", "SET plan_cache_mode=force_custom_plan", "SET recursive_worktable_factor=3", "BEGIN", "SET LOCAL default_statistics_target=4", "ROLLBACK", "RESET ALL"} {
		plannerCostCommand(t, s, q)
	}
	for name := range values {
		if provider.calls[name] != 1 || !reflect.DeepEqual(s.Settings.TypedValue(name), captured[name]) {
			t.Fatal("reset did not restore captured typed default", name, provider.calls[name], s.Settings.TypedValue(name), captured[name])
		}
	}
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if op.StatisticsTarget != 2 || op.ConstraintExclusion != "on" || op.CursorTupleFraction != .123456789012345 || op.RecursiveWorktableFactor != 2.123456789012345 {
		t.Fatal("admission lost typed inputs", op)
	}
	// Scalar ownership persists even when the session's next value changes.
	nextTarget := captured["default_statistics_target"]
	nextTarget.Number = 9
	s.Settings.Mu.Lock()
	s.Settings.Typed["default_statistics_target"] = nextTarget
	s.Settings.Values["constraint_exclusion"] = "off"
	s.Settings.Mu.Unlock()
	if op.ConstraintExclusion != "on" || op.StatisticsTarget != 2 {
		t.Fatal("operation borrowed settings")
	}
	op.Done()
}
