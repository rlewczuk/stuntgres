//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerReceivePrecedence(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			one := func(t *testing.T, w *serverWire) {
				serverWantTags(t, serverCycle(t, w, serverParse("s", "SELECT $1", 23)), "1Z")
			}
			two := func(t *testing.T, w *serverWire) {
				serverWantTags(t, serverCycle(t, w, serverParse("s", "SELECT $1,$2", 23, 23)), "1Z")
			}
			duplicate := func(t *testing.T, w *serverWire) {
				serverWantCode(t, w.query(t, "BEGIN"), "")
				serverWantTags(t, serverCycle(t, w, serverParse("s", "SELECT $1", 23), serverBind("p", "s", nil, [][]byte{[]byte("42")})), "12Z")
			}
			aborted := func(t *testing.T, w *serverWire) {
				serverWantCode(t, w.query(t, "BEGIN"), "")
				serverNotificationError(t, w.query(t, "SELECT 1/0"), "22012", "division by zero", "")
			}
			// An intact Bind frame whose second argument length has only two bytes.
			// Argument1 must be converted before the receiver reaches that underflow.
			laterTruncated := func(first string) []byte {
				body := serverCString(serverCString(nil, ""), "s")
				body = server16(server16(body, 0), 2)
				body = serverPut32(body, uint32(len(first)))
				body = append(body, first...)
				body = append(body, 0, 0)
				return serverPacket('B', body)
			}
			for _, c := range []struct {
				name                   string
				setup                  func(*testing.T, *serverWire)
				packet                 []byte
				code, message, context string
				ready                  byte
				function               bool
			}{
				{name: "missing_statement_before_truncated_suffix", packet: serverPacket('B', serverCString(serverCString(nil, ""), "absent")), code: "26000", message: "prepared statement \"absent\" does not exist", ready: 'I'},
				{name: "existing_statement_reaches_truncated_suffix", setup: one, packet: serverPacket('B', serverCString(serverCString(nil, ""), "s")), code: "08P01", message: "insufficient data left in message", ready: 'I'},
				{name: "selector_mismatch_before_formal_arity", setup: two, packet: serverBind("", "s", []int{0, 0}, [][]byte{[]byte("1")}), code: "08P01", message: "bind message has 2 parameter formats but 1 parameters", ready: 'I'},
				{name: "matching_selectors_reach_formal_arity", setup: two, packet: serverBind("", "s", nil, [][]byte{[]byte("1")}), code: "08P01", message: "bind message supplies 1 parameters, but prepared statement \"s\" requires 2", ready: 'I'},
				{name: "duplicate_portal_before_conversion", setup: duplicate, packet: serverBind("p", "s", nil, [][]byte{[]byte("bad")}), code: "42P03", message: "cursor \"p\" already exists", ready: 'E'},
				{name: "first_conversion_before_second_length", setup: two, packet: laterTruncated("bad"), code: "22P02", message: "invalid input syntax for type integer: \"bad\"", context: "unnamed portal parameter $1 = '...'", ready: 'I'},
				{name: "valid_first_argument_reaches_second_length", setup: two, packet: laterTruncated("42"), code: "08P01", message: "insufficient data left in message", context: "unnamed portal parameter $2", ready: 'I'},
				{name: "aborted_function_before_missing_oid", setup: aborted, packet: serverPacket('F', nil), code: "25P02", message: "current transaction is aborted, commands ignored until end of transaction block", ready: 'E', function: true},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(b))
					defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
					if c.setup != nil {
						c.setup(t, w)
					}
					var frames []serverFrame
					if c.function {
						w.send(t, c.packet)
						frames = w.ready(t)
					} else {
						frames = serverCycle(t, w, c.packet)
					}
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, c.code, c.message, "", c.context)
					serverWantReady(t, frames, c.ready)
					// Every bounded logical error leaves a recoverable same-owner stream.
					serverWantCode(t, w.query(t, "ROLLBACK"), "")
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
		})
	}
}
