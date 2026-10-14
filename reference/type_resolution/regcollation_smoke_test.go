package reference_test

import "testing"

// Reuse consumer-visible contracts rather than maintaining separate smoke SQL.
func TestReferenceRegCollationFeatureSmoke(t *testing.T) {
	for _, scenario := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"collation-input", TestReferenceRegCollationInput},
		{"namespace-input", TestReferenceRegNamespaceInput},
		{"visibility-and-authorization", TestReferenceRegCollationVisibility},
		{"collation-diagnostics", TestReferenceRegCollationDiagnostics},
		{"namespace-diagnostics", TestReferenceRegNamespaceDiagnostics},
		{"prepared-and-retained-output", TestReferenceRegCollationPrepared},
		{"stored-and-native-controls", TestReferenceRegCollationStoredControls},
		{"opaque-array-dependencies", TestReferenceRegCollationArrayDependencies},
		{"mixed-routine-persistence", TestReferenceRegCollationMixedRoutinePersistence},
		{"string-category-boundaries", TestReferenceRegObjectBoundaryStringCasts},
		{"byte-parameter-boundaries", TestReferenceRegObjectBoundaryByteParameters},
	} {
		t.Run(scenario.name, scenario.run)
	}
}
