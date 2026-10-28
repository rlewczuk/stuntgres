//go:build !noservertests

package reference_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"stuntgres/reference/fixture"
	"testing"
)

const clientEncodingEuroError = `character with byte sequence 0xe2 0x82 0xac in encoding "UTF8" has no equivalent in encoding "LATIN1"`

func TestReferenceClientEncodingOutput(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			for _, c := range []struct {
				name, sql, tags string
				rows            [][][]byte
			}{
				{"description", `SELECT 1 AS U&"\20AC"`, "EZ", nil},
				{"datum", `SELECT U&'\20AC' AS v`, "TEZ", nil},
				{"late_datum", `SELECT v FROM (VALUES ('ok'::text),(U&'\20AC')) x(v)`, "TDEZ", [][][]byte{{[]byte("ok")}}},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
					frames := w.query(t, c.sql)
					serverWantTags(t, frames, c.tags)
					clientEncodingError(t, frames, "22P05", clientEncodingEuroError, "")
					got := serverRawRows(t, frames)
					if len(got) != len(c.rows) {
						t.Fatalf("partial rows %x want %x: %s", got, c.rows, clientEncodingTranscript(frames))
					}
					if len(got) == 1 && !bytes.Equal(got[0][0], c.rows[0][0]) {
						t.Fatalf("prior completed row %x", got)
					}
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
			t.Run("returning_rollback", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				peer := serverAuthenticated(t, dsn)
				clientEncodingCommand(t, w, "CREATE TABLE ce_returning(v text)", "CREATE TABLE")
				clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
				frames := w.query(t, `INSERT INTO ce_returning VALUES(U&'\20AC') RETURNING v`)
				serverWantTags(t, frames, "TEZ")
				clientEncodingError(t, frames, "22P05", clientEncodingEuroError, "")
				serverScalar(t, peer, "SELECT count(*) FROM ce_returning", "0")
				serverScalar(t, w, "SELECT 42", "42")
			})
			t.Run("representable_fields", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
				frames := w.query(t, `DO $$ BEGIN RAISE NOTICE USING MESSAGE=U&'\00E4\00F6', DETAIL=U&'\00E9', HINT=U&'\00F6'; END $$`)
				serverWantTags(t, frames, "NCZ")
				clientEncodingDiagnostic(t, frames, 'N', map[byte]string{'S': "NOTICE", 'V': "NOTICE", 'C': "00000", 'M': "\xe4\xf6", 'D': "\xe9", 'H': "\xf6", 'W': "PL/pgSQL function inline_code_block line 1 at RAISE"})
				frames = w.query(t, `DO $$ BEGIN RAISE EXCEPTION USING MESSAGE=U&'\00E9', DETAIL=U&'\00E4', HINT=U&'\00F6', SCHEMA=U&'\00E9', TABLE=U&'\00E9', COLUMN=U&'\00E9', DATATYPE=U&'\00E9', CONSTRAINT=U&'\00E9'; END $$`)
				serverWantTags(t, frames, "EZ")
				clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': "P0001", 'M': "\xe9", 'D': "\xe4", 'H': "\xf6", 's': "\xe9", 't': "\xe9", 'c': "\xe9", 'd': "\xe9", 'n': "\xe9", 'W': "PL/pgSQL function inline_code_block line 1 at RAISE"})
				frames = w.query(t, "SELECT * FROM \"missing\xe9\"")
				clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': "42P01", 'M': "relation \"missing\xe9\" does not exist", 'P': "15"})
				frames = clientEncodingCommand(t, w, "SET application_name='\xe9'", "SET")
				clientEncodingStatus(t, frames, "application_name", `\xc3\xa9`)
				frames = clientEncodingCommand(t, w, "SET search_path='\xe9'", "SET")
				clientEncodingStatus(t, frames, "search_path", `"`+"\xe9"+`"`)
			})
			for _, severity := range []string{"EXCEPTION", "NOTICE"} {
				t.Run("diagnostic_recursion_"+severity, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
					frames := w.query(t, `DO $$ BEGIN RAISE `+severity+` '%', chr(8364); END $$`)
					// The pinned oracle replaces the original diagnostic and clears
					// its PL/pgSQL Context; every absent logical field stays exact.
					serverWantTags(t, frames, "EZ")
					clientEncodingError(t, frames, "22P05", clientEncodingEuroError, "")
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
			t.Run("notify_delivery_policy_and_ready_order", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				peer := serverAuthenticated(t, dsn)
				clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
				clientEncodingCommand(t, w, "LISTEN \"ce\xe9\"", "LISTEN")
				clientEncodingCommand(t, w, "BEGIN; SET LOCAL client_encoding='UTF8'", "BEGIN", "SET")
				clientEncodingCommand(t, peer, `NOTIFY U&"ce\00E9", U&'\00E4\00F6'`, "NOTIFY")
				frames := w.query(t, "ROLLBACK")
				serverWantTags(t, frames, "CASZ")
				clientEncodingStatus(t, frames, "client_encoding", "LATIN1")
				var notification []byte
				for _, fr := range frames {
					if fr.tag == 'A' {
						notification = fr.body
					}
				}
				if len(notification) < 4 || binary.BigEndian.Uint32(notification) == 0 || !bytes.Equal(notification[4:], []byte("ce\xe9\x00\xe4\xf6\x00")) {
					t.Fatalf("notification %s", clientEncodingTranscript(frames))
				}
				serverScalar(t, w, "SELECT 42", "42")
			})
			t.Run("notify_unrepresentable_fatal", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				peer := serverAuthenticated(t, dsn)
				// LISTEN becomes effective only at commit. Do not leave its
				// registration pending inside the transaction that defers delivery.
				clientEncodingCommand(t, w, "LISTEN ce_fatal", "LISTEN")
				clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
				clientEncodingCommand(t, w, "BEGIN", "BEGIN")
				clientEncodingCommand(t, peer, "CREATE TABLE ce_notify_commit(v int)", "CREATE TABLE")
				clientEncodingCommand(t, peer, `BEGIN; INSERT INTO ce_notify_commit VALUES(77); NOTIFY ce_fatal, U&'\20AC'; COMMIT`, "BEGIN", "INSERT 0 1", "NOTIFY", "COMMIT")
				w.send(t, serverPacket('Q', serverCString(nil, "COMMIT")))
				var frames []serverFrame
				for {
					fr, err := w.readFrame()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatalf("notification close %v: %s", err, clientEncodingTranscript(frames))
					}
					frames = append(frames, fr)
				}
				serverWantTags(t, frames, "CE")
				clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "FATAL", 'V': "FATAL", 'C': "22P05", 'M': clientEncodingEuroError})
				serverScalar(t, peer, "SELECT 42", "42")
				serverScalar(t, peer, "SELECT v FROM ce_notify_commit", "77")
			})
		})
	}
}
