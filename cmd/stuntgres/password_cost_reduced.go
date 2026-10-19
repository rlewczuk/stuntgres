//go:build stuntgres_reduced_kdf

package main

// passwordIterations is lowered only for test-built servers. The reference
// server fixture builds with this tag so that each login does not spend a
// production-cost PBKDF2. Never set this tag for a distributed binary: such a
// server writes and accepts only these weaker verifiers.
const passwordIterations = 4096
