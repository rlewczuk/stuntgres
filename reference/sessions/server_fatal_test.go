//go:build !noservertests

package reference_test

import (
	"io"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerFatalSession(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			owner := serverAuthenticated(t, dsn)
			serverWantCode(t, owner.query(t, "CREATE TABLE server_fatal(v int)"), "")
			for _, mode := range []string{"transaction", "idle"} {
				t.Run(mode, func(t *testing.T) {
					victim := serverAuthenticated(t, dsn)
					want := "57P05"
					if mode == "transaction" {
						serverWantCode(t, owner.query(t, "BEGIN; LOCK TABLE server_fatal IN ACCESS EXCLUSIVE MODE"), "")
						defer func() { serverWantCode(t, owner.query(t, "ROLLBACK"), "") }()
						serverWantCode(t, victim.query(t, "SET transaction_timeout='200ms'; BEGIN"), "")
						victim.send(t, serverPacket('Q', []byte("INSERT INTO server_fatal VALUES(99)\x00")))
						want = "25P04"
					} else {
						serverWantCode(t, victim.query(t, "SET idle_session_timeout='100ms'"), "")
					}
					first := victim.frame(t)
					serverWantCode(t, []serverFrame{first}, want)
					fields := serverFields(first.body)
					if first.tag != 'E' || fields['S'] != "FATAL" || fields['V'] != "FATAL" {
						t.Fatalf("fatal session diagnostic%v", fields)
					}
					next, err := victim.readFrame()
					if err != io.EOF {
						t.Fatalf("FATAL must close without ReadyForQuery: tag=%c error=%v", next.tag, err)
					}
					victim.Close()
				})
			}
			serverScalar(t, owner, "SELECT count(*) FROM server_fatal", "0")
			serverScalar(t, owner, "SELECT 42", "42")
		})
	}
}
