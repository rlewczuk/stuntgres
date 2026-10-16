//go:build !noservertests

package reference_test

import (
	"encoding/binary"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerNegotiation(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverDial(t, f.DSNFor(b))
			frames := w.login(t, f.DSNFor(b), 196611)
			serverWantCode(t, frames, "")
			want := uint32(196610)
			if len(frames) == 0 || frames[0].tag != 'v' || len(frames[0].body) != 8 || binary.BigEndian.Uint32(frames[0].body) != want || binary.BigEndian.Uint32(frames[0].body[4:]) != 0 {
				t.Fatalf("negotiated version must encode major and minor: %s", serverFrameSummary(frames))
			}
			serverScalar(t, w, "SELECT 42", "42")
		})
	}
}
