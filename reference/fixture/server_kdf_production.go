//go:build productionkdf

package fixture

// The production-cost server build: no reduced-KDF tag.
const (
	serverKDFTag        = ""
	serverKDFIterations = 600000
)
