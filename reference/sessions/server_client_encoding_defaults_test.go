//go:build !noservertests

package reference_test

import (
	"context"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func clientEncodingPersistedDefaults(t *testing.T, f *fixture.ServerFixture, b fixture.Backend) {
	dsn := f.DSNFor(b)
	_, user, _, db := serverEndpoint(t, dsn)
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	role, database := quote(user), quote(db)
	admin := serverAuthenticated(t, dsn)
	pool := f.RawPool(t, b)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, q := range []string{"ALTER ROLE " + role + " IN DATABASE " + database + " RESET client_encoding", "ALTER ROLE " + role + " RESET client_encoding", "ALTER ROLE ALL IN DATABASE " + database + " RESET client_encoding"} {
			if _, err := pool.ExecContext(ctx, q); err != nil {
				t.Error("restore persisted defaults", q, err)
			}
		}
	})
	clientEncodingCommand(t, admin, "ALTER ROLE ALL IN DATABASE "+database+" SET client_encoding='SJIS'", "ALTER ROLE")
	check := func(want string) {
		w := serverDial(t, dsn)
		defer w.Close()
		frames := w.login(t, dsn, 196608)
		if serverCode(frames) != "" || w.statuses["client_encoding"] != want {
			t.Fatalf("persisted startup want %s: status=%q %s", want, w.statuses, clientEncodingTranscript(frames))
		}
		serverScalar(t, w, "SHOW client_encoding", want)
		clientEncodingCommand(t, w, "SET client_encoding='SQL_ASCII'; RESET client_encoding", "SET", "RESET")
		serverScalar(t, w, "SHOW client_encoding", want)
	}
	check("SJIS")
	clientEncodingCommand(t, admin, "ALTER ROLE "+role+" SET client_encoding='LATIN1'", "ALTER ROLE")
	check("LATIN1")
	clientEncodingCommand(t, admin, "ALTER ROLE "+role+" IN DATABASE "+database+" SET client_encoding='EUC_JP'", "ALTER ROLE")
	check("EUC_JP")
	w := clientEncodingLogin(t, dsn, "UTF8")
	clientEncodingCommand(t, w, "SET client_encoding='LATIN1'; RESET client_encoding", "SET", "RESET")
	serverScalar(t, w, "SHOW client_encoding", "UTF8")
}
