package core

import "stuntgres/internal/engine/exec"

// Borrow exact records already attached to the actual command presentations.
// This test-only seam neither reserves records nor constructs another binding.
func AutoExplainDMLRecordsForTest(scope *StatementScope) (automatic, explicit []*exec.DMLObservation, rootsShared bool) {
	rootsShared = true
	if frame := scope.autoExplain; frame != nil {
		for _, renderer := range frame.renderers {
			if renderer.DML != nil {
				automatic = append(automatic, renderer.DML)
				rootsShared = rootsShared && renderer.CommandTree().Actual == renderer.DML.CommandActual()
			}
		}
	}
	if owner := scope.explain; owner != nil {
		for _, renderer := range owner.renderers {
			if renderer.DML != nil {
				explicit = append(explicit, renderer.DML)
				rootsShared = rootsShared && renderer.CommandTree().Actual == renderer.DML.CommandActual()
			}
		}
	}
	return
}
