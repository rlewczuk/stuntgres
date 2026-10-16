//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerCopyBinaryReceive(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			for _, extended := range []bool{false, true} {
				route := "simple"
				if extended {
					route = "extended"
				}
				for _, tc := range []struct {
					name    string
					columns string
					list    string
					payload []byte
				}{
					{"empty", "v aclitem", "", serverCopyBinary()},
					{"all_null", "v aclitem", "", serverCopyBinary([][]byte{nil})},
					{"omitted_aclitem", "id integer, v aclitem", "(id)", serverCopyBinary([][]byte{{0, 0, 0, 7}})},
				} {
					t.Run(route+"/"+tc.name, func(t *testing.T) {
						w := serverAuthenticated(t, dsn)
						peer := serverAuthenticated(t, dsn)
						table := "server_copy_receive_" + route + "_" + tc.name
						serverWantCode(t, w.query(t, "CREATE TABLE "+table+"("+tc.columns+")"), "")
						serverScalar(t, peer, "SELECT count(*) FROM "+table, "0")
						if extended {
							frames := w.query(t, "BEGIN")
							serverWantTags(t, frames, "CZ")
							serverWantReady(t, frames, 'T')
						}
						source := "COPY " + table + tc.list + " FROM STDIN (FORMAT binary)"
						if extended {
							for _, packet := range [][]byte{serverParse("cp", source), serverBind("p", "cp", nil, nil), serverDescribe('P', "p"), serverExecute("p", 0)} {
								w.send(t, packet)
							}
						} else {
							w.send(t, serverPacket('Q', serverCString(nil, source)))
						}
						// Offer real, valid streams without waiting for G. The empty
						// stream could complete COPY 0; the NULL row could complete
						// COPY 1 without any field conversion. Neither may bypass
						// destination receive initialization. The omitted-column arm
						// offers an ordinary int4 row but still requires aclitem's
						// provider for the other non-dropped physical attribute.
						w.send(t, serverPacket('d', tc.payload[:19]))
						w.send(t, serverPacket('d', tc.payload[19:]))
						w.send(t, serverPacket('c', nil))
						if extended {
							w.send(t, serverPacket('S', nil))
						}
						frames := w.ready(t)
						wantTags := "EZ"
						state := byte('I')
						if extended {
							wantTags = "12nEZ"
							state = 'E'
						}
						serverWantTags(t, frames, wantTags)
						if extended && len(frames[2].body) != 0 {
							t.Fatalf("COPY portal NoData payload %x", frames[2].body)
						}
						serverNotificationError(t, frames, "42883", "no binary input function available for type aclitem", "")
						serverWantCompletions(t, frames)
						serverWantReady(t, frames, state)
						if extended {
							frames = w.query(t, "ROLLBACK")
							serverWantTags(t, frames, "CZ")
							serverWantCompletions(t, frames, "ROLLBACK")
							serverWantReady(t, frames, 'I')
						}
						// This same-owner query fences the offered data/CopyDone
						// packets as well as proving recovery. No rows may have
						// appeared through either the owner or an independent peer.
						serverScalar(t, w, "SELECT 42", "42")
						serverScalar(t, w, "SELECT count(*) FROM "+table, "0")
						serverScalar(t, peer, "SELECT count(*) FROM "+table, "0")
					})
				}
			}
		})
	}
}

func TestReferenceServerCopyBinaryDomainReceive(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			for _, extended := range []bool{false, true} {
				route := "simple"
				if extended {
					route = "extended"
				}
				for _, nullRow := range []bool{false, true} {
					name := "empty"
					if nullRow {
						name = "null"
					}
					t.Run(route+"/"+name, func(t *testing.T) {
						w := serverAuthenticated(t, dsn)
						peer := serverAuthenticated(t, dsn)
						domain := "server_copy_receive_domain_" + route + "_" + name
						table := "server_copy_domain_rows_" + route + "_" + name
						serverWantCode(t, w.query(t, "CREATE DOMAIN "+domain+" AS aclitem"), "")
						serverWantCode(t, w.query(t, "CREATE TABLE "+table+"(v "+domain+")"), "")
						serverScalar(t, peer, "SELECT count(*) FROM "+table, "0")
						if extended {
							frames := w.query(t, "BEGIN")
							serverWantTags(t, frames, "CZ")
							serverWantReady(t, frames, 'T')
						}
						// The declared domain has domain_recv even though its base
						// lacks aclitem_recv. COPY setup must admit G before any
						// invocation discovers the base receive-function absence.
						source := "COPY " + table + " FROM STDIN (FORMAT binary)"
						serverCopyStart(t, w, source, extended, 1, 1)
						payload := serverCopyBinary()
						if nullRow {
							payload = serverCopyBinary([][]byte{nil})
						}
						w.send(t, serverPacket('d', payload[:19]))
						w.send(t, serverPacket('d', payload[19:]))
						frames := serverCopyFinish(t, w, extended)
						state := byte('I')
						if nullRow {
							serverWantTags(t, frames, "EZ")
							serverNotificationError(t, frames, "42883", "no binary input function available for type aclitem", "", "COPY "+table+", line 1, column v")
							serverWantCompletions(t, frames)
							if extended {
								state = 'E'
							}
						} else {
							serverWantTags(t, frames, "CZ")
							serverWantCode(t, frames, "")
							serverWantCompletions(t, frames, "COPY 0")
							if extended {
								state = 'T'
							}
						}
						serverWantReady(t, frames, state)
						if extended {
							command := "COMMIT"
							if nullRow {
								command = "ROLLBACK"
							}
							frames = w.query(t, command)
							serverWantTags(t, frames, "CZ")
							serverWantCompletions(t, frames, command)
							serverWantReady(t, frames, 'I')
						}
						serverScalar(t, w, "SELECT 42", "42")
						serverScalar(t, w, "SELECT count(*) FROM "+table, "0")
						serverScalar(t, peer, "SELECT count(*) FROM "+table, "0")
					})
				}
			}
		})
	}
}
