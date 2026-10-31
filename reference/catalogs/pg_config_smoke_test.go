package reference_test

import "testing"

func TestReferencePGConfigFeatureSmoke(t *testing.T) {
	// Reuse the permanent public scenarios, with a separate bounded fixture for
	// each phase. The smoke proves values, empty metadata, analysis diagnostics,
	// both current authority gates, and native replacement rather than wiring.
	t.Run("values", TestReferencePGConfigValues)
	t.Run("metadata", TestReferencePGConfigMetadata)
	t.Run("diagnostics", TestReferencePGConfigDiagnostics)
	t.Run("privileges", TestReferencePGConfigPrivileges)
	t.Run("lifecycle", TestReferencePGConfigLifecycle)
}
