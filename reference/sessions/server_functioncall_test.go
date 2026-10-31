//go:build !noservertests

package reference_test

import (
	"bytes"
	"encoding/binary"
	"stuntgres/reference/fixture"
	"testing"
)

func serverFunction(oid uint32, formats []int, args [][]byte, result int) []byte {
	b := serverPut32(nil, oid)
	b = server16(b, len(formats))
	for _, v := range formats {
		b = server16(b, v)
	}
	b = server16(b, len(args))
	for _, a := range args {
		if a == nil {
			b = serverPut32(b, ^uint32(0))
		} else {
			b = serverPut32(b, uint32(len(a)))
			b = append(b, a...)
		}
	}
	return serverPacket('F', server16(b, result))
}
func TestReferenceServerFunctionCall(t *testing.T) {
	f := fixture.NewServerFixture(t)
	// Builtin ACL mutation is supported, but its explicit descriptor is outside
	// native logical Dump. Disable optional persistence for this isolated test,
	// not for the independent baseline persistence scenarios.
	f.Stop(t)
	f.Dumpdir = ""
	f.Restart(t, false)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			for _, c := range []struct {
				name           string
				oid            uint32
				input, output  []byte
				format, result int
			}{
				{"abs_text", 1397, []byte("-42"), []byte("42"), 0, 0}, {"abs_binary", 1397, []byte{255, 255, 255, 214}, []byte{0, 0, 0, 42}, 1, 1},
				{"abs_null", 1397, nil, nil, 0, 0}, {"abs_null_unused_result", 1397, nil, nil, 0, 7},
				{"length_unicode", 1317, []byte("é🙂"), []byte("2"), 0, 0}, {"length_empty", 1317, []byte{}, []byte("0"), 0, 0}, {"length_null", 1317, nil, nil, 0, 1},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					w.send(t, serverFunction(c.oid, []int{c.format}, [][]byte{c.input}, c.result))
					frames := w.ready(t)
					serverWantTags(t, frames, "VZ")
					body := frames[0].body
					if len(body) < 4 {
						t.Fatal("short function result")
					}
					n := int32(binary.BigEndian.Uint32(body))
					if c.output == nil {
						if n != -1 || len(body) != 4 {
							t.Fatalf("NULL response %x", body)
						}
					} else if n != int32(len(c.output)) || !bytes.Equal(body[4:], c.output) {
						t.Fatalf("result %x want %x", body, c.output)
					}
					serverWantReady(t, frames, 'I')
				})
			}
			for _, c := range []struct {
				name          string
				packet        []byte
				code, message string
			}{
				{"missing", serverFunction(4294967295, nil, nil, 0), "42883", "function with OID 4294967295 does not exist"},
				{"arity", serverFunction(1397, nil, nil, 0), "08P01", "function call message contains 0 arguments but function requires 1"},
				{"format", serverFunction(1397, []int{2}, [][]byte{[]byte("-42")}, 0), "22023", "unsupported format code: 2"},
				{"result_format", serverFunction(1397, nil, [][]byte{[]byte("-42")}, 2), "22023", "unsupported format code: 2"},
				{"empty_integer", serverFunction(1397, nil, [][]byte{[]byte{}}, 0), "22P02", "invalid input syntax for type integer: \"\""},
				{"short_integer", serverFunction(1397, []int{1}, [][]byte{{0, 0}}, 0), "08P01", "insufficient data left in message"},
				{"binary_tail", serverFunction(1397, []int{1}, [][]byte{{0, 0, 0, 1, 0}}, 0), "22P03", "incorrect binary data format in function argument 1"},
				{"set_returning", serverFunction(1067, nil, [][]byte{[]byte("1"), []byte("2")}, 0), "0A000", "cannot call function \"generate_series\" via fastpath interface"},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					w.send(t, c.packet)
					frames := w.ready(t)
					serverWantTags(t, frames, "EZ")
					serverNotificationError(t, frames, c.code, c.message, "")
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
			t.Run("explicit_abort", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				serverWantCode(t, w.query(t, "BEGIN"), "")
				w.send(t, serverFunction(1397, nil, [][]byte{[]byte("-42")}, 0))
				serverWantReady(t, w.ready(t), 'T')
				w.send(t, serverFunction(4294967295, nil, nil, 0))
				frames := w.ready(t)
				serverWantCode(t, frames, "42883")
				serverWantReady(t, frames, 'E')
				w.send(t, serverFunction(1397, nil, [][]byte{[]byte("-42")}, 0))
				serverNotificationError(t, w.ready(t), "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
				serverWantCode(t, w.query(t, "ROLLBACK"), "")
			})
			t.Run("denied_before_input", func(t *testing.T) {
				admin := serverAuthenticated(t, dsn)
				serverWantCode(t, admin.query(t, "CREATE ROLE server_fastpath_reader"), "")
				defer func() {
					serverWantCode(t, admin.query(t, "GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO PUBLIC"), "")
					serverWantCode(t, admin.query(t, "DROP ROLE server_fastpath_reader"), "")
				}()
				serverWantCode(t, admin.query(t, "REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC"), "")
				w := serverAuthenticated(t, dsn)
				defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), ""); w.Close() }()
				serverWantCode(t, w.query(t, "SET ROLE server_fastpath_reader"), "")
				w.send(t, serverFunction(1397, nil, [][]byte{[]byte("bad")}, 0))
				frames := w.ready(t)
				serverNotificationError(t, frames, "42501", "permission denied for function abs", "")
				serverWantReady(t, frames, 'I')
			})
			t.Run("lo_open_invalid_mode", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				w.send(t, serverFunction(952, nil, [][]byte{[]byte("0"), []byte("0")}, 0))
				frames := w.ready(t)
				serverWantTags(t, frames, "EZ")
				serverNotificationError(t, frames, "22023", "invalid flags for opening a large object: 0", "")
				serverWantReady(t, frames, 'I')
				serverScalar(t, w, "SELECT 42", "42")
			})
		})
	}
}
