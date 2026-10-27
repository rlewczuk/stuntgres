package session

import "fmt"

// PlanGenerationHeadroom is called under physical-owner admission before any
// generation consumer. Settings installers also hold their effective settings
// mutex. Suspended physical/parent SAVE frames remain obligations even while a
// detached invocation image is effective. No callback or allocation is needed.
func (s *Session) PlanGenerationHeadroom(op *Operation, immediate uint64) error {
	if s == nil {
		return nil
	}
	needed := uint64(2) // immediate transition and infallible transaction restoration
	if immediate > needed {
		needed = immediate
	}
	add := func(n uint64) bool {
		if ^uint64(0)-needed < n {
			return false
		}
		needed += n
		return true
	}
	if s.Settings != nil && s.Settings.NestLevel > 0 && !add(uint64(s.Settings.NestLevel)) {
		return fmt.Errorf("stuntgres: discard plan generation exhausted")
	}
	if s.Transactions != nil && !add(uint64(len(s.Transactions.Savepoints))) {
		return fmt.Errorf("stuntgres: discard plan generation exhausted")
	}
	if op != nil {
		for frame := op.Restricted; frame != nil; frame = frame.Parent {
			if !add(1) {
				return fmt.Errorf("stuntgres: discard plan generation exhausted")
			}
			if frame.settings != nil && frame.settings.NestLevel > 0 && !add(uint64(frame.settings.NestLevel)) {
				return fmt.Errorf("stuntgres: discard plan generation exhausted")
			}
		}
	}
	if ^uint64(0)-s.DiscardPlansGeneration < needed {
		return fmt.Errorf("stuntgres: discard plan generation exhausted")
	}
	return nil
}

// RuleRoleChanged is infallible: every mutating installer reserves capacity for
// itself and its pending restoration boundaries before changing any state.
// Owner admission serializes this field; callers hold the effective store mutex.
func (s *SettingsStore) RuleRoleChanged(before string) {
	if s.Session != nil && before != s.TypedValue("session_replication_role").Text {
		s.Session.DiscardPlansGeneration++
	}
}
