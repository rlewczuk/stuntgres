//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerFunctionCallReceivePrecedence(t *testing.T) {
	f := fixture.NewServerFixture(t)
	negative := serverPut32(nil, 1397)
	negative = server16(negative, 0)
	negative = server16(negative, 1)
	negative = serverPut32(negative, ^uint32(1)) // signed argument length -2
	negative = server16(negative, 0)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, explicit := range []bool{false, true} {
				suffix := "implicit"
				if explicit {
					suffix = "explicit"
				}
				for _, c := range []struct {
					name, message string
					packet        []byte
				}{
					{"arity_before_selector_count", "function call message contains 2 arguments but function requires 1", serverFunction(1397, []int{0, 0, 0}, [][]byte{[]byte("-42"), []byte("9")}, 0)},
					{"selector_count_template", "function call message contains 2 argument formats but 1 arguments", serverFunction(1397, []int{0, 0}, [][]byte{[]byte("-42")}, 0)},
					{"signed_argument_size", "invalid argument size -2 in function call message", serverPacket('F', negative)},
				} {
					t.Run(c.name+"/"+suffix, func(t *testing.T) {
						w := serverAuthenticated(t, f.DSNFor(b))
						if explicit {
							serverWantCode(t, w.query(t, "BEGIN"), "")
						}
						w.send(t, c.packet)
						frames := w.ready(t)
						serverWantTags(t, frames, "EZ")
						serverNotificationError(t, frames, "08P01", c.message, "")
						serverWantCompletions(t, frames)
						state := byte('I')
						if explicit {
							state = 'E'
						}
						serverWantReady(t, frames, state)
						if explicit {
							// F completes its own error cycle, rather than suppressing messages until
							// Sync. A normal Q must receive the retained failed-transaction diagnostic.
							frames = w.query(t, "SELECT 42")
							serverWantTags(t, frames, "EZ")
							serverNotificationError(t, frames, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
							serverWantReady(t, frames, 'E')
							frames = w.query(t, "ROLLBACK")
							serverWantTags(t, frames, "CZ")
							serverWantCompletions(t, frames, "ROLLBACK")
							serverWantReady(t, frames, 'I')
						}
						serverScalar(t, w, "SELECT 42", "42")
					})
				}
			}
		})
	}
}
