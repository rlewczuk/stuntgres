package fixture

import (
	"database/sql"
	"testing"
)

func JSONPathNavigateBoth(t *testing.T, p *Pair, b Backend, c *sql.Conn, s Step) {
	t.Helper()
	RunStep(t, p.Ctx, c, b.Recorder, s)
	RunStep(t, p.Ctx, c, b.Recorder, Prepared(s))
}
