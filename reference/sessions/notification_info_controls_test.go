package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/ngrok/sqlmw"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceNotificationInfoControls(t *testing.T) {
	sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		receiver := notificationInfoConn(t, p, b)
		sender := notificationInfoConn(t, p, b)
		observer := notificationInfoConn(t, p, b)
		const channels = "SELECT pg_listening_channels()"
		heldChannels := notificationInfoHeld(t, p, observer, channels)
		heldUsage := notificationInfoHeld(t, p, observer, notificationInfoUsageSQL)
		sessionMessageSQL(t, p, b, receiver, "LISTEN control_receiver")
		sessionMessageSQL(t, p, b, observer, "LISTEN control_observer")
		notificationInfoChannels(t, p, b, heldChannels, channels, "control_observer")
		notificationInfoWantUsage(t, p, heldUsage, 0)
		sessionMessageSQL(t, p, b, receiver, "BEGIN")
		notificationInfoBatch(t, p, b, sender, "control_receiver", "COMMIT")
		pending := notificationInfoUsage(t, p, heldUsage)
		if pending <= 0 {
			t.Fatal("control setup must have backend-pending pages", pending)
		}
		if !b.PG {
			// Native controls have no PostgreSQL API counterpart. The SQL
			// setup and observations surrounding this phase are shared above.
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, replace := range []struct {
				name  string
				apply func() error
			}{
				{"reset", func() error { return stuntgres.Reset(p.Ctx, p.Native) }},
				{"restore", func() error { return stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())) }},
			} {
				if err := replace.apply(); err != nil {
					t.Fatal(replace.name, err)
				}
				notificationInfoChannels(t, p, b, heldChannels, channels, "control_observer")
				notificationInfoWantUsage(t, p, heldUsage, pending)
			}

			connector, err := stuntgres.NewDriver().OpenConnector("notification-info-restored")
			if err != nil {
				t.Fatal(err)
			}
			restored := sql.OpenDB(connector)
			defer restored.Close()
			if err := stuntgres.Restore(p.Ctx, restored, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			wrappedDriver := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: connector}, fixture.Interceptor{})
			configured, err := wrappedDriver.(driver.DriverContext).OpenConnector("")
			if err != nil {
				t.Fatal(err)
			}
			wrapped := sql.OpenDB(configured)
			t.Cleanup(func() {
				if err := wrapped.Close(); err != nil {
					t.Error(err)
				}
			})
			freshBackend := b
			freshBackend.DB = wrapped
			freshBackend.Recorder = &fixture.Recorder{RawBackend: fixture.RawNative}
			fresh := notificationInfoConn(t, p, freshBackend)
			notificationInfoChannels(t, p, freshBackend, fresh, channels)
			notificationInfoWantUsage(t, p, fresh, 0)
			notificationInfoWantUsage(t, p, heldUsage, pending)

			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, stmt := range []*sql.Stmt{heldChannels.Statement, heldUsage.Statement} {
				rows, err := stmt.QueryContext(ctx)
				if rows != nil {
					_ = rows.Close()
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("precanceled held inquiry:", err)
				}
			}
			notificationInfoWantUsage(t, p, heldUsage, pending)
		}
		sessionMessageSQL(t, p, b, receiver, "ROLLBACK")
		notificationInfoWantUsage(t, p, heldUsage, 0)
		notificationInfoChannels(t, p, b, receiver, channels, "control_receiver")
		notificationInfoChannels(t, p, b, heldChannels, channels, "control_observer")
		// Close an unconsumed source, then reuse the same prepared recipe.
		rows, err := heldChannels.Statement.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		notificationInfoChannels(t, p, b, heldChannels, channels, "control_observer")
	})
}
