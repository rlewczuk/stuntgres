package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerSettingLifecycle(t *testing.T) {
	t.Run("sessions", func(t *testing.T) {
		eventTriggerScenario(t, eventTriggerPhaseNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			d := eventDDLCase{t, p, b, c, eventDDLRoute{}}
			eventTriggerPhases(t, p, b, c, "'CREATE TABLE'")
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer peer.Close()
			other := eventDDLCase{t, p, b, peer, eventDDLRoute{}}
			d.exec("SET event_triggers=off")
			eventSettingShow(d, "off")
			eventSettingShow(other, "on")
			d.utility("CREATE TABLE et_session_a(v integer)")
			d.audit()
			other.utility("CREATE TABLE et_session_b(v integer)")
			eventSettingPhases(other, true, "CREATE TABLE", 1, 4)
			eventSettingTarget(d, "et_session_a", 1)
			eventSettingTarget(other, "et_session_b", 1)
			eventSettingShow(d, "off")
			eventSettingShow(other, "on")
		})
	})
	for _, tc := range []struct{ name, setting, end string; savepoint bool; retained bool }{
		{"session_rollback", "SET event_triggers=off", "ROLLBACK", false, false},
		{"local_commit", "SET LOCAL event_triggers=off", "COMMIT", false, true},
		{"local_rollback", "SET LOCAL event_triggers=off", "ROLLBACK", false, false},
		{"local_savepoint", "SET LOCAL event_triggers=off", "COMMIT", true, false},
		{"session_savepoint", "SET event_triggers=off", "COMMIT", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eventTriggerScenario(t, eventTriggerPhaseNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				d := eventDDLCase{t, p, b, c, eventDDLRoute{}}
				eventTriggerPhases(t, p, b, c, "'CREATE TABLE'")
				eventSettingShow(d, "on")
				d.exec("BEGIN")
				if tc.savepoint { d.exec("SAVEPOINT setting_entry") }
				d.exec(tc.setting)
				eventSettingShow(d, "off")
				d.utility("CREATE TABLE et_lifetime_silent(v integer)")
				d.audit()
				eventSettingTarget(d, "et_lifetime_silent", 1)
				if tc.savepoint {
					d.exec("ROLLBACK TO setting_entry")
					eventSettingShow(d, "on")
					eventSettingTarget(d, "et_lifetime_silent", 0)
					d.audit()
					// Callback rows and DDL after restoration are genuinely transactional.
					d.utility("CREATE TABLE et_lifetime_rolled_audit(v integer)")
					eventSettingPhases(d, true, "CREATE TABLE", 1, 4)
					d.exec("ROLLBACK TO setting_entry")
					eventSettingTarget(d, "et_lifetime_rolled_audit", 0)
					d.audit()
				}
				d.exec(tc.end)
				eventSettingShow(d, "on")
				count := int64(0)
				if tc.retained { count = 1 }
				eventSettingTarget(d, "et_lifetime_silent", count)
				d.audit()
				d.utility(fmt.Sprintf("CREATE TABLE et_lifetime_after_%s(v integer)", tc.name))
				eventSettingPhases(d, true, "CREATE TABLE", 1, 4)
			})
		})
	}
}
