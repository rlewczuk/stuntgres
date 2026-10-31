package reference_test

import "testing"

// The shared control scenario includes all fourteen decoded fields, flag arrays,
// strict cardinality, raw-page diagnostics, live authority and native replay.
func TestReferencePageinspectHeapFeatureSmoke(t *testing.T) { heapControls(t, true) }
