package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceNotificationInfoQueue(t *testing.T) {
	sessionMessagesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		receiver := notificationInfoConn(t, p, b)
		second := notificationInfoConn(t, p, b)
		sender := notificationInfoConn(t, p, b)
		observer := notificationInfoConn(t, p, b)
		held := notificationInfoHeld(t, p, observer, notificationInfoUsageSQL)
		notificationInfoWantUsage(t, p, observer, 0)
		notificationInfoWantUsage(t, p, held, 0)
		sessionMessageSQL(t, p, b, receiver, "LISTEN notification_info")
		sessionMessageSQL(t, p, b, second, "LISTEN notification_info")

		for _, ending := range []string{"COMMIT", "ROLLBACK"} {
			sessionMessageSQL(t, p, b, receiver, "BEGIN")
			sessionMessageSQL(t, p, b, second, "BEGIN")
			notificationInfoBatch(t, p, b, sender, "notification_info", "COMMIT")
			pending := notificationInfoUsage(t, p, observer)
			if pending <= 0 {
				t.Fatal("multiple pages waiting for busy receivers must occupy queue", pending)
			}
			notificationInfoWantUsage(t, p, held, pending)
			notificationInfoWantUsage(t, p, sender, pending)
			// Inquiry on a busy receiver cannot scan away its unread hold.
			notificationInfoWantUsage(t, p, receiver, pending)
			notificationInfoBatch(t, p, b, sender, "notification_info", "ROLLBACK")
			notificationInfoWantUsage(t, p, observer, pending)
			sessionMessageSQL(t, p, b, receiver, ending)
			// One shared stream, not summed per-recipient usage: releasing the
			// first listener must leave the identical oldest hold on the second.
			notificationInfoWantUsage(t, p, held, pending)
			sessionMessageSQL(t, p, b, second, ending)
			notificationInfoWantUsage(t, p, observer, 0)
			notificationInfoWantUsage(t, p, held, 0)
			// No WaitForNotification: both clients still have unread Ready data.
		}

		// Registered backends scan the global stream, even for unmatched channels.
		sessionMessageSQL(t, p, b, receiver, "BEGIN")
		notificationInfoBatch(t, p, b, sender, "notification_info_unmatched", "COMMIT")
		if got := notificationInfoUsage(t, p, held); got <= 0 {
			t.Fatal("unmatched traffic must retain busy listener queue pages", got)
		}
		sessionMessageSQL(t, p, b, receiver, "ROLLBACK")
		notificationInfoWantUsage(t, p, held, 0)
	})
}
