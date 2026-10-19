//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerCopyBinaryInputAtomicity(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, peer.query(t, "CREATE TABLE server_copy_binary_input_atomicity(v integer); INSERT INTO server_copy_binary_input_atomicity VALUES (99)"), "")
			for _, split := range []bool{false, true} {
				name := "complete_record"
				if split {
					name = "split_record"
				}
				t.Run(name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					serverWantCode(t, w.query(t, "BEGIN"), "")
					serverCopyStart(t, w, "COPY server_copy_binary_input_atomicity FROM STDIN (FORMAT binary)", true, 1, 1)
					// Row1 is a real valid prefix. Row2's complete int4 payload
					// contains an extra byte after its four-byte integer.
					stream := serverCopyBinary([][]byte{{0, 0, 0, 1}}, [][]byte{{0, 0, 0, 2, 0}})
					stream = stream[:len(stream)-2]
					if split {
						w.send(t, serverPacket('d', stream[:len(stream)-1]))
						w.send(t, serverPacket('d', stream[len(stream)-1:]))
					} else {
						w.send(t, serverPacket('d', stream))
					}
					// Require conversion failure before offering either binary
					// trailer or CopyDone; packet boundaries cannot finish a row.
					frames := serverThrough(t, w, 'E')
					serverWantTags(t, frames, "E")
					serverNotificationError(t, frames, "22P03", "incorrect binary data format", "", "COPY server_copy_binary_input_atomicity, line 2, column v")
					serverWantCompletions(t, frames)
					w.send(t, serverPacket('c', nil))
					w.send(t, serverPacket('S', nil))
					frames = append(frames, w.ready(t)...)
					serverWantTags(t, frames, "EZ")
					serverWantReady(t, frames, 'E')
					serverWantCompletions(t, frames)
					serverScalar(t, peer, "SELECT v FROM server_copy_binary_input_atomicity", "99")
					serverWantCode(t, w.query(t, "ROLLBACK"), "")
					serverScalar(t, w, "SELECT 42", "42")
					serverScalar(t, w, "SELECT v FROM server_copy_binary_input_atomicity", "99")
				})
			}
		})
	}
}
