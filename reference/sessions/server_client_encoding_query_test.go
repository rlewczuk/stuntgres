//go:build !noservertests

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceClientEncodingQuery(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			owner := serverAuthenticated(t, f.DSNFor(b))
			clientEncodingCommand(t, owner, "CREATE TABLE ce_query_storage(v text)", "CREATE TABLE")
			for _, c := range []struct{ encoding, raw, unicode string }{
				{"LATIN1", "\xe9", "é"}, {"LATIN1", "\xe4\xf6", "äö"},
				{"SJIS", "\x82\xa0", "あ"}, {"SJIS", "\x8f\xdb", "象"},
				{"EUC_JP", "\xa4\xa2", "あ"}, {"EUC_JP", "\xbe\xdd", "象"},
				{"GB18030", "\x84\x30\x9c\x38", "飯"}, {"WIN1251", "\xdf", "Я"},
				{"KOI8R", "\xc6\xcf\xcf", "фоо"}, {"SQL_ASCII", "é", "é"},
			} {
				t.Run(c.encoding+"_"+c.unicode, func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(b))
					// Start UTF8, so native-red reaches live SET, not the known startup rejection.
					set := clientEncodingCommand(t, w, "SET client_encoding='"+c.encoding+"'", "SET")
					if c.encoding != "UTF8" {
						clientEncodingStatus(t, set, "client_encoding", c.encoding)
					}
					q := "SELECT '" + c.raw + "'::text AS \"" + c.raw + "\", NULL::text AS n, ''::text AS e"
					frames := w.query(t, q)
					serverWantTags(t, frames, "TDCZ")
					clientEncodingRow(t, frames, []string{c.raw, "n", "e"}, []uint32{25, 25, 25}, []int16{0, 0, 0}, []byte(c.raw), nil, []byte{})
					serverWantCompletions(t, frames, "SELECT 1")
					serverWantReady(t, frames, 'I')
					clientEncodingCommand(t, w, "CREATE TEMP TABLE \""+c.raw+"\"(v text)", "CREATE TABLE")
					clientEncodingCommand(t, w, "INSERT INTO \""+c.raw+"\" VALUES ('"+c.raw+"')", "INSERT 0 1")
					clientEncodingCommand(t, w, "TRUNCATE ce_query_storage; INSERT INTO ce_query_storage VALUES('"+c.raw+"')", "TRUNCATE TABLE", "INSERT 0 1")
					peer := serverAuthenticated(t, f.DSNFor(b))
					serverScalar(t, peer, "SELECT v FROM ce_query_storage", c.unicode)
					peer.Close()
					clientEncodingCommand(t, w, "SET client_encoding='UTF8'", "SET")
					serverScalar(t, w, "SELECT v FROM \""+c.unicode+"\"", c.unicode)
				})
			}
			t.Run("whole_query_decoded_before_set", func(t *testing.T) {
				w := serverAuthenticated(t, f.DSNFor(b))
				clientEncodingCommand(t, w, "SET client_encoding='LATIN1'", "SET")
				frames := w.query(t, "SET client_encoding='UTF8'; SELECT '\xe4\xf6'::text AS v")
				serverWantTags(t, frames, "CTDCSZ")
				clientEncodingRow(t, frames, []string{"v"}, []uint32{25}, []int16{0}, []byte("äö"))
				serverWantCompletions(t, frames, "SET", "SELECT 1")
				clientEncodingStatus(t, frames, "client_encoding", "UTF8")
			})
			t.Run("sql_ascii_validates_server_input", func(t *testing.T) {
				w := serverAuthenticated(t, f.DSNFor(b))
				clientEncodingCommand(t, w, "SET client_encoding='SQL_ASCII'", "SET")
				frames := w.query(t, "SELECT '\xff'")
				serverWantTags(t, frames, "EZ")
				clientEncodingError(t, frames, "22021", `invalid byte sequence for encoding "UTF8": 0xff`, "")
				serverScalar(t, w, "SELECT 42", "42")
			})
		})
	}
}
