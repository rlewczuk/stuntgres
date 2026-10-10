package reference_test

import "testing"

// Smoke reuses the actual scenario definitions, not a second implementation or
// a printing-only readiness check.
func TestReferenceRegtypeFeatureSmoke(t *testing.T) {
	for _, scenario := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"input-and-numeric", TestReferenceRegtypeInput},
		{"shadow-and-quoted-output", TestReferenceRegtypeNames},
		{"lookup-only-inventory", TestReferenceRegtypeReleaseIdentities},
		{"initialized-row-identities", TestReferenceRegtypeInitializedNames},
		{"rows-and-currentdb", TestReferenceRegtypeRowsAndCurrentDatabase},
		{"percent-and-diagnostics", TestReferenceRegtypeDiagnostics},
		{"held-valid-error-null-recovery", TestReferenceRegtypeHeldFrames},
		{"held-literal-path-drop-recreate", TestReferenceRegtypeHeldLiterals},
		{"typmod-warning-timing", TestReferenceRegtypeWarnings},
		{"persistent-values-and-metadata", TestReferenceRegtypeValues},
		{"native-controls-and-emitted-replay", TestReferenceRegtypeNativeControls},
		{"selected-output-and-assignment", TestReferenceRegtypeOutputAuthority},
		{"output-atomicity", TestReferenceRegtypeOutputAuthorityAtomicity},
		{"held-selected-output", TestReferenceRegtypeOutputAuthorityHeld},
		{"output-native-controls-and-replay", TestReferenceRegtypeOutputAuthorityNativeControls},
		{"alter-assignment-output", TestReferenceRegtypeOutputAuthorityAlter},
		{"generated-assignment-and-explicit-io-boundary", TestReferenceRegtypeOutputAuthorityGenerated},
		{"unrelated-role-literal-retention", TestReferenceRegtypeUnrelatedRoles},
		{"qualified-namespace-authority", TestReferenceRegtypeUnrelatedRolesQualifiedAuthority},
		{"associated-array-displacement", TestReferenceRegtypeArrayDisplacement},
		{"array-name-collisions", TestReferenceRegtypeArrayDisplacementCollisions},
		{"array-displacement-atomicity", TestReferenceRegtypeArrayDisplacementAtomicity},
		{"array-native-controls-and-replay", TestReferenceRegtypeArrayDisplacementControls},
		{"array-executable-consumers", TestReferenceRegtypeArrayDisplacementExecutable},
		{"mixed-creator-native-and-pg-replay", TestReferenceRegtypeArrayDisplacementMixedReplay},
		{"named-array-domain-declaration-and-replay", TestReferenceRegtypeArrayDisplacementNamedDomainReplay},
	} {
		t.Run(scenario.name, scenario.run)
	}
}
