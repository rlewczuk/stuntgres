//go:build !productionkdf

package fixture

// Test-built servers use a reduced PBKDF2 cost so each login stays cheap.
// Build tests with -tags productionkdf to exercise the production cost.
const (
	serverKDFTag        = "stuntgres_reduced_kdf"
	serverKDFIterations = 4096
)
