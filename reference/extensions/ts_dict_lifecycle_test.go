package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTSDictLifecycle(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var db *sql.DB
		for _, b := range p.Backends() {
			if b.PG == pg { db = b.DB }
		}
		peer, err := db.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer peer.Close()
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		a, err := c.PrepareContext(p.Ctx, tsDictQuery)
		if err != nil { t.Fatal(err) }
		defer a.Close()
		b, err := peer.PrepareContext(p.Ctx, tsDictQuery)
		if err != nil { t.Fatal(err) }
		defer b.Close()
		run := func(q string) { fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0)) }
		state := func(ownerPresent, peerPresent bool) (tsDictIdentity, tsDictIdentity) {
			t.Helper()
			owner := tsDictRead(t, p, c, pg, ownerPresent)
			other := tsDictRead(t, p, peer, pg, peerPresent)
			if tsDictHeld(t, p, a, pg, ownerPresent) != owner || tsDictHeld(t, p, b, pg, peerPresent) != other { t.Fatal("held dictionary differs from current catalog") }
			tsDictAvailability(t, p, c, r, ownerPresent)
			tsDictAvailability(t, p, peer, r, peerPresent)
			if ownerPresent && tsDictLinked(t, p, c, r, pg) != owner { t.Fatal("owner identity changed during read") }
			if peerPresent && tsDictLinked(t, p, peer, r, pg) != other { t.Fatal("peer identity changed during read") }
			return owner, other
		}
		run("CREATE SCHEMA ext_addon")
		state(false, false)
		run("BEGIN")
		run("SAVEPOINT before_install")
		run("CREATE EXTENSION unaccent SCHEMA ext_addon")
		state(true, false)
		run("ROLLBACK TO before_install")
		state(false, false)
		run("CREATE EXTENSION unaccent SCHEMA ext_addon")
		state(true, false)
		run("ROLLBACK")
		state(false, false)
		run("BEGIN")
		run("CREATE EXTENSION unaccent SCHEMA ext_addon")
		state(true, false)
		run("COMMIT")
		original, other := state(true, true)
		if original != other { t.Fatal("commit did not publish exact dictionary") }
		capability := fixture.Select("SELECT ext_addon.unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}})
		fixture.RunStep(t, p.Ctx, c, r, capability)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(capability))
		run("BEGIN")
		run("DROP EXTENSION unaccent")
		_, other = state(false, true)
		if other != original { t.Fatal("private drop changed peer identity") }
		run("ROLLBACK")
		restored, other := state(true, true)
		if restored != original || other != original { t.Fatal("drop rollback did not restore exact identity/options") }
		// Native rows are eager immutable captures. pq streams, so this witness
		// deliberately retains only a native result while another connection DDLs.
		var saved *sql.Rows
		if !pg {
			saved, err = peer.QueryContext(p.Ctx, tsDictQuery, "unaccent")
			if err != nil { t.Fatal(err) }
			defer saved.Close()
			tsDictMetadata(t, saved, false)
		}
		run("BEGIN")
		run("DROP EXTENSION unaccent")
		run("COMMIT")
		state(false, false)
		run("CREATE EXTENSION unaccent SCHEMA ext_addon")
		fresh := tsDictLinked(t, p, c, r, pg)
		if fresh.oid == original.oid || fresh.template == original.template { t.Fatal("reinstall reused deleted dictionary/template identity") }
		if saved != nil && tsDictConsume(t, saved, false, true) != original { t.Fatal("retained dictionary changed after drop/reinstall") }
		current, other := state(true, true)
		if current != fresh || other != fresh { t.Fatal("reinstall was not atomically published") }
	})
}
