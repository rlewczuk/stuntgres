//go:build !stuntgres_reduced_kdf

package serverauth

// passwordIterations is the PBKDF2-SHA256 cost of every userfile verifier.
// Production builds always use this value; see password_cost_reduced.go.
const passwordIterations = 600000
