//go:build !noservertests

package reference_test

import (
	"bytes"
	"encoding/binary"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceClientEncodingExtended(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			for _, named := range []bool{false, true} {
				for _, format := range []int{0, 1} {
					w := serverAuthenticated(t, dsn)
					clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
					statement, portal := "", ""
					if named {
						statement, portal = "s\xe9", "p\xe9"
					}
					frames := serverCycle(t, w, serverParse(statement, "SELECT $1::text AS \"\xe9\", $2::text AS n, $3::text AS e", 25, 25, 25), serverDescribe('S', statement))
					serverWantTags(t, frames, "1tTZ")
					columns := serverColumns(t, frames)
					if len(columns) != 3 || columns[0].name != "\xe9" || columns[0].oid != 25 || columns[0].format != 0 || columns[0].typmod != -1 || columns[0].length != -1 {
						t.Fatalf("statement columns %+v", columns)
					}
					frames = serverCycle(t, w, serverBind(portal, statement, []int{format}, [][]byte{{0xe9}, nil, {}}, format), serverDescribe('P', portal), serverExecute(portal, 0))
					serverWantTags(t, frames, "2TDCZ")
					clientEncodingRow(t, frames, []string{"\xe9", "n", "e"}, []uint32{25, 25, 25}, []int16{int16(format), int16(format), int16(format)}, []byte{0xe9}, nil, []byte{})
					serverWantCompletions(t, frames, "SELECT 1")
					// Parse's Unicode source survives subsequent incoming encoding changes.
					clientEncodingCommand(t, w, "SET client_encoding='UTF8'", "SET")
					if named {
						frames = serverCycle(t, w, serverBind("p2", "sé", []int{format}, [][]byte{[]byte("é"), nil, {}}, format), serverDescribe('P', "p2"), serverExecute("p2", 0), serverClose('S', "sé"))
						serverWantTags(t, frames, "2TDC3Z")
						clientEncodingRow(t, frames, []string{"é", "n", "e"}, []uint32{25, 25, 25}, []int16{int16(format), int16(format), int16(format)}, []byte("é"), nil, []byte{})
					}
				}
			}
			t.Run("held_suspended_portal_current_output", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				clientEncodingCommand(t, w, "SET client_encoding='LATIN1'; BEGIN", "SET", "BEGIN")
				frames := serverCycle(t, w, serverParse("s\xe9", "SELECT v FROM (VALUES ('\xe9'::text),('\xe9')) x(v)"), serverBind("p\xe9", "s\xe9", nil, nil), serverClose('S', "s\xe9"), serverDescribe('P', "p\xe9"), serverExecute("p\xe9", 1))
				serverWantTags(t, frames, "123TDsZ")
				serverWantRaw(t, frames, [][][]byte{{{0xe9}}})
				old := bytes.Clone(serverRawRows(t, frames)[0][0])
				// Named SET does not destroy unnamed objects or end the portal transaction.
				frames = serverCycle(t, w, serverParse("change", "SET client_encoding='UTF8'"), serverBind("change", "change", nil, nil), serverExecute("change", 0))
				serverWantCode(t, frames, "")
				frames = serverCycle(t, w, serverDescribe('P', "pé"), serverExecute("pé", 0), serverClose('P', "pé"))
				serverWantTags(t, frames, "TDC3Z")
				clientEncodingRow(t, frames, []string{"v"}, []uint32{25}, []int16{0}, []byte("é"))
				if !bytes.Equal(old, []byte{0xe9}) {
					t.Fatalf("old row mutated %x", old)
				}
				clientEncodingCommand(t, w, "ROLLBACK", "ROLLBACK")
			})
			for _, c := range []struct {
				name   string
				packet []byte
			}{
				{"parse_name", serverPacket('P', serverCString(nil, "\x82"))},
				{"parse_source", serverPacket('P', serverCString(serverCString(nil, ""), "\x82"))},
				{"bind_portal", serverPacket('B', serverCString(nil, "\x82"))},
				{"bind_statement", serverPacket('B', serverCString(serverCString(nil, "p"), "\x82"))},
				{"describe", serverDescribe('X', "\x82")}, {"close", serverClose('X', "\x82")},
				{"execute", serverPacket('E', serverCString(nil, "\x82"))},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					clientEncodingCommand(t, w, "SET client_encoding='SJIS'", "SET")
					serverWantCode(t, serverCycle(t, w, serverParse("", "SELECT 43")), "")
					frames := serverCycle(t, w, c.packet, serverParse("skipped", "SELECT 99"))
					serverWantTags(t, frames, "EZ")
					clientEncodingError(t, frames, "22021", `invalid byte sequence for encoding "SJIS": 0x82`, "")
					frames = serverCycle(t, w, serverBind("", "", nil, nil), serverExecute("", 0))
					serverWantValues(t, frames, [][]string{{"43"}})
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
			for _, c := range []struct {
				encoding      string
				raw           []byte
				code, message string
			}{
				{"LATIN1", []byte{0}, "22021", `invalid byte sequence for encoding "LATIN1": 0x00`},
				{"SJIS", []byte{0x82}, "22021", `invalid byte sequence for encoding "SJIS": 0x82`},
				{"GB18030", []byte{0x84, 0x30, 0x9c}, "22021", `invalid byte sequence for encoding "GB18030": 0x84 0x30 0x9c`},
				{"GB18030", []byte{0x84, 0x31, 0xa5, 0x30}, "22P05", `character with byte sequence 0x84 0x31 0xa5 0x30 in encoding "GB18030" has no equivalent in encoding "UTF8"`},
			} {
				for _, format := range []int{0, 1} {
					w := serverAuthenticated(t, dsn)
					clientEncodingCommand(t, w, "SET client_encoding='"+c.encoding+"'", "SET")
					serverWantCode(t, serverCycle(t, w, serverParse("s", "SELECT $1::text", 25)), "")
					frames := serverCycle(t, w, serverBind("p", "s", []int{format}, [][]byte{c.raw}), serverExecute("p", 0))
					serverWantTags(t, frames, "EZ")
					clientEncodingError(t, frames, c.code, c.message, "portal \"p\" parameter $1")
					serverScalar(t, w, "SELECT 42", "42")
				}
			}
			t.Run("fastpath", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
				frames := w.query(t, "SELECT 'length(text)'::regprocedure::oid, 'abs(integer)'::regprocedure::oid")
				values := serverValues(t, frames)
				if len(values) != 1 || len(values[0]) != 2 {
					t.Fatalf("function OIDs %s", clientEncodingTranscript(frames))
				}
				var lengthOID, absOID uint32
				for i, p := range []*uint32{&lengthOID, &absOID} {
					var n uint64
					for _, c := range values[0][i] {
						if c < '0' || c > '9' {
							t.Fatal("OID", values)
						}
						n = n*10 + uint64(c-'0')
					}
					*p = uint32(n)
				}
				for _, c := range []struct {
					oid       uint32
					arg, want []byte
				}{{lengthOID, []byte{0xe9}, []byte{0, 0, 0, 1}}, {absOID, []byte{0xff, 0xff, 0xff, 0xd6}, []byte{0, 0, 0, 42}}} {
					format := 1
					if c.oid == lengthOID {
						format = 0
					}
					w.send(t, serverFunction(c.oid, []int{format}, [][]byte{c.arg}, 1))
					frames := w.ready(t)
					serverWantTags(t, frames, "VZ")
					if !bytes.Equal(frames[0].body, clientEncodingBinaryField(c.want)) || binary.BigEndian.Uint32(frames[0].body) != 4 {
						t.Fatalf("fastpath %s", clientEncodingTranscript(frames))
					}
				}
			})
		})
	}
}
