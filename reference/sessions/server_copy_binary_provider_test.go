//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Binary output needs an actual send provider even when no row is demanded.
// PostgreSQL has already sent CopyOutResponse, but not binary stream data.
func TestReferenceServerCopyBinaryProvider(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(b))
					source := "COPY (SELECT NULL::aclitem WHERE FALSE) TO STDOUT (FORMAT binary)"
					if prepared {
						serverWantTags(t, serverCycle(t, w, serverParse("cp", source)), "1Z")
						source = "EXECUTE cp"
					}
					frames := w.query(t, source)
					serverWantTags(t, frames, "HEZ")
					if string(frames[0].body) != string([]byte{1, 0, 1, 0, 1}) {
						t.Fatalf("binary one-column response %v", frames[0].body)
					}
					serverNotificationError(t, frames, "42883", "no binary output function available for type aclitem", "")
					serverWantCompletions(t, frames)
					serverWantReady(t, frames, 'I')
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
		})
	}
}
