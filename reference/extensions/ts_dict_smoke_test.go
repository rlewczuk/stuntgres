package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTSDictFeatureSmoke(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0)) }
		held, err := c.PrepareContext(p.Ctx, tsDictQuery)
		if err != nil { t.Fatal(err) }
		defer held.Close()
		tsDictAvailability(t, p, c, r, false)
		tsDictHeld(t, p, held, pg, false)
		tsDictDiagnostic(t, p, c, r, "SELECT missing FROM pg_catalog.pg_ts_dict WHERE FALSE", "8")
		run("CREATE SCHEMA ext_addon")
		run("BEGIN")
		run("CREATE EXTENSION unaccent SCHEMA ext_addon")
		tsDictLinked(t, p, c, r, pg)
		tsDictHeld(t, p, held, pg, true)
		run("ROLLBACK")
		tsDictHeld(t, p, held, pg, false)
		tsDictAvailability(t, p, c, r, false)
		run("CREATE EXTENSION unaccent SCHEMA ext_addon")
		original := tsDictLinked(t, p, c, r, pg)
		if tsDictHeld(t, p, held, pg, true) != original { t.Fatal("held install identity") }
		tsDictAvailability(t, p, c, r, true)
		capability := fixture.Select("SELECT ext_addon.unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}})
		fixture.RunStep(t, p.Ctx, c, r, capability)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(capability))
		run("BEGIN")
		run("DROP EXTENSION unaccent")
		tsDictHeld(t, p, held, pg, false)
		tsDictAvailability(t, p, c, r, false)
		run("ROLLBACK")
		if tsDictHeld(t, p, held, pg, true) != original { t.Fatal("drop rollback identity/options") }
		run("DROP EXTENSION unaccent")
		tsDictHeld(t, p, held, pg, false)
		tsDictAvailability(t, p, c, r, false)
		run("CREATE EXTENSION unaccent SCHEMA ext_addon")
		fresh := tsDictLinked(t, p, c, r, pg)
		if fresh.oid == original.oid || fresh.template == original.template || tsDictHeld(t, p, held, pg, true) != fresh { t.Fatal("reinstall did not replace held identity") }
		tsDictAvailability(t, p, c, r, true)
	})
}
