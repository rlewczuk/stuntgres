package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMoneyIntervalSumBoundaries(t *testing.T) {
	for _, c := range []struct {
		name, kind, values, want, code, message string
		count                                   int64
	}{
		{"money_max", "MONEY", "(1,'92233720368547758.07'),(2,NULL)", "$92,233,720,368,547,758.07", "", "", 2},
		{"money_min", "MONEY", "(1,'-92233720368547758.08')", "-$92,233,720,368,547,758.08", "", "", 1},
		{"money_positive_overflow", "MONEY", "(1,'92233720368547758.07'),(2,'0.01')", "", "22003", "money out of range", 2},
		{"money_negative_overflow", "MONEY", "(1,'-92233720368547758.08'),(2,'-0.01')", "", "22003", "money out of range", 2},
		{"months_positive_overflow", "INTERVAL", "(1,'2147483647 months'),(2,'1 month')", "", "22008", "interval out of range", 2},
		{"months_negative_overflow", "INTERVAL", "(1,'-2147483648 months'),(2,'-1 month')", "", "22008", "interval out of range", 2},
		{"days_positive_overflow", "INTERVAL", "(1,'2147483647 days'),(2,'1 day')", "", "22008", "interval out of range", 2},
		{"days_negative_overflow", "INTERVAL", "(1,'-2147483648 days'),(2,'-1 day')", "", "22008", "interval out of range", 2},
		{"time_max", "INTERVAL", "(1,'2562047788:00:54.775807')", "2562047788:00:54.775807", "", "", 1},
		{"time_min", "INTERVAL", "(1,'-2562047788:00:54.775807'),(2,'-1 microsecond')", "-2562047788:00:54.775808", "", "", 2},
		{"time_positive_overflow", "INTERVAL", "(1,'2562047788:00:54.775807'),(2,'1 microsecond')", "", "22008", "interval out of range", 2},
		{"time_negative_overflow", "INTERVAL", "(1,'-2562047788:00:54.775807'),(2,'-1 microsecond'),(3,'-1 microsecond')", "", "22008", "interval out of range", 3},
		{"positive_infinity", "INTERVAL", "(1,'infinity'),(2,'1 month'),(3,NULL)", "infinity", "", "", 3},
		{"negative_infinity", "INTERVAL", "(1,'-infinity'),(2,'1 day'),(3,NULL)", "-infinity", "", "", 3},
		{"repeated_infinity", "INTERVAL", "(1,'infinity'),(2,'infinity')", "infinity", "", "", 2},
		{"opposite_infinities", "INTERVAL", "(1,'infinity'),(2,'-infinity')", "", "22008", "interval out of range", 2},
		{"overflow_after_infinity", "INTERVAL", "(1,'infinity'),(2,'2147483647 days'),(3,'1 day')", "", "22008", "interval out of range", 3},
		{"overflow_before_infinity", "INTERVAL", "(1,'2147483647 days'),(2,'1 day'),(3,'infinity')", "", "22008", "interval out of range", 3},
		{"reserved_positive_tuple", "INTERVAL", "(1,'2147483647 months 2147483647 days 2562047788:00:54.775806'),(2,'1 microsecond')", "", "22008", "interval out of range", 2},
		{"reserved_negative_tuple", "INTERVAL", "(1,'-2147483648 months -2147483648 days -2562047788:00:54.775807'),(2,'-1 microsecond')", "", "22008", "interval out of range", 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			moneyIntervalSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE mis_boundary(id INTEGER,v "+c.kind+")", 0))
				run(fixture.Exec("INSERT INTO mis_boundary VALUES "+c.values, c.count))
				q := "SELECT sum(v ORDER BY id) FROM mis_boundary"
				if c.code != "" {
					moneyIntervalSumExecutionError(t, p, conn, b, q, c.code, c.message, true)
					// Failure never damages the committed input or connection.
					moneyIntervalSumBoth(t, p, conn, b, fixture.Select("SELECT count(*) FROM mis_boundary", []string{"count"}, []string{"INT8"}, [][]any{{c.count}}))
				} else {
					moneyIntervalSumBoth(t, p, conn, b, fixture.Select(q, []string{"sum"}, []string{c.kind}, [][]any{{c.want}}))
				}
			})
		})
	}
}
