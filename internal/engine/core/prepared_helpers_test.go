package core_test

import (
	"testing"

	"stuntgres/internal/engine/core"
)


func preparedRegistryEmpty(registry *core.PreparedRegistry) bool {
	for range registry.Definitions() {
		return false
	}
	return true
}

// The caller holds e.Mu, as required by production environment capture.
func preparedUnitEnvironment(t *testing.T, e *core.Engine, auth core.AuthorizationView) core.PreparedEnvironment {
	t.Helper()
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil { t.Fatal(err) }
	return environment
}
