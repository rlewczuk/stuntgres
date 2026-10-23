package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceNotificationInfoCluster(t *testing.T) {
	const database = "notification_info_cluster"
	names := []string{database}
	fixture.DatabaseReferenceCleanup(t, &names)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			sender := notificationInfoConn(t, p, b)
			observer := notificationInfoConn(t, p, b)
			sessionMessageSQL(t, p, b, sender, "CREATE DATABASE "+database)
			pool, _, recorder := fixture.DatabaseScenarioPool(t, p, b.PG, database, "")
			foreign := b
			foreign.DB, foreign.Recorder = pool, recorder
			receiver := notificationInfoConn(t, p, foreign)
			foreignObserver := notificationInfoConn(t, p, foreign)
			held := notificationInfoHeld(t, p, foreignObserver, notificationInfoUsageSQL)
			notificationInfoWantUsage(t, p, observer, 0)
			notificationInfoWantUsage(t, p, held, 0)
			sessionMessageSQL(t, p, foreign, receiver, "LISTEN foreign_only")
			sessionMessageSQL(t, p, foreign, receiver, "BEGIN")
			notificationInfoBatch(t, p, b, sender, "foreign_only", "COMMIT")
			pending := notificationInfoUsage(t, p, observer)
			if pending <= 0 {
				t.Fatal("busy listener in another database must hold global queue pages", pending)
			}
			notificationInfoWantUsage(t, p, held, pending)
			notificationInfoWantUsage(t, p, receiver, pending)
			sessionMessageSQL(t, p, foreign, receiver, "ROLLBACK")
			// An explicit command on the same backend supplies a deterministic
			// post-transaction processing boundary, without sleeps or polling.
			notificationInfoChannels(t, p, foreign, receiver, "SELECT pg_listening_channels()", "foreign_only")
			notificationInfoWantUsage(t, p, observer, 0)
			notificationInfoWantUsage(t, p, held, 0)
		})
	}
}
