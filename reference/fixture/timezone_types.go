package fixture

import (
	"testing"
)

// Independent constants are asserted by the existing recorded public-driver
// fixture before backend parity. Each route owns a single physical session.
func TimezoneReferencePair(t *testing.T) *Pair {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		backend.DB.SetMaxOpenConns(1)
		backend.DB.SetMaxIdleConns(1)
	}
	p.Step(t, Exec("SET TIME ZONE 'UTC'", 0))
	p.Step(t, Exec("SET DateStyle='ISO, MDY'", 0))
	p.Step(t, Exec("CREATE TABLE tz_witness(id INTEGER,z TIMESTAMPTZ(3),q TIMETZ(3),d DATE,c TIMESTAMP)", 0))
	p.Step(t, Exec("INSERT INTO tz_witness VALUES(1,NULL,NULL,NULL,NULL)", 1))
	return p
}
