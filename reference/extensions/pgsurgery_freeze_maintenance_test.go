package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func freezeVacuumRepairCase(t *testing.T) {
	t.Run("vacuum_retained", func(t *testing.T) {
		freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			horizon := freezeHorizon(t, p, b)
			defer horizon.Close()
			defer horizon.ExecContext(p.Ctx, "ROLLBACK")
			a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
			z := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
			next := freezeOne(t, p, c, "UPDATE fs_part.heap SET v='B' WHERE k=1 RETURNING k,v,ctid,xmin,xmax")
			freezeApply(t, p, b, c, a.tid)
			freezeExpect(t, p, b, c, freezeRepaired(a), next, z)
			freezeRun(t, p, b, horizon, fixture.Exec("ROLLBACK", 0))
			freezeRun(t, p, b, c, fixture.Exec("VACUUM fs_part.heap", 0))
			freezeExpect(t, p, b, c, freezeRepaired(a), next, z)
		})
	})
}
