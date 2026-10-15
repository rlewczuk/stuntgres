//go:build !noservertests

package reference_test

import (
	"encoding/binary"
	"io"
	"stuntgres/reference/fixture"
	"testing"
)

func serverStartupContracts(t *testing.T, f *fixture.ServerFixture, b fixture.Backend) {
	dsn := f.DSNFor(b)
	t.Run("startup_settings", func(t *testing.T) {
		w := serverDial(t, dsn)
		frames := w.login(t, dsn, 196608, "options", "-c application_name=option\\ name --TimeZone=UTC", "application_name", "individual name")
		serverWantCode(t, frames, "")
		if w.statuses["application_name"] != "individual name" || w.statuses["client_encoding"] != "UTF8" || w.statuses["integer_datetimes"] != "on" {
			t.Fatal(w.statuses)
		}
		serverScalar(t, w, "SHOW application_name", "individual name")
		serverScalar(t, w, "SHOW TimeZone", "UTC")
	})
	// Major 2 selects PostgreSQL's untagged legacy error framing, not protocol 3.
	// Major 4 is unsupported while retaining the tagged diagnostic witness.
	for _, c := range []struct {
		name    string
		version uint32
		extra   []string
		code    string
	}{{"wrong_major", 262144, nil, "0A000"}, {"unknown_setting", 196608, []string{"server_unknown_setting", "x"}, "42704"}, {"replication", 196608, []string{"replication", "database"}, "0A000"}, {"client_encoding", 196608, []string{"client_encoding", "LATIN1"}, "0A000"}} {
		t.Run(c.name, func(t *testing.T) {
			w := serverDial(t, dsn)
			frames := w.login(t, dsn, c.version, c.extra...)
			if b.PG && c.name == "client_encoding" {
				serverWantCode(t, frames, "")
				if w.statuses["client_encoding"] != "LATIN1" {
					t.Fatal(w.statuses)
				}
			} else if b.PG && c.name == "replication" { // PostgreSQL accepts replication startup for its genuine superuser.
				serverWantCode(t, frames, "")
				w.send(t, serverPacket('X', nil))
			} else {
				serverWantCode(t, frames, c.code)
			}
		})
	}
	t.Run("duplicate_startup_key", func(t *testing.T) {
		w := serverDial(t, dsn)
		_, user, password, db := serverEndpoint(t, dsn)
		w.send(t, serverStartup(196608, "user", user, "database", db, "user", user))
		frames := w.authenticate(t, user, password)
		if b.PG {
			serverWantCode(t, frames, "")
			serverScalar(t, w, "SELECT 42", "42")
		} else {
			serverWantCode(t, frames, "08P01")
		}
	})
	t.Run("malformed_startup", func(t *testing.T) {
		w := serverDial(t, dsn)
		packet := serverStartup(196608, "user", f.UserFor(b))
		packet = packet[:len(packet)-1]
		binary.BigEndian.PutUint32(packet, uint32(len(packet)))
		w.send(t, packet)
		serverWantCode(t, []serverFrame{w.frame(t)}, "08P01")
	})
	t.Run("sync_flush_function", func(t *testing.T) {
		w := serverAuthenticated(t, dsn)
		w.send(t, serverPacket('H', nil))
		w.send(t, serverPacket('S', nil))
		if frames := w.ready(t); serverTags(frames) != "Z" {
			t.Fatal(frames)
		}
		body := serverPut32(nil, 0)
		body = append(body, 0, 0, 0, 0, 0, 0)
		w.send(t, serverPacket('F', body))
		frames := w.ready(t)
		want := "0A000"
		if b.PG {
			want = "42883"
		}
		serverWantCode(t, frames, want)
		serverScalar(t, w, "SELECT 42", "42")
	})
	t.Run("truncated_startup", func(t *testing.T) {
		w := serverDial(t, dsn)
		w.send(t, []byte{0, 0})
		w.Close()
		serverScalar(t, serverAuthenticated(t, dsn), "SELECT 42", "42")
	})
	t.Run("cancel_no_reply", func(t *testing.T) {
		w := serverDial(t, dsn)
		packet := serverPut32(nil, 16)
		packet = serverPut32(packet, 80877102)
		packet = serverPut32(packet, 0)
		packet = serverPut32(packet, 0)
		w.send(t, packet)
		var one [1]byte
		if _, e := w.Read(one[:]); e != io.EOF {
			t.Fatalf("invalid CancelRequest reply: %v", e)
		}
	})
}
