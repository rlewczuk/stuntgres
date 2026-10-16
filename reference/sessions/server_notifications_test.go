//go:build !noservertests

package reference_test

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// PostgreSQL's working notification transport is the positive oracle. The
// stuntgres server deliberately has a narrower capability than its native API:
// it must fail the selected command, not accept an inaccessible subscription.
func TestReferenceServerNotificationsUnsupported(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for i, command := range []struct{ sql, tag string }{
				{"LISTEN server_notification", "LISTEN"},
				{"NOTIFY server_notification, 'payload'", "NOTIFY"},
				{"UNLISTEN server_notification", "UNLISTEN"},
				{"UNLISTEN *", "UNLISTEN"},
			} {
				t.Run(command.sql, func(t *testing.T) {
					w := serverDial(t, f.DSNFor(b))
					serverWantCode(t, w.login(t, f.DSNFor(b), 196608), "")
					table := fmt.Sprintf("server_notification_rows_%d", i)
					serverNotificationSuccess(t, w.query(t, "CREATE TABLE "+table+" (id int)"), 'I', "CREATE TABLE")

					// The Unicode comment also distinguishes character positions from
					// byte offsets. Rejection is an execution capability error.
					direct := "/* é */ " + command.sql
					frames := w.query(t, direct)
					if b.PG {
						serverNotificationSuccess(t, frames, 'I', command.tag)
					} else {
						serverNotificationUnsupported(t, frames, direct, command.tag, 'I')
						serverWantCompletions(t, frames)
						serverNotificationTags(t, frames, "EZ")
					}
					serverScalar(t, w, "SELECT 42", "42")

					implicit := "INSERT INTO " + table + " VALUES (1); " + command.sql + "; INSERT INTO " + table + " VALUES (2)"
					frames = w.query(t, implicit)
					if b.PG {
						serverNotificationSuccess(t, frames, 'I', "INSERT 0 1", command.tag, "INSERT 0 1")
						serverScalar(t, w, "SELECT count(*) FROM "+table, "2")
					} else {
						serverNotificationUnsupported(t, frames, implicit, command.tag, 'I')
						serverWantCompletions(t, frames, "INSERT 0 1")
						serverNotificationTags(t, frames, "CEZ")
						serverScalar(t, w, "SELECT count(*) FROM "+table, "0")
					}
					serverNotificationSuccess(t, w.query(t, "DELETE FROM "+table), 'I', "DELETE "+map[bool]string{true: "2", false: "0"}[b.PG])

					serverNotificationSuccess(t, w.query(t, "BEGIN; INSERT INTO "+table+" VALUES (3)"), 'T', "BEGIN", "INSERT 0 1")
					frames = w.query(t, command.sql)
					if b.PG {
						serverNotificationSuccess(t, frames, 'T', command.tag)
						serverScalar(t, w, "SELECT count(*) FROM "+table, "1")
					} else {
						serverNotificationUnsupported(t, frames, command.sql, command.tag, 'E')
						serverWantCompletions(t, frames)
						serverNotificationTags(t, frames, "EZ")
						frames = w.query(t, "SELECT 42")
						serverNotificationError(t, frames, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
						serverWantReady(t, frames, 'E')
						serverNotificationTags(t, frames, "EZ")
					}
					serverNotificationSuccess(t, w.query(t, "ROLLBACK"), 'I', "ROLLBACK")
					serverScalar(t, w, "SELECT count(*) FROM "+table, "0")

					// A message-wide precheck would incorrectly erase this explicit
					// committed prefix. Only the implicit suffix is rolled back.
					prefix := "BEGIN; INSERT INTO " + table + " VALUES (4); COMMIT; INSERT INTO " + table + " VALUES (5); " + command.sql + "; INSERT INTO " + table + " VALUES (6)"
					frames = w.query(t, prefix)
					if b.PG {
						serverNotificationSuccess(t, frames, 'I', "BEGIN", "INSERT 0 1", "COMMIT", "INSERT 0 1", command.tag, "INSERT 0 1")
						serverWantValues(t, w.query(t, "SELECT id FROM "+table+" ORDER BY id"), [][]string{{"4"}, {"5"}, {"6"}})
					} else {
						serverNotificationUnsupported(t, frames, prefix, command.tag, 'I')
						serverWantCompletions(t, frames, "BEGIN", "INSERT 0 1", "COMMIT", "INSERT 0 1")
						serverNotificationTags(t, frames, "CCCCEZ")
						serverWantValues(t, w.query(t, "SELECT id FROM "+table+" ORDER BY id"), [][]string{{"4"}})
					}

					// PREPARE has a real notification-effects restriction. A fresh
					// transaction must not inherit effects from rejected/rolled-back
					// commands; preparing and rolling it back also proves recovery.
					gid := fmt.Sprintf("server_notification_clean_%d", i)
					serverNotificationSuccess(t, w.query(t, "BEGIN; INSERT INTO "+table+" VALUES (7); PREPARE TRANSACTION '"+gid+"'"), 'I', "BEGIN", "INSERT 0 1", "PREPARE TRANSACTION")
					serverNotificationSuccess(t, w.query(t, "ROLLBACK PREPARED '"+gid+"'"), 'I', "ROLLBACK PREPARED")
					serverScalar(t, w, "SELECT count(*) FROM "+table+" WHERE id = 7", "0")
				})
			}
			if b.PG {
				t.Run("working-postgres-transport", func(t *testing.T) {
					listener := serverDial(t, f.DSNFor(b))
					sender := serverDial(t, f.DSNFor(b))
					serverWantCode(t, listener.login(t, f.DSNFor(b), 196608), "")
					serverWantCode(t, sender.login(t, f.DSNFor(b), 196608), "")
					serverNotificationSuccess(t, listener.query(t, "LISTEN server_positive; LISTEN server_sentinel"), 'I', "LISTEN", "LISTEN")
					serverNotificationSuccess(t, sender.query(t, "NOTIFY server_positive, 'delivered'"), 'I', "NOTIFY")
					serverNotificationEvent(t, listener.query(t, "SELECT 1"), sender.pid, "server_positive", "delivered")
					for _, unlisten := range []string{"UNLISTEN server_positive", "UNLISTEN *"} {
						serverNotificationSuccess(t, listener.query(t, "LISTEN server_positive"), 'I', "LISTEN")
						serverNotificationSuccess(t, listener.query(t, unlisten), 'I', "UNLISTEN")
						serverNotificationSuccess(t, listener.query(t, "LISTEN server_sentinel"), 'I', "LISTEN")
						serverNotificationSuccess(t, sender.query(t, "NOTIFY server_positive, 'removed'; NOTIFY server_sentinel, 'sentinel'"), 'I', "NOTIFY", "NOTIFY")
						serverNotificationEvent(t, listener.query(t, "SELECT 1"), sender.pid, "server_sentinel", "sentinel")
					}
				})
			}
		})
	}
}

