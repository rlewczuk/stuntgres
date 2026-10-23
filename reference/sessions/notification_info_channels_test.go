package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceNotificationInfoChannels(t *testing.T) {
	sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn := notificationInfoConn(t, p, b)
		peer := notificationInfoConn(t, p, b)
		const target = "SELECT pg_listening_channels()"
		const from = "SELECT channel FROM pg_listening_channels() AS c(channel) ORDER BY channel"
		targetHeld := notificationInfoHeld(t, p, conn, target)
		fromHeld := notificationInfoHeld(t, p, conn, from)
		check := func(names ...string) {
			t.Helper()
			notificationInfoChannels(t, p, b, conn, target, names...)
			notificationInfoChannels(t, p, b, targetHeld, target, names...)
			notificationInfoChannels(t, p, b, fromHeld, from, names...)
			notificationInfoChannels(t, p, b, conn, "SELECT pg_catalog.pg_listening_channels()", names...)
			notificationInfoChannels(t, p, b, peer, target)
		}
		ordinality := func(want [][]any) {
			t.Helper()
			s := fixture.Select("SELECT channel, n FROM pg_listening_channels() WITH ORDINALITY AS c(channel,n)",
				[]string{"channel", "n"}, []string{"TEXT", "INT8"}, want)
			s.Bag = true
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				columns, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if len(columns) != 2 || columns[0].ScanType() != reflect.TypeOf("") || columns[1].ScanType() != reflect.TypeOf(int64(0)) {
					t.Fatal("ordinality TEXT/INT8 scan metadata", columns)
				}
			}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
		check()
		ordinality(nil)
		for range 2 {
			sessionMessageSQL(t, p, b, conn, "LISTEN a")
		}
		check("a")
		ordinality([][]any{{"a", int64(1)}})
		sessionMessageSQL(t, p, b, conn, `LISTEN "Mixed雪"`)
		check("Mixed雪", "a")
		sessionMessageSQL(t, p, b, conn, "UNLISTEN a")
		check("Mixed雪")
		sessionMessageSQL(t, p, b, conn, "UNLISTEN *")
		check()

		sessionMessageSQL(t, p, b, conn, "LISTEN a")
		for _, end := range []string{"ROLLBACK", "COMMIT"} {
			sessionMessageSQL(t, p, b, conn, "BEGIN")
			sessionMessageSQL(t, p, b, conn, "UNLISTEN a")
			sessionMessageSQL(t, p, b, conn, "LISTEN b")
			check("a") // The current transaction has not published its actions.
			sessionMessageSQL(t, p, b, conn, end)
			if end == "ROLLBACK" {
				check("a")
			} else {
				check("b")
			}
		}
		sessionMessageSQL(t, p, b, conn, "BEGIN")
		sessionMessageSQL(t, p, b, conn, "SAVEPOINT child")
		sessionMessageSQL(t, p, b, conn, "UNLISTEN b")
		sessionMessageSQL(t, p, b, conn, "LISTEN discarded")
		sessionMessageSQL(t, p, b, conn, "ROLLBACK TO child")
		sessionMessageSQL(t, p, b, conn, "RELEASE child")
		sessionMessageSQL(t, p, b, conn, "SAVEPOINT retained")
		sessionMessageSQL(t, p, b, conn, "LISTEN c")
		sessionMessageSQL(t, p, b, conn, "RELEASE retained")
		check("b")
		sessionMessageSQL(t, p, b, conn, "COMMIT")
		check("b", "c")
		sessionMessageSQL(t, p, b, conn, "UNLISTEN *")
		check()
	})
}
