package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDatetimeJustifyFeatureSmoke(t *testing.T) {
	datetimeJustifyScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		s := referenceSelect("SELECT justify_days(INTERVAL '1 year 65 days') AS days,justify_hours(INTERVAL '50 hours 10 minutes') AS hours,justify_interval(INTERVAL '1 mon -1 hour') AS combined", []string{"days", "hours", "combined"}, []string{"INTERVAL", "INTERVAL", "INTERVAL"}, [][]any{{"1 year 2 mons 5 days", "2 days 02:10:00", "29 days 23:00:00"}})
		datetimeJustifyBoth(t, p, b, c, s)
		datetimeJustifyHeld(t, p, b, c, "justify_interval")
		datetimeJustifyValue(t, p, b, c, "justify_interval(INTERVAL '2147483647 days 24 hours')", "5965232 years 4 mons 8 days")
		datetimeJustifyFailure(t, p, b, c, "SELECT justify_hours(INTERVAL '2147483647 days 24 hours')", "22008", "interval out of range", "", 0)
		datetimeJustifyFailure(t, p, b, c, "SELECT justify_days(1)", "42883", "function justify_days(integer) does not exist", datetimeJustifyHint, 8)
		datetimeJustifyAtomicity(t, p, b, c)
	})
}