func serverNotificationSuccess(t *testing.T, frames []serverFrame, ready byte, tags ...string) {
	t.Helper()
	serverWantCode(t, frames, "")
	serverWantCompletions(t, frames, tags...)
	serverWantReady(t, frames, ready)
}

func serverNotificationUnsupported(t *testing.T, frames []serverFrame, source, token string, ready byte) {
	t.Helper()
	position := strconv.Itoa(utf8.RuneCountInString(source[:strings.Index(source, token)]) + 1)
	serverNotificationError(t, frames, "0A000", "statement is unsupported", position)
	serverWantReady(t, frames, ready)
}

func serverNotificationError(t *testing.T, frames []serverFrame, code, message, position string) {
	t.Helper()
	serverWantCode(t, frames, code)
	count := 0
	for _, frame := range frames {
		if frame.tag != 'E' {
			continue
		}
		count++
		fields := serverFields(frame.body)
		for key, want := range map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': code, 'M': message, 'P': position, 'D': "", 'H': "", 'W': "", 'p': "", 'q': "", 's': "", 't': "", 'c': "", 'd': "", 'n': ""} {
			if fields[key] != want {
				t.Fatalf("diagnostic field %c = %q want %q: %v", key, fields[key], want, fields)
			}
		}
	}
	if count != 1 {
		t.Fatalf("error count %d want 1: %s", count, serverFrameSummary(frames))
	}
}

func serverNotificationTags(t *testing.T, frames []serverFrame, want string) {
	t.Helper()
	if got := serverTags(frames); got != want {
		t.Fatalf("wire tags %q want %q: %s", got, want, serverFrameSummary(frames))
	}
}

func serverNotificationEvent(t *testing.T, frames []serverFrame, pid uint32, channel, payload string) {
	t.Helper()
	serverNotificationSuccess(t, frames, 'I', "SELECT 1")
	serverWantValues(t, frames, [][]string{{"1"}})
	count := 0
	for _, frame := range frames {
		if frame.tag != 'A' {
			continue
		}
		count++
		if len(frame.body) < 6 || binary.BigEndian.Uint32(frame.body) != pid || pid == 0 || string(frame.body[4:]) != channel+"\x00"+payload+"\x00" {
			t.Fatalf("NotificationResponse %v want PID %d channel %q payload %q", frame.body, pid, channel, payload)
		}
	}
	if count != 1 {
		t.Fatalf("notification count %d want 1: %s", count, serverFrameSummary(frames))
	}
}
