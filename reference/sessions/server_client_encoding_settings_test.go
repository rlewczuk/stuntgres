//go:build !noservertests

package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceClientEncodingSettings(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			for _, c := range []struct{ name, setup, finish, want, active string }{
				{"local_commit", "BEGIN; SET LOCAL client_encoding='SJIS'", "COMMIT", "LATIN1", "SJIS"},
				{"local_rollback", "BEGIN; SET LOCAL client_encoding='SJIS'", "ROLLBACK", "LATIN1", "SJIS"},
				{"session_commit", "BEGIN; SET SESSION client_encoding='SJIS'", "COMMIT", "SJIS", "SJIS"},
				{"session_rollback", "BEGIN; SET SESSION client_encoding='SJIS'", "ROLLBACK", "LATIN1", "SJIS"},
				{"set_then_local_commit", "BEGIN; SET client_encoding='SJIS'; SET LOCAL client_encoding='EUC_JP'", "COMMIT", "SJIS", "EUC_JP"},
				{"set_then_local_rollback", "BEGIN; SET client_encoding='SJIS'; SET LOCAL client_encoding='EUC_JP'", "ROLLBACK", "LATIN1", "EUC_JP"},
				{"savepoint_rollback", "BEGIN; SAVEPOINT s; SET client_encoding='SJIS'; ROLLBACK TO s", "COMMIT", "LATIN1", "LATIN1"},
				{"savepoint_release", "BEGIN; SAVEPOINT s; SET LOCAL client_encoding='SJIS'; RELEASE s", "COMMIT", "LATIN1", "SJIS"},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					peer := serverAuthenticated(t, dsn)
					clientEncodingCommand(t, w, "SET NAMES 'LATIN1'", "SET")
					setupFrames := w.query(t, c.setup)
					serverWantCode(t, setupFrames, "")
					serverWantReady(t, setupFrames, 'T')
					if c.active != "LATIN1" {
						clientEncodingStatus(t, setupFrames, "client_encoding", c.active)
					} else {
						clientEncodingStatus(t, setupFrames, "client_encoding")
					}
					serverScalar(t, w, "SHOW client_encoding", c.active)
					activeRaw := "\xe9"
					if c.active == "SJIS" {
						activeRaw = "\x82\xa0"
					}
					if c.active == "EUC_JP" {
						activeRaw = "\xa4\xa2"
					}
					activeFrames := w.query(t, "SELECT '"+activeRaw+"'::text AS v")
					clientEncodingRow(t, activeFrames, []string{"v"}, []uint32{25}, []int16{0}, []byte(activeRaw))
					serverWantCompletions(t, activeFrames, "SELECT 1")
					frames := w.query(t, c.finish)
					serverWantCode(t, frames, "")
					serverWantReady(t, frames, 'I')
					if c.active != c.want {
						clientEncodingStatus(t, frames, "client_encoding", c.want)
					} else {
						clientEncodingStatus(t, frames, "client_encoding")
					}
					serverScalar(t, w, "SHOW client_encoding", c.want)
					serverScalar(t, peer, "SHOW client_encoding", "UTF8")
					raw := "\xe9"
					if c.want == "SJIS" {
						raw = "\x82\xa0"
					}
					serverScalar(t, w, "SELECT '"+raw+"'::text", raw)
				})
			}
			for _, reset := range []string{"RESET client_encoding", "SET client_encoding=DEFAULT", "RESET ALL", "DISCARD ALL"} {
				t.Run(reset, func(t *testing.T) {
					w := clientEncodingLogin(t, dsn, "LATIN1")
					clientEncodingCommand(t, w, "SET client_encoding='SJIS'", "SET")
					frames := w.query(t, reset)
					serverWantCode(t, frames, "")
					clientEncodingStatus(t, frames, "client_encoding", "LATIN1")
					serverScalar(t, w, "SHOW client_encoding", "LATIN1")
					serverScalar(t, w, "SELECT '\xe9'::text", "\xe9")
				})
			}
			t.Run("held_set_config_and_sync_reporting", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				frames := serverCycle(t, w, serverParse("held", `SELECT set_config('client_encoding',$1,false)`, 25))
				serverWantTags(t, frames, "1Z")
				for _, enc := range []string{"LATIN1", "SJIS", "UTF8"} {
					w.send(t, serverBind("", "held", nil, [][]byte{[]byte(enc)}))
					w.send(t, serverDescribe('P', ""))
					w.send(t, serverExecute("", 0))
					w.send(t, serverPacket('H', nil))
					frames = serverThrough(t, w, 'C')
					serverWantTags(t, frames, "2TDC")
					clientEncodingRow(t, frames, []string{"set_config"}, []uint32{25}, []int16{0}, []byte(enc))
					serverWantCompletions(t, frames, "SELECT 1")
					clientEncodingStatus(t, frames, "client_encoding")
					w.send(t, serverPacket('S', nil))
					frames = w.ready(t)
					serverWantTags(t, frames, "SZ")
					clientEncodingStatus(t, frames, "client_encoding", enc)
				}
			})
			t.Run("error_before_abort_reporting", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				q := `SET client_encoding='LATIN1'; SELECT U&'\00E9'::int`
				frames := w.query(t, q)
				serverWantTags(t, frames, "CEZ")
				clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': "22P02", 'M': "invalid input syntax for type integer: \"\xe9\"", 'P': strconv.Itoa(strings.Index(q, "U&") + 1)})
				clientEncodingStatus(t, frames, "client_encoding")
				serverScalar(t, w, "SHOW client_encoding", "UTF8")
				serverScalar(t, w, "SELECT 'é'::text", "é")
			})
			t.Run("coalesced_multi_query", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				frames := w.query(t, "SET client_encoding='LATIN1'; SET client_encoding='SJIS'; SET client_encoding='UTF8'")
				serverWantTags(t, frames, "CCCZ")
				clientEncodingStatus(t, frames, "client_encoding")
			})
			t.Run("normalization_and_failed_publication", func(t *testing.T) {
				w := serverAuthenticated(t, dsn)
				for _, c := range []struct{ value, want string }{{"latin-1", "LATIN1"}, {"UNICODE", "UNICODE"}, {"UTF_8", "UTF8"}, {"SQL_ASCII", "SQL_ASCII"}} {
					clientEncodingCommand(t, w, "SET client_encoding='"+c.value+"'", "SET")
					serverScalar(t, w, "SHOW client_encoding", c.want)
				}
				for _, c := range []struct{ value, code, message, detail string }{
					{"not_an_encoding", "22023", `invalid value for parameter "client_encoding": "not_an_encoding"`, ""},
					{"MULE_INTERNAL", "0A000", `invalid value for parameter "client_encoding": "MULE_INTERNAL"`, `Conversion between MULE_INTERNAL and UTF8 is not supported.`},
				} {
					frames := w.query(t, "SET client_encoding='"+c.value+"'")
					serverWantTags(t, frames, "EZ")
					clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': c.code, 'M': c.message, 'D': c.detail})
					clientEncodingStatus(t, frames, "client_encoding")
					serverScalar(t, w, "SHOW client_encoding", "SQL_ASCII")
				}
				for _, q := range []string{"SET client_encoding='" + strings.Repeat("-", 80) + "utf8'", "SET NAMES '" + strings.Repeat("-", 80) + "utf8'", "SELECT set_config('client_encoding','" + strings.Repeat("-", 80) + "utf8',false)"} {
					frames := clientEncodingQuery(t, w, q)
					clientEncodingError(t, frames, "22023", `invalid value for parameter "client_encoding": "`+strings.Repeat("-", 63)+`"`, "")
					clientEncodingStatus(t, frames, "client_encoding")
					serverScalar(t, w, "SHOW client_encoding", "SQL_ASCII")
				}
				// C-string NUL terminates the field; extra bytes are a protocol error.
				w.send(t, serverPacket('Q', []byte("SELECT 42\x00tail\x00")))
				frames := w.ready(t)
				clientEncodingError(t, frames, "08P01", "invalid message format", "")
				serverScalar(t, w, "SELECT 42", "42")
			})
			t.Run("persisted_defaults", func(t *testing.T) { clientEncodingPersistedDefaults(t, f, b) })
		})
	}
}
