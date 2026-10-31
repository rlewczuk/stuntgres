package reference_test

import "testing"

// Smoke definitions intentionally reuse the same public, independently asserted
// workload as their feature family; they never substitute parser-only probes.
func TestReferenceClusterSmokeReorganization(t *testing.T) {
	clusterCommandReorganizationScenario(t)
}

func TestReferenceClusterSmokeDatabase(t *testing.T) {
	clusterCommandDatabaseScenario(t)
}

func TestReferenceClusterSmokeCancellation(t *testing.T) {
	clusterCommandCancellationScenario(t)
}
