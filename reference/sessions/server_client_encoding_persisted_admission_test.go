//go:build !noservertests

package reference_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceClientEncodingPersistedAdmission(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			pool := f.RawPool(t, b)
			_, user, _, db := serverEndpoint(t, dsn)
			quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
			for _, scope := range []struct{ name, prefix string }{
				{"role", "ALTER ROLE " + quote(user)},
				{"database", "ALTER ROLE ALL IN DATABASE " + quote(db)},
				{"role_database", "ALTER ROLE " + quote(user) + " IN DATABASE " + quote(db)},
			} {
				t.Run(scope.name, func(t *testing.T) {
					admin := serverAuthenticated(t, dsn)
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						defer cancel()
						if _, err := pool.ExecContext(ctx, scope.prefix+" RESET client_encoding"); err != nil {
							t.Error("restore persisted scope", err)
						}
					})
					clientEncodingCommand(t, admin, scope.prefix+" SET client_encoding='SJIS'", "ALTER ROLE")
					fresh := func(t *testing.T) {
						t.Helper()
						w := serverDial(t, dsn)
						defer w.Close()
						frames := w.login(t, dsn, 196608)
						if serverCode(frames) != "" || w.statuses["client_encoding"] != "SJIS" {
							t.Fatalf("persisted publication changed: status%q frames%s", w.statuses, clientEncodingTranscript(frames))
						}
						serverScalar(t, w, "SHOW client_encoding", "SJIS")
						clientEncodingCommand(t, w, "SET client_encoding='SQL_ASCII'; RESET client_encoding", "SET", "RESET")
						serverScalar(t, w, "SHOW client_encoding", "SJIS")
						serverScalar(t, w, "SELECT 42", "42")
					}
					fresh(t)
					for _, candidate := range []struct{ name, encoding, seed string }{
						{"unsupported", "MULE_INTERNAL", ""},
						{"missing_incoming", "LATIN1", "iso_8859_1_to_utf8"},
						{"missing_outgoing", "LATIN1", "utf8_to_iso_8859_1"},
					} {
						t.Run(candidate.name, func(t *testing.T) {
							issuer := serverDial(t, dsn)
							serverWantCode(t, issuer.login(t, dsn, 196608), "")
							clientEncodingCommand(t, issuer, "SET client_encoding='LATIN1'", "SET")
							if candidate.seed != "" {
								clientEncodingRetireSeed(t, f.Ctx, pool, candidate.seed, "ce_persist_"+scope.name+"_"+candidate.name)
							}
							frames := issuer.query(t, scope.prefix+" SET client_encoding='"+candidate.encoding+"'")
							serverWantTags(t, frames, "EZ")
							clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': "0A000", 'M': `invalid value for parameter "client_encoding": "` + candidate.encoding + `"`, 'D': "Conversion between " + candidate.encoding + " and UTF8 is not supported."})
							clientEncodingStatus(t, frames, "client_encoding")
							// Validation must neither install into the issuer nor replace
							// the previously published SJIS reset/startup default.
							serverScalar(t, issuer, "SHOW client_encoding", "LATIN1")
							serverScalar(t, issuer, "SELECT '\xe9'::text", "\xe9")
							clientEncodingCommand(t, issuer, "RESET client_encoding", "RESET")
							serverScalar(t, issuer, "SHOW client_encoding", "SJIS")
							serverScalar(t, issuer, "SELECT 42", "42")
							fresh(t)
						})
					}
				})
			}
		})
	}
}
