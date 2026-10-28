//go:build !noservertests

package reference_test

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceClientEncodingBinary(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			for _, q := range []string{
				"CREATE TYPE ce_enum AS ENUM ('é')",
				"CREATE DOMAIN ce_domain AS text",
				"CREATE TYPE ce_record AS (v text,b bytea)",
				"CREATE TYPE ce_range AS RANGE (subtype=text)",
				"CREATE TABLE ce_binary(v text,b bytea,n int)",
			} {
				serverWantCode(t, w.query(t, q), "")
			}
			clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
			oid := func(name string) uint32 {
				frames := w.query(t, "SELECT '"+name+"'::regtype::oid")
				serverWantCode(t, frames, "")
				v := serverValues(t, frames)
				if len(v) != 1 || len(v[0]) != 1 {
					t.Fatalf("type OID %s", clientEncodingTranscript(frames))
				}
				n, err := strconv.ParseUint(v[0][0], 10, 32)
				if err != nil || n == 0 {
					t.Fatalf("type OID %q %v", v, err)
				}
				return uint32(n)
			}
			array := []byte{0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 25, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 2, 0xe4, 0xf6}
			record := serverPut32(nil, 2)
			record = serverPut32(record, 25)
			record = append(record, clientEncodingBinaryField([]byte{0xe9})...)
			record = serverPut32(record, 17)
			record = append(record, clientEncodingBinaryField([]byte{0xff, 0, 0xe9})...)
			rangeValue := append([]byte{2}, clientEncodingBinaryField([]byte{0xe4})...)
			rangeValue = append(rangeValue, clientEncodingBinaryField([]byte{0xf6})...)
			multi := serverPut32(nil, 1)
			multi = append(multi, clientEncodingBinaryField(rangeValue)...)
			for i, c := range []struct {
				name string
				raw  []byte
			}{
				{"text", []byte{0xe4, 0xf6}}, {"varchar", []byte{0xe4, 0xf6}}, {"bpchar", []byte{0xe4, 0xf6}},
				{"name", []byte{0xe4, 0xf6}}, {"refcursor", []byte{0xe4, 0xf6}}, {"ce_enum", []byte{0xe9}},
				{"json", []byte{'"', 0xe4, 0xf6, '"'}}, {"jsonb", []byte{1, '"', 0xe4, 0xf6, '"'}},
				{"jsonpath", []byte{1, '$', '.', '"', 0xe9, '"'}},
				{"text[]", array}, {"ce_domain", []byte{0xe9}}, {"ce_record", record},
				{"ce_range", rangeValue}, {"ce_multirange", multi},
				{"bytea", []byte{0xe4, 0xf6, 0, 0xff}}, {`"char"`, []byte{0xe9}},
				{"integer", []byte{0, 0, 0, 42}}, {"boolean", []byte{1}},
				{"numeric", []byte{0, 1, 0, 0, 0, 0, 0, 0, 0, 42}},
				{"uuid", []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}},
			} {
				t.Run(c.name, func(t *testing.T) {
					typeOID := oid(c.name)
					name := "binary_" + strconv.Itoa(i)
					frames := serverCycle(t, w, serverParse(name, "SELECT $1 AS v", typeOID), serverBind("", name, []int{1}, [][]byte{c.raw}, 1), serverDescribe('P', ""), serverExecute("", 0))
					serverWantTags(t, frames, "12TDCZ")
					// Domain protocol metadata is its base type, not the domain OID.
					resultOID := typeOID
					if c.name == "ce_domain" {
						resultOID = 25
					}
					clientEncodingRow(t, frames, []string{"v"}, []uint32{resultOID}, []int16{1}, c.raw)
					serverWantCompletions(t, frames, "SELECT 1")
					serverWantReady(t, frames, 'I')
				})
			}
			for _, option := range []string{"", ", ENCODING 'UTF8'", ", ENCODING 'SJIS'"} {
				t.Run("binary_copy"+option, func(t *testing.T) {
					clientEncodingCommand(t, w, "TRUNCATE ce_binary", "TRUNCATE TABLE")
					payload := serverCopyBinary([][]byte{{0xe9}, {0xff, 0, 0xe9}, {0, 0, 0, 42}})
					serverCopyStart(t, w, "COPY ce_binary FROM STDIN (FORMAT binary"+option+")", false, 1, 3)
					for _, v := range payload {
						w.send(t, serverPacket('d', []byte{v}))
					}
					frames := serverCopyFinish(t, w, false)
					serverWantTags(t, frames, "CZ")
					serverWantCompletions(t, frames, "COPY 1")
					frames = w.query(t, "COPY ce_binary TO STDOUT (FORMAT binary"+option+")")
					clientEncodingCopyBytes(t, frames, payload, "COPY 1")
					if frames[0].tag != 'H' || !bytes.Equal(frames[0].body, []byte{1, 0, 3, 0, 1, 0, 1, 0, 1}) {
						t.Fatalf("binary CopyOut metadata %s", clientEncodingTranscript(frames))
					}
					clientEncodingCommand(t, w, "SET client_encoding='UTF8'", "SET")
					serverScalar(t, w, "SELECT v FROM ce_binary", "é")
					serverScalar(t, w, "SELECT encode(b,'hex') FROM ce_binary", "ff00e9")
					clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
				})
			}
			// Bad version remains a codec error; conversion must not alter version1.
			frames := serverCycle(t, w, serverParse("bad_version", "SELECT $1", 3802), serverBind("", "bad_version", []int{1}, [][]byte{{2, '"', 0xe9, '"'}}))
			clientEncodingError(t, frames, "XX000", "unsupported jsonb version number 2", "unnamed portal parameter $1")
			serverScalar(t, w, "SELECT 42", "42")
			frames = serverCycle(t, w, serverParse("unrepresentable", `SELECT U&'\20AC'::text AS v`), serverBind("", "unrepresentable", nil, nil, 1), serverDescribe('P', ""), serverExecute("", 0))
			serverWantTags(t, frames, "12TEZ")
			clientEncodingError(t, frames, "22P05", clientEncodingEuroError, "")
			serverScalar(t, w, "SELECT 42", "42")
			frames = w.query(t, "COPY (SELECT NULL::aclitem WHERE FALSE) TO STDOUT (FORMAT binary)")
			serverWantTags(t, frames, "HEZ")
			clientEncodingError(t, frames, "42883", "no binary output function available for type aclitem", "")
			serverScalar(t, w, "SELECT 42", "42")
			// Structural lengths are network order, independent of encoded leaf bytes.
			if binary.BigEndian.Uint32(array[20:24]) != 2 {
				t.Fatal("independent array fixture length")
			}
		})
	}
}
