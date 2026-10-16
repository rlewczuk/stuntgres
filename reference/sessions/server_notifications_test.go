//go:build !noservertests

package reference_test

import (
	"encoding/binary"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceServerNotifications(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			listener := serverAuthenticated(t, dsn)
			sender := serverAuthenticated(t, dsn)
			serverNotificationSuccess(t, listener.query(t, "LISTEN server_positive; LISTEN server_sentinel"), 'I', "LISTEN", "LISTEN")
			serverNotificationSuccess(t, sender.query(t, "NOTIFY server_positive, 'delivered'"), 'I', "NOTIFY")
			serverWantNotification(t, listener.frame(t), sender.pid, "server_positive", "delivered")
			serverNotificationSuccess(t, sender.query(t, "BEGIN; NOTIFY server_positive, 'rolled-back'; ROLLBACK; NOTIFY server_sentinel, 'after'"), 'I', "BEGIN", "NOTIFY", "ROLLBACK", "NOTIFY")
			serverWantNotification(t, listener.frame(t), sender.pid, "server_sentinel", "after")
			for _, unlisten := range []string{"UNLISTEN server_positive", "UNLISTEN *"} {
				serverWantCode(t, listener.query(t, "LISTEN server_positive"), "")
				serverWantCode(t, listener.query(t, unlisten), "")
				serverWantCode(t, listener.query(t, "LISTEN server_sentinel"), "")
				serverWantCode(t, sender.query(t, "NOTIFY server_positive, 'poison'; NOTIFY server_sentinel, 'sentinel'"), "")
				serverWantNotification(t, listener.frame(t), sender.pid, "server_sentinel", "sentinel")
			}
			serverWantCode(t, listener.query(t, "LISTEN server_self"), "")
			frames := listener.query(t, "NOTIFY server_self, 'self'")
			count := 0
			for _, fr := range frames {
				if fr.tag == 'A' {
					serverWantNotification(t, fr, listener.pid, "server_self", "self")
					count++
				}
			}
			if count != 1 {
				t.Fatalf("self event: %s", serverTags(frames))
			}
			// Preserve committed-prefix, implicit rollback and explicit abort assertions with working notification commands.
			serverWantCode(t, sender.query(t, "CREATE TABLE server_notification_rows(id int)"), "")
			frames = sender.query(t, "INSERT INTO server_notification_rows VALUES (1); NOTIFY server_positive; SELECT 1/0")
			serverWantCode(t, frames, "22012")
			serverScalar(t, sender, "SELECT count(*) FROM server_notification_rows", "0")
			frames = sender.query(t, "BEGIN; INSERT INTO server_notification_rows VALUES (3); NOTIFY server_positive; SELECT 1/0")
			serverWantReady(t, frames, 'E')
			serverNotificationError(t, sender.query(t, "SELECT 42"), "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
			serverWantCode(t, sender.query(t, "ROLLBACK"), "")
			serverScalar(t, sender, "SELECT count(*) FROM server_notification_rows", "0")
			frames = sender.query(t, "BEGIN; INSERT INTO server_notification_rows VALUES (4); COMMIT; INSERT INTO server_notification_rows VALUES (5); NOTIFY server_positive; SELECT 1/0")
			serverWantCode(t, frames, "22012")
			serverWantCompletions(t, frames, "BEGIN", "INSERT 0 1", "COMMIT", "INSERT 0 1", "NOTIFY")
			serverWantValues(t, sender.query(t, "SELECT id FROM server_notification_rows ORDER BY id"), [][]string{{"4"}})
			serverWantCode(t, sender.query(t, "BEGIN; INSERT INTO server_notification_rows VALUES (7); PREPARE TRANSACTION 'server_notification_clean'"), "")
			serverWantCode(t, sender.query(t, "ROLLBACK PREPARED 'server_notification_clean'"), "")
			serverScalar(t, sender, "SELECT count(*) FROM server_notification_rows WHERE id=7", "0")
		})
	}
}
func TestReferenceServerNotificationsOrdering(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			l := serverAuthenticated(t, dsn)
			s := serverAuthenticated(t, dsn)
			serverWantCode(t, l.query(t, "LISTEN server_order"), "")
			serverWantCode(t, s.query(t, "BEGIN; NOTIFY server_order, 'a'; NOTIFY server_order, 'b'; NOTIFY server_order, 'a'; COMMIT; NOTIFY server_order, 'c'"), "")
			for _, payload := range []string{"a", "b", "c"} {
				serverWantNotification(t, l.frame(t), s.pid, "server_order", payload)
			}
			for _, end := range []string{"COMMIT", "ROLLBACK"} {
				serverWantCode(t, l.query(t, "BEGIN; SAVEPOINT s"), "")
				serverWantCode(t, s.query(t, "NOTIFY server_order, 'deferred'"), "")
				frames := l.query(t, "ROLLBACK TO s")
				for _, fr := range frames {
					if fr.tag == 'A' {
						t.Fatal("notification inside transaction")
					}
				}
				serverWantReady(t, frames, 'T')
				frames = l.query(t, end)
				count := 0
				for _, fr := range frames {
					if fr.tag == 'A' {
						serverWantNotification(t, fr, s.pid, "server_order", "deferred")
						count++
					}
				}
				if count != 1 {
					t.Fatalf("outer boundary event %s", serverTags(frames))
				}
			}
			serverWantCode(t, l.query(t, "BEGIN"), "")
			serverWantCode(t, s.query(t, "NOTIFY server_order, 'chain'"), "")
			frames := l.query(t, "COMMIT AND CHAIN")
			serverWantReady(t, frames, 'T')
			for _, fr := range frames {
				if fr.tag == 'A' {
					t.Fatal("CHAIN released event")
				}
			}
			frames = l.query(t, "ROLLBACK")
			for _, fr := range frames {
				if fr.tag == 'A' {
					serverWantNotification(t, fr, s.pid, "server_order", "chain")
				}
			}
		})
	}
}
func TestReferenceServerNotificationsLifetime(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			l := serverAuthenticated(t, dsn)
			s := serverAuthenticated(t, dsn)
			other := serverAuthenticated(t, f.DSNForDatabase(b, fixture.ServerSecondDatabase))
			serverWantCode(t, l.query(t, "LISTEN server_lifetime"), "")
			serverWantCode(t, l.query(t, "BEGIN"), "")
			serverWantCode(t, other.query(t, "NOTIFY server_lifetime, 'wrong-db'"), "")
			serverWantCode(t, s.query(t, "NOTIFY server_lifetime, 'retained'"), "")
			pid := s.pid
			s.send(t, serverPacket('X', nil))
			s.Close()
			frames := l.query(t, "ROLLBACK")
			count := 0
			for _, fr := range frames {
				if fr.tag == 'A' {
					serverWantNotification(t, fr, pid, "server_lifetime", "retained")
					count++
				}
			}
			if count != 1 {
				t.Fatalf("retired sender notification %s", serverTags(frames))
			}
			l.Close()
			fresh := serverAuthenticated(t, dsn)
			serverScalar(t, fresh, "SELECT 42", "42")
		})
	}
}
func serverNotificationSuccess(t *testing.T, frames []serverFrame, ready byte, tags ...string) {
	t.Helper()
	serverWantCode(t, frames, "")
	serverWantCompletions(t, frames, tags...)
	serverWantReady(t, frames, ready)
}
func serverNotificationError(t *testing.T, frames []serverFrame, code, message, position string, context ...string) {
	t.Helper()
	wantContext := ""
	if len(context) > 1 {
		t.Fatal("at most one diagnostic context is expected")
	}
	if len(context) == 1 {
		wantContext = context[0]
	}
	serverWantCode(t, frames, code)
	count := 0
	for _, frame := range frames {
		if frame.tag != 'E' {
			continue
		}
		count++
		fields := serverFields(frame.body)
		for key, want := range map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': code, 'M': message, 'P': position, 'D': "", 'H': "", 'W': wantContext, 'p': "", 'q': "", 's': "", 't': "", 'c': "", 'd': "", 'n': ""} {
			if fields[key] != want {
				t.Fatalf("diagnostic field %c = %q want %q: %v", key, fields[key], want, fields)
			}
		}
	}
	if count != 1 {
		t.Fatalf("error count %d: %s", count, serverFrameSummary(frames))
	}
}
func serverNotificationTags(t *testing.T, f []serverFrame, want string) {
	t.Helper()
	serverWantTags(t, f, want)
}
func serverWantNotification(t *testing.T, fr serverFrame, pid uint32, channel, payload string) {
	t.Helper()
	if fr.tag != 'A' || len(fr.body) < 6 || pid == 0 || binary.BigEndian.Uint32(fr.body) != pid || string(fr.body[4:]) != channel+"\x00"+payload+"\x00" {
		t.Fatalf("notification tag=%c body=%x want PID=%d %q %q", fr.tag, fr.body, pid, channel, payload)
	}
}
