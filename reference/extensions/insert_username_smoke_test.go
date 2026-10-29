package reference_test

import "testing"

// The smoke uses the complete shared role/binding/control exercise, including
// real SQL effects before the explicit native Dump/Reset/Restore phase.
func TestReferenceInsertUsernameFeatureSmoke(t *testing.T) {
	insertUsernameCase(t, insertUsernameControls)
}
